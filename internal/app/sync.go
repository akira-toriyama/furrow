package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// retryPolicy bounds how long Sync waits out a transient foreign rebase (a
// concurrent writer momentarily holding `pull --rebase`) before giving up.
type retryPolicy struct {
	base   time.Duration // first backoff
	factor int           // per-attempt multiplier
	cap    time.Duration // per-sleep ceiling
	max    int           // maximum number of sleeps
}

// defaultRebaseWait retries for ~4.7s (100+200+400+800+1600+1600ms) — long
// enough to ride out a concurrent writer's sub-second rebase window, short
// enough that a genuinely stuck rebase surfaces promptly.
var defaultRebaseWait = retryPolicy{
	base:   100 * time.Millisecond,
	factor: 2,
	cap:    1600 * time.Millisecond,
	max:    6,
}

// waitForRebaseToClear polls check (a mid-operation probe returning op+busy),
// sleeping with bounded exponential backoff between polls, until the repo is no
// longer mid-rebase or the policy budget is exhausted. It only waits out a
// "rebase" — the concurrent-writer signature; any other in-progress op (a
// user's own "merge") is never transient, so it returns immediately. Returns
// the last observed op and whether the repo is now clear (no op in progress).
func waitForRebaseToClear(check func() (string, bool), sleep func(time.Duration), pol retryPolicy) (string, bool) {
	op, busy := check()
	if !busy {
		return op, true
	}
	if op != "rebase" {
		return op, false
	}
	backoff := pol.base
	for i := 0; i < pol.max; i++ {
		sleep(backoff)
		op, busy = check()
		if !busy {
			return op, true
		}
		if op != "rebase" {
			return op, false
		}
		if backoff *= time.Duration(pol.factor); backoff > pol.cap {
			backoff = pol.cap
		}
	}
	return op, false
}

// DefaultSyncMessage is the auto-commit message `furrow sync` uses when
// --message is not given: gitmoji + Conventional, and chore = no version bump,
// which is exactly right for board data.
const DefaultSyncMessage = ":card_file_box: chore(board): sync via furrow"

// SyncOpts controls one Sync. Message overrides the auto-commit subject. Bodies
// names task ids whose hand-edited bodies/<id>.md this sync should commit — on a
// shared board a merely-modified body is otherwise left for its own author
// (reported in PendingBodies) rather than swept in under the wrong hands.
// AllBodies commits every dirty body (the pre-scoping sweep), for a checkout you
// know is yours alone.
type SyncOpts struct {
	Message   string
	Bodies    []string
	AllBodies bool
}

// SyncProgress is the machine-readable record of how far a sync got. It is
// emitted on stdout on success AND on failure, so an agent can tell "the
// auto-commit happened but the push didn't" instead of guessing from an error
// string.
type SyncProgress struct {
	Committed bool `json:"committed"` // a board auto-commit was created
	Pulled    bool `json:"pulled"`    // pull --rebase completed
	Pushed    bool `json:"pushed"`    // push completed
	Conflict  bool `json:"conflict"`  // pull hit conflicts (rebase was aborted)
	// CommittedBodies lists task ids whose bodies/<id>.md this sync committed
	// (new/seeded bodies, or ones named via -b/--all-bodies). PendingBodies lists
	// modified bodies deliberately LEFT uncommitted on a shared board — rerun with
	// -b <id> (or --all-bodies) to push them. Both are omitted when empty.
	CommittedBodies []string `json:"committed_bodies,omitempty"`
	PendingBodies   []string `json:"pending_bodies,omitempty"`
}

// partitionSync splits the dirty .furrow paths into what the auto-commit should
// stage. Machine-written files (tasks/, meta.json, config.toml — everything that
// is not a body) are always committed: they are deterministic and complete by
// construction. A hand-edited bodies/<id>.md is committed only when it is
// brand-new (an add/retitle seed, still untracked) or explicitly opted in
// (opts.Bodies or opts.AllBodies); an otherwise-modified body is left
// uncommitted and returned in pendingBodies, so a shared checkout never sweeps a
// co-located operator's in-progress prose under the wrong author. commitPaths
// are the pathspecs to stage; committedBodies/pendingBodies are affected ids,
// sorted (nil when empty, so SyncProgress omits them).
func partitionSync(spec string, changes []gitrepo.Change, opts SyncOpts) (commitPaths, committedBodies, pendingBodies []string) {
	bodiesPrefix := spec + "/bodies/"
	named := make(map[string]bool, len(opts.Bodies))
	for _, id := range opts.Bodies {
		named[id] = true
	}
	for _, ch := range changes {
		if body, isBody := strings.CutPrefix(ch.Path, bodiesPrefix); isBody && strings.HasSuffix(body, ".md") {
			id := strings.TrimSuffix(body, ".md")
			if ch.Untracked || opts.AllBodies || named[id] {
				commitPaths = append(commitPaths, ch.Path)
				committedBodies = append(committedBodies, id)
			} else {
				pendingBodies = append(pendingBodies, id)
			}
			continue
		}
		commitPaths = append(commitPaths, ch.Path) // machine-written: always safe
	}
	sort.Strings(committedBodies)
	sort.Strings(pendingBodies)
	return commitPaths, committedBodies, pendingBodies
}

