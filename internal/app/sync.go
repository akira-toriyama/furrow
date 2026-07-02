package app

import (
	"errors"
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// DefaultSyncMessage is the auto-commit message `furrow sync` uses when
// --message is not given: gitmoji + Conventional, and chore = no version bump,
// which is exactly right for board data.
const DefaultSyncMessage = ":card_file_box: chore(board): sync via furrow"

// SyncProgress is the machine-readable record of how far a sync got. It is
// emitted on stdout on success AND on failure, so an agent can tell "the
// auto-commit happened but the push didn't" instead of guessing from an error
// string.
type SyncProgress struct {
	Committed bool `json:"committed"` // a board auto-commit was created
	Pulled    bool `json:"pulled"`    // pull --rebase completed
	Pushed    bool `json:"pushed"`    // push completed
	Conflict  bool `json:"conflict"`  // pull hit conflicts (rebase was aborted)
}

// Sync is the multi-machine ritual as one command: commit the board (pathspec
// -limited to .furrow/ so unrelated dirty files are never swept in), pull
// --rebase (autostash), push (retrying pull→push once on non-fast-forward).
// It is a thin git wrapper by design — no daemon, no sync server (see
// docs/non-goals.md).
//
// Failure contract: a rebase conflict aborts the rebase automatically (the
// board is never left with conflict markers — those would make every later
// furrow command fail in UnmarshalTask; the local sync commit survives) and
// returns a CodeInternal error with ID "sync-conflict" whose Details carry the
// conflicted paths. The returned SyncProgress is meaningful even when err is
// non-nil.
func (a *App) Sync(message string) (*SyncProgress, error) {
	p := &SyncProgress{}

	r, err := gitrepo.Open(a.Dir)
	if err != nil {
		return p, err // non-git board = validation (exit 2), from the adapter
	}
	if op, busy := r.MidOperation(); busy {
		return p, core.Validationf("sync",
			"a git %s is in progress in %s — finish or abort it, then re-run furrow sync", op, r.Toplevel())
	}
	spec, err := r.RelPath(a.Dir)
	if err != nil {
		return p, err
	}

	changed, err := r.HasChanges(spec)
	if err != nil {
		return p, err
	}
	if changed {
		if message == "" {
			message = DefaultSyncMessage
		}
		if err := r.Commit(message, spec); err != nil {
			return p, err
		}
		p.Committed = true
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
