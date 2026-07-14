package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// retryPolicy bounds how long Sync waits out a transient concurrent-writer
// condition before giving up: a foreign rebase caught by the pre-flight, or a
// fetch/ref-lock race during the pull (see pullWithRetry).
type retryPolicy struct {
	base   time.Duration // first backoff
	factor int           // per-attempt multiplier
	cap    time.Duration // per-sleep ceiling
	max    int           // maximum number of sleeps
}

// next advances a backoff by the policy's factor, clamped at cap — the one
// place the exponential-backoff step lives, shared by every retry loop here.
func (pol retryPolicy) next(backoff time.Duration) time.Duration {
	if backoff *= time.Duration(pol.factor); backoff > pol.cap {
		return pol.cap
	}
	return backoff
}

// defaultConcurrentWait retries for ~4.7s (100+200+400+800+1600+1600ms) — long
// enough to ride out a concurrent writer's sub-second window (a foreign rebase,
// or a fetch racing ours), short enough that a genuinely stuck state surfaces
// promptly.
var defaultConcurrentWait = retryPolicy{
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
func waitForRebaseToClear(check func() (string, bool), sleep func(time.Duration) error, pol retryPolicy) (string, bool) {
	op, busy := check()
	if !busy {
		return op, true
	}
	if op != "rebase" {
		return op, false
	}
	backoff := pol.base
	for i := 0; i < pol.max; i++ {
		if sleep(backoff) != nil {
			return op, false // cancelled while waiting out a foreign rebase — still in progress
		}
		op, busy = check()
		if !busy {
			return op, true
		}
		if op != "rebase" {
			return op, false
		}
		backoff = pol.next(backoff)
	}
	return op, false
}

// pullWithRetry runs pullOnce and, while it fails with a transient
// concurrent-access race (a co-writer's fetch clobbering FETCH_HEAD or
// contending a ref/index lock in a shared checkout — gitrepo.ErrTransientFetchRace),
// retries with bounded backoff. A LIVE race self-resolves in well under a
// second, so this rides it out silently in the common case. If it outlives the
// whole budget the lock is almost certainly STALE (a crashed git left a
// .git/*.lock) or the ref conflict permanent, so the residual is returned as a
// TERMINAL error naming the recovery — deliberately NOT the retryable
// "sync-busy", which would loop an agent forever on a stale lock (git can't tell
// a stale lock from a live one, but "outlived the retry budget" can). Any other
// outcome — success, a sync-conflict, a real error — is returned immediately and
// unchanged. top is the work-tree root, named in the recovery guidance.
func pullWithRetry(pullOnce func() error, sleep func(time.Duration) error, pol retryPolicy, top string) error {
	err := pullOnce()
	backoff := pol.base
	for i := 0; err != nil && errors.Is(err, gitrepo.ErrTransientFetchRace) && i < pol.max; i++ {
		if serr := sleep(backoff); serr != nil {
			return serr // cancelled mid-backoff — stop retrying and propagate
		}
		err = pullOnce()
		backoff = pol.next(backoff)
	}
	if err != nil && errors.Is(err, gitrepo.ErrTransientFetchRace) {
		return core.Internalf("sync", "furrow sync kept losing a git lock/fetch race in %s across "+
			"several seconds of retries; if no other operator is syncing, a crashed git likely left a "+
			"stale lock — remove a stray .git/*.lock (e.g. .git/index.lock) in that repo, then re-run "+
			"(last error: %v)", top, err)
	}
	return err
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
	// PendingStash lists the autostash entries sitting in `git stash` INSTEAD of in
	// the working tree — changes git stashed for the rebase and then could not put
	// back (see strandedStash). The stash twin of PendingBodies: a leftover is
	// always reported machine-readably, never left to be noticed. Omitted when empty.
	PendingStash []StashEntry `json:"pending_stash,omitempty"`
}

// StashEntry is one autostash entry left holding working-tree changes. Commit is
// the stable handle (the stash@{N} in Ref is an INDEX that shifts as entries are
// pushed and dropped); Paths says which files are in there, so "did it eat my
// body?" is answerable without running git.
type StashEntry struct {
	Ref    string   `json:"ref"`
	Commit string   `json:"commit"`
	Paths  []string `json:"paths"`
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

// autostashCommits is the cheap BEFORE probe: the oids of the autostash entries
// already in the stash when the pull starts. Taking it first is what lets a
// leftover from an earlier sync (or an earlier machine) be told apart from one
// THIS sync just stranded — the difference between a nudge and a failure.
func autostashCommits(ctx context.Context, r *gitrepo.Repo) map[string]bool {
	set := map[string]bool{}
	for _, e := range r.StashEntries(ctx) {
		if e.Subject == gitrepo.AutostashSubject {
			set[e.Commit] = true
		}
	}
	return set
}

// autostashEntries resolves the stash entries git stored on OUR behalf — subject
// "autostash", never an operator's own `git stash` ("WIP on …") — to the reported
// form (ref + stable oid + the paths they are holding hostage).
func autostashEntries(ctx context.Context, r *gitrepo.Repo) []StashEntry {
	var out []StashEntry
	for _, e := range r.StashEntries(ctx) {
		if e.Subject != gitrepo.AutostashSubject {
			continue
		}
		out = append(out, StashEntry{Ref: e.Ref, Commit: e.Commit, Paths: r.StashedPaths(ctx, e.Commit)})
	}
	return out
}

// strandedStash is the AFTER probe: the autostash entries holding working-tree
// changes once the pull has finished (or been aborted), plus whether any of them
// is new — i.e. whether this sync is the one that stranded it.
//
// This exists because git's failure here is silent by construction. `git rebase
// --autostash` re-applies the stash at the end (or on abort); when that apply
// conflicts, git stores the entry back in the stash, prints a warning to stderr,
// and exits 0. The dirty files are simply gone from the working tree. So an exit
// code cannot see it and neither can a rebase-in-progress probe — only the stash
// itself can. We compare refs rather than grep git's warning because git localizes
// its prose but not `stash store -m autostash`'s subject.
func strandedStash(ctx context.Context, r *gitrepo.Repo, before map[string]bool) (all []StashEntry, fresh bool) {
	all = autostashEntries(ctx, r)
	for _, e := range all {
		if !before[e.Commit] {
			fresh = true
		}
	}
	return all, fresh
}

// stashSummary renders the entries for a human error line: "stash@{0} (bodies/t-x.md)".
func stashSummary(entries []StashEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Ref
		if len(e.Paths) > 0 {
			parts[i] += " (" + strings.Join(e.Paths, ", ") + ")"
		}
	}
	return strings.Join(parts, "; ")
}

// guardBodyMarkers refuses to auto-commit a body that still carries git conflict
// markers. It is the PREVENTION half of the conflict-marker rule; `furrow lint`'s
// conflict-marker finding is the DETECTION half, for a board that already has one.
//
// The two halves are not redundant: a failed autostash re-apply leaves markers in
// the working tree (git merges the stash in and keeps what conflicts), and the very
// next sync would sweep that body into a commit — at which point the corruption is
// on every other machine and in git history, which cannot be un-published. Lint
// tells you afterwards; this refuses beforehand.
//
// Validation (exit 2, do-not-retry): the fix is an edit, not a re-run.
func (a *App) guardBodyMarkers(ids []string) error {
	type markedBody struct {
		ID    string `json:"id"`
		Path  string `json:"path"`
		Lines []int  `json:"lines"`
	}
	bad := []markedBody{}
	for _, id := range ids {
		body, err := a.Store.LoadBody(id)
		if err != nil {
			return err
		}
		if lines := core.ConflictMarkerLines(body); len(lines) > 0 {
			bad = append(bad, markedBody{ID: id, Path: core.BodyPath(id), Lines: lines})
		}
	}
	if len(bad) == 0 {
		return nil
	}
	named := make([]string, len(bad))
	for i, b := range bad {
		named[i] = fmt.Sprintf("%s (line %s)", b.Path, joinInts(b.Lines))
	}
	return &core.Error{
		Code: core.CodeValidation,
		ID:   "body-conflict-marker",
		Msg: fmt.Sprintf("refusing to commit %d body file(s) still carrying git conflict markers: %s — "+
			"a half-merged body is half a progress record, and a commit cannot be un-published. Resolve the "+
			"markers (the other half is often still in `git stash`), then re-run furrow sync",
			len(bad), strings.Join(named, ", ")),
		Details: map[string]any{"bodies": bad},
	}
}

// interruptError collapses err into one honest "sync-interrupted" error when the
// sync was cancelled (ctxErr != nil): a Ctrl-C / SIGTERM kills the in-flight git
// via exec.CommandContext, and that otherwise surfaces however the step then
// running classified a failed git — a cancelled rev-parse in Open looks like "not
// a git repository", a killed fetch like "git fetch: (no output)". With no
// cancellation (or no error), err passes through unchanged.
func interruptError(err error, ctxErr error) error {
	if err == nil || ctxErr == nil {
		return err
	}
	// A sync-conflict is a DELIBERATE, definitive outcome, not a cancellation
	// artifact: the rebase was detected, aborted, and the board restored (the
	// abort runs detached from ctx — see Repo.AbortRebase), and it carries the
	// contract's Details.paths. A signal racing that handling must not mask it —
	// dropping the paths and claiming the board "may be left mid-operation" would
	// both be false. Everything else under a cancelled ctx is a killed-subprocess
	// artifact, so collapse it.
	if fe := core.AsError(err); fe != nil && fe.ID == "sync-conflict" {
		return err
	}
	return &core.Error{
		Code: core.CodeInternal,
		ID:   "sync-interrupted",
		Msg: "furrow sync was interrupted; the board may be left mid-operation " +
			"(re-run furrow sync — its pre-flight waits out or reports any in-progress rebase)",
	}
}

// Sync is the multi-machine ritual as one command: commit the board (scoped to
// .furrow/ so unrelated dirty files are never swept in — and within it, to
// machine-written shards plus new/opted-in bodies so a co-located operator's
// in-progress body is never committed under the wrong author; see partitionSync),
// fetch + rebase --autostash @{u} (onto the tracking ref, not FETCH_HEAD), push
// (retrying pull→push once on non-fast-forward).
// It is a thin git wrapper by design — no daemon, no sync server (see
// docs/non-goals.md).
//
// Failure contract: a rebase conflict aborts the rebase automatically (the
// board is never left with conflict markers — those would make every later
// furrow command fail in UnmarshalTask; the local sync commit survives) and
// returns a CodeInternal error with ID "sync-conflict" whose Details carry the
// conflicted paths. The returned SyncProgress is meaningful even when err is
// non-nil.
//
// Two further ids exist because a sync can lose WORK without losing the BOARD, and
// the board is the only thing git's exit codes talk about:
//
//   - "sync-stash-stranded" (exit 3): the rebase succeeded, but re-applying the
//     autostash conflicted, so git kept those changes in the stash and exited 0.
//     The dirty files — a half-written body, most likely — are simply not in the
//     working tree any more. Details carry pending_stash; nothing is pushed.
//   - "body-conflict-marker" (exit 2): a body about to be auto-committed still
//     carries conflict markers (usually the wreckage of the case above). Refused
//     before the commit, because a published commit cannot be un-published.
//
// Both were silent before: the first is how prose gets stranded, the second is how
// the wreckage then gets published to every other machine. A third, "sync-unmerged"
// (exit 2), is the pre-flight for the state the first one leaves behind — an
// unmerged index with no operation in progress, where git's own error names neither
// the stash nor the fix.
func (a *App) Sync(ctx context.Context, opts SyncOpts) (p *SyncProgress, err error) {
	p = &SyncProgress{}
	// Collapse a cancellation artifact into one honest "sync-interrupted" (see
	// interruptError). The progress object is left intact, so it still reports how
	// far the sync got before the interrupt.
	defer func() { err = interruptError(err, ctx.Err()) }()

	r, err := gitrepo.Open(ctx, a.Dir)
	if err != nil {
		return p, err // non-git board = validation (exit 2), from the adapter
	}
	// A rebase in progress is usually a concurrent writer (the board's bot / a
	// second operator) momentarily holding `pull --rebase`; wait it out with a
	// bounded backoff so agents don't fail spuriously in that sub-second window.
	// sleep is the cancellable backoff used by both retry loops below: it waits
	// out a transient concurrent-writer condition but returns early if ctx is
	// cancelled, so a Ctrl-C during a backoff doesn't have to ride out the budget.
	sleep := func(d time.Duration) error { return a.ctxSleep(ctx, d) }

	if op, cleared := waitForRebaseToClear(func() (string, bool) { return r.MidOperation(ctx) }, sleep, defaultConcurrentWait); !cleared {
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
	// Unmerged paths with NO operation in progress is the aftermath of a stash git
	// could not re-apply: it merges what it can, leaves conflict markers in the
	// files and unmerged entries in the index, and walks away. Every later git that
	// touches the index then fails — but with git's own opaque wording ("notes.md:
	// unmerged (…)"), which names neither the stash nor the fix. Sync must not be
	// the tool that relays that: say what state this is, name the stash still
	// holding the other half, and refuse (exit 2 — the fix is an edit, not a re-run).
	if unmerged := r.ConflictedPaths(ctx); len(unmerged) > 0 {
		p.PendingStash = autostashEntries(ctx, r)
		msg := fmt.Sprintf("the working tree has unmerged paths (%s) — git will not rebase over them, so sync cannot run. "+
			"Resolve the conflict markers and `git add` them (or `git checkout -- <path>` to discard), then re-run furrow sync",
			strings.Join(unmerged, ", "))
		if len(p.PendingStash) > 0 {
			msg += fmt.Sprintf(". This is what a stash git could not re-apply leaves behind — the other half of those "+
				"changes is still in the stash: %s", stashSummary(p.PendingStash))
		}
		return p, &core.Error{
			Code:    core.CodeValidation,
			ID:      "sync-unmerged",
			Msg:     msg,
			Details: map[string]any{"paths": unmerged, "pending_stash": p.PendingStash},
		}
	}

	spec, err := r.RelPath(a.Dir)
	if err != nil {
		return p, err
	}

	changes, err := r.DirtyChanges(ctx, spec)
	if err != nil {
		return p, err
	}
	if len(changes) > 0 {
		commitPaths, committedBodies, pendingBodies := partitionSync(spec, changes, opts)
		p.PendingBodies = pendingBodies // reported even when there is nothing else to commit
		if len(commitPaths) > 0 {
			// Never publish a half-merged body (see guardBodyMarkers). This runs
			// BEFORE the commit, so a refused sync has changed nothing at all.
			if err := a.guardBodyMarkers(committedBodies); err != nil {
				return p, err
			}
			message := opts.Message
			if message == "" {
				message = DefaultSyncMessage
			}
			if err := r.Commit(ctx, message, commitPaths...); err != nil {
				return p, err
			}
			p.Committed = true
			p.CommittedBodies = committedBodies
		}
	}

	// stranded records that THIS sync's pull left the autostash in the stash rather
	// than back in the working tree (see strandedStash). It is reset per pull()
	// because sync can pull twice (the non-fast-forward retry).
	stranded := false

	// pullOnce is a single pull --rebase attempt. A conflict is resolved
	// definitively here (flag, abort, sync-conflict); a transient race bubbles up
	// as ErrTransientFetchRace for pull (below) to retry; success sets Pulled.
	// Either way the stash is probed afterwards — git's autostash re-apply can fail
	// silently on BOTH paths, and on the success path it is the only tell there is.
	pullOnce := func() error {
		before := autostashCommits(ctx, r)
		if err := r.PullRebase(ctx); err != nil {
			if r.RebaseInProgress(ctx) {
				// Flag the conflict BEFORE attempting the abort: even if the
				// abort itself fails (the one state the contract promises never
				// to leave behind), the progress object and the error must both
				// say "conflict" and carry the paths.
				p.Conflict = true
				paths := r.ConflictedPaths(ctx)
				aerr := r.AbortRebase(ctx)
				// The abort is what re-applies the autostash on this path, so probe
				// after it — and only after it (a failed abort never got that far).
				details := map[string]any{"paths": paths}
				if aerr == nil {
					p.PendingStash, stranded = strandedStash(ctx, r, before)
					if len(p.PendingStash) > 0 {
						details["pending_stash"] = p.PendingStash
					}
				}
				if aerr != nil {
					return &core.Error{
						Code: core.CodeInternal,
						ID:   "sync-conflict",
						Msg: fmt.Sprintf("pull --rebase hit conflicts AND the automatic abort failed (%v) — "+
							"run 'git rebase --abort' in %s by hand, then re-run furrow sync", aerr, r.Toplevel()),
						Details: details,
					}
				}
				// Only claim the board was restored when it WAS. With the autostash
				// stranded, the most dangerous moment is the one where the old wording
				// was the most reassuring — the working tree is missing the very edits
				// the operator was in the middle of writing.
				msg := "pull --rebase hit conflicts; the rebase was aborted and the board restored " +
					"(your local sync commit is intact). Resolve the paths by hand (pull, fix, commit), then re-run furrow sync"
				if stranded {
					msg = fmt.Sprintf("pull --rebase hit conflicts; the rebase was aborted (your local sync commit is "+
						"intact), but git could NOT put your stashed working-tree changes back — they are in the stash, "+
						"NOT in your working tree: %s. Recover them with 'git -C %s stash pop', resolve the conflicted "+
						"paths by hand (pull, fix, commit), then re-run furrow sync",
						stashSummary(p.PendingStash), r.Toplevel())
				}
				return &core.Error{
					Code:    core.CodeInternal,
					ID:      "sync-conflict",
					Msg:     msg,
					Details: details,
				}
			}
			return err
		}
		// The rebase succeeded — and this is the path where a failed autostash
		// re-apply is INVISIBLE (git exits 0 and only warns on stderr), so the probe
		// here is not belt-and-braces: it is the only detector.
		p.PendingStash, stranded = strandedStash(ctx, r, before)
		p.Pulled = true
		return nil
	}
	// pull rides out a concurrent writer's fetch/ref-lock race (transient in a
	// shared checkout), reclassifying a persistent one as retryable sync-busy. A
	// pull that rebased cleanly but stranded the autostash stops the sync here:
	// pushing on would leave the operator's edits in a stash they were never told
	// about, and the working tree may hold the failed apply's conflict markers —
	// running more git in it is how the markers get committed.
	pull := func() error {
		stranded = false
		if err := pullWithRetry(pullOnce, sleep, defaultConcurrentWait, r.Toplevel()); err != nil {
			return err
		}
		if stranded {
			return &core.Error{
				Code: core.CodeInternal,
				ID:   "sync-stash-stranded",
				Msg: fmt.Sprintf("the pull rebased cleanly, but git could NOT put your stashed working-tree "+
					"changes back — they are in the stash, NOT in your working tree: %s. Recover them with "+
					"'git -C %s stash pop' (resolve any conflict it reports, then re-run furrow sync). The board's "+
					"own commit is safe; nothing was pushed",
					stashSummary(p.PendingStash), r.Toplevel()),
				Details: map[string]any{"pending_stash": p.PendingStash},
			}
		}
		return nil
	}

	if err := pull(); err != nil {
		return p, err
	}

	if err := r.Push(ctx); err != nil {
		if !errors.Is(err, gitrepo.ErrNonFastForward) {
			return p, err
		}
		// The remote moved between our pull and push: pull once more and retry.
		if err := pull(); err != nil {
			return p, err
		}
		if err := r.Push(ctx); err != nil {
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
