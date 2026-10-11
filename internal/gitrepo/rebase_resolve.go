// The git calls app.Sync uses to FINISH a rebase that stopped on conflicts
// instead of aborting it: read a conflicted path's three index stages, stage
// a resolution, and continue (or skip an emptied commit). Same contract as the
// rest of the adapter — command assembly and classification only; the merge
// itself is core's, and the resolved bytes reach the work tree through fsstore.

package gitrepo

import (
	"context"
	"strconv"
)

// IndexStages returns a conflicted path's three index stages during a stopped
// rebase: base (stage 1, the common ancestor), ours (stage 2 — under a rebase
// that is the UPSTREAM side) and theirs (stage 3 — the local commit being
// replayed). A stage that does not exist (no ancestor for a both-added path; a
// side that deleted it) is nil. path is relative to the work-tree toplevel.
func (r *Repo) IndexStages(ctx context.Context, path string) (base, ours, theirs []byte) {
	stage := func(n int) []byte {
		out, _, err := runGit(ctx, r.git, r.top, "show", ":"+strconv.Itoa(n)+":"+path)
		if err != nil {
			return nil
		}
		return []byte(out)
	}
	return stage(1), stage(2), stage(3)
}

// Stage marks paths resolved (`git add -- <paths>`): the work-tree content
// becomes the index entry and the conflict stages are cleared. Paths are
// relative to the work-tree toplevel.
func (r *Repo) Stage(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	if _, stderr, err := runGit(ctx, r.git, r.top, append([]string{"add", "--"}, paths...)...); err != nil {
		return gitFailed("git add: %s", firstLine(stderr))
	}
	return nil
}

// IndexHasChanges reports whether the index differs from HEAD — during a
// stopped rebase, whether the commit being replayed still changes anything
// once its conflicts are resolved. false means `rebase --continue` would refuse
// ("No changes") and the commit is to be skipped instead. `diff --quiet` exits
// 1 on a difference and 0 on none; any other failure is read as "changes", so
// the continue that follows is what reports it.
func (r *Repo) IndexHasChanges(ctx context.Context) bool {
	_, _, err := runGit(ctx, r.git, r.top, "diff", "--cached", "--quiet")
	return err != nil
}

// gitEditorOff is the environment that makes a rebase step non-interactive:
// GIT_EDITOR outranks core.editor, so an operator's `code --wait` cannot be
// what a sync blocks on.
var gitEditorOff = []string{"GIT_EDITOR=true"}

// ContinueRebase resumes a stopped rebase after its conflicts were staged; the
// commit message is the replayed commit's own. commit.cleanup is pinned to
// strip for the same reason gitConfig pins diff flags: under an operator's
// `commit.cleanup=verbatim` the template git hands the (no-op) editor —
// "# Conflicts:", "# interactive rebase in progress" — would be committed as
// the message. A failure is returned as git-failed; the caller probes
// RebaseInProgress to tell "the next commit conflicted too" from "the rebase
// could not go on".
func (r *Repo) ContinueRebase(ctx context.Context) error {
	if _, stderr, err := runGitEnv(ctx, r.git, r.top, gitEditorOff, "-c", "commit.cleanup=strip", "rebase", "--continue"); err != nil {
		return gitFailed("git rebase --continue: %s", firstLine(stderr))
	}
	return nil
}

// SkipRebase drops the commit a stopped rebase is replaying and resumes — for
// a commit whose every change the upstream already carries once its conflicts
// are resolved (`--continue` refuses to record an empty commit).
func (r *Repo) SkipRebase(ctx context.Context) error {
	if _, stderr, err := runGitEnv(ctx, r.git, r.top, gitEditorOff, "-c", "commit.cleanup=strip", "rebase", "--skip"); err != nil {
		return gitFailed("git rebase --skip: %s", firstLine(stderr))
	}
	return nil
}