// Sync is the multi-machine ritual as one command: commit the board (scoped to
// .furrow/ so unrelated dirty files are never swept in — and within it, to
// machine-written shards plus new/opted-in bodies so a co-located operator's
// in-progress body is never committed under the wrong author; see partitionSync),
// pull --rebase (autostash), push (retrying pull→push once on non-fast-forward).
// It is a thin git wrapper by design — no daemon, no sync server (see
// docs/non-goals.md).
//
// Failure contract: a rebase conflict aborts the rebase automatically (the
// board is never left with conflict markers — those would make every later
// furrow command fail in UnmarshalTask; the local sync commit survives) and
// returns a CodeInternal error with ID "sync-conflict" whose Details carry the
// conflicted paths. The returned SyncProgress is meaningful even when err is
// non-nil.
func (a *App) Sync(opts SyncOpts) (*SyncProgress, error) {
	p := &SyncProgress{}

	r, err := gitrepo.Open(a.Dir)
	if err != nil {
		return p, err // non-git board = validation (exit 2), from the adapter
	}
	// A rebase in progress is usually a concurrent writer (the board's bot / a
	// second operator) momentarily holding `pull --rebase`; wait it out with a
	// bounded backoff so agents don't fail spuriously in that sub-second window.
	if op, cleared := waitForRebaseToClear(r.MidOperation, a.sleeper(), defaultRebaseWait); !cleared {
		if op == "rebase" {
			// Still rebasing after the budget: transient in the common case, so
			// classify as retryable (exit 3, not the do-not-retry exit 2) and say
			// how to recover if it's actually a stuck rebase.
			top := r.Toplevel()
			return p, &core.Error{
				Code: core.CodeInternal,
				ID:   "sync-busy",
				Msg: fmt.Sprintf("a git rebase has stayed in progress in %s through several retries — "+
					"this is usually a concurrent writer that clears within a second, so re-running "+
					"'furrow sync' typically succeeds. If it persists a rebase here may be stuck: check "+
					"'git -C %s status' and finish or abort it, then re-run", top, top),
			}
		}
		// Any other in-progress op (a merge you started) is your own to resolve.
		return p, core.Validationf("sync",
			"a git %s is in progress in %s — finish or abort it, then re-run furrow sync", op, r.Toplevel())
	}
	spec, err := r.RelPath(a.Dir)
	if err != nil {
		return p, err
	}

	changes, err := r.DirtyChanges(spec)
	if err != nil {
		return p, err
	}
	if len(changes) > 0 {
		commitPaths, committedBodies, pendingBodies := partitionSync(spec, changes, opts)
		p.PendingBodies = pendingBodies // reported even when there is nothing else to commit
		if len(commitPaths) > 0 {
			message := opts.Message
			if message == "" {
				message = DefaultSyncMessage
			}
			if err := r.Commit(message, commitPaths...); err != nil {
				return p, err
			}
			p.Committed = true
			p.CommittedBodies = committedBodies
		}
	}

	pull := func() error {
		if err := r.PullRebase(); err != nil {
			if r.RebaseInProgress() {
				// Flag the conflict BEFORE attempting the abort: even if the
				// abort itself fails (the one state the contract promises never
				// to leave behind), the progress object and the error must both
				// say "conflict" and carry the paths.
				p.Conflict = true
				paths := r.ConflictedPaths()
				if aerr := r.AbortRebase(); aerr != nil {
					return &core.Error{
						Code: core.CodeInternal,
						ID:   "sync-conflict",
						Msg: fmt.Sprintf("pull --rebase hit conflicts AND the automatic abort failed (%v) — "+
							"run 'git rebase --abort' in %s by hand, then re-run furrow sync", aerr, r.Toplevel()),
						Details: map[string]any{"paths": paths},
					}
				}
				return &core.Error{
					Code: core.CodeInternal,
					ID:   "sync-conflict",
					Msg: "pull --rebase hit conflicts; the rebase was aborted and the board restored " +
						"(your local sync commit is intact). Resolve the paths by hand (pull, fix, commit), then re-run furrow sync",
					Details: map[string]any{"paths": paths},
				}
			}
			return err
		}
		p.Pulled = true
		return nil
	}

	if err := pull(); err != nil {
		return p, err
	}

	if err := r.Push(); err != nil {
		if !errors.Is(err, gitrepo.ErrNonFastForward) {
			return p, err
		}
		// The remote moved between our pull and push: pull once more and retry.
		if err := pull(); err != nil {
			return p, err
		}
		if err := r.Push(); err != nil {
			if errors.Is(err, gitrepo.ErrNonFastForward) {
				return p, core.Internalf("sync", "push kept being rejected after a retry: %v", err)
			}
			return p, err
		}
	}
	p.Pushed = true
	return p, nil
}

// SyncSummary renders the progress as one terse human line (stdout, non-JSON
// mode); the JSON object is the agent-facing form.
func (p *SyncProgress) SyncSummary() string {
	return fmt.Sprintf("sync: committed=%v pulled=%v pushed=%v conflict=%v",
		p.Committed, p.Pulled, p.Pushed, p.Conflict)
}
