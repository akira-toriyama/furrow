// Package gitrepo is the git subprocess adapter behind `furrow sync`: command
// assembly and error classification for the handful of git operations the sync
// flow needs, and nothing else. It is deliberately a THIN wrapper — no daemon,
// no state, no porcelain parsing beyond what the failure contract requires
// (see docs/non-goals.md).
//
// Layering: this package shells out to git (its own kind of side effect), so it
// lives beside the store adapters and is driven only through internal/app —
// core never sees it. fsstore remains the only package that touches the store's
// files; gitrepo only ever asks git to move whole commits around.
package gitrepo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ErrNonFastForward marks a push rejected because the remote moved ahead —
// the one push failure `furrow sync` retries (pull --rebase, push again).
var ErrNonFastForward = errors.New("push rejected: non-fast-forward")

// Repo drives git inside one working tree (the repo enclosing a .furrow board).
type Repo struct {
	git string // resolved git binary
	top string // absolute path of the work-tree toplevel
}

// Open locates the git repository enclosing dir (typically the .furrow
// directory itself). A dir outside any git work tree is a validation error
// (exit 2): sync is meaningless there and the fix — `git init` or moving the
// board — is the caller's.
func Open(dir string) (*Repo, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, core.Internalf("sync", "git not found on PATH: %v", err)
	}
	out, stderr, err := runGit(git, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, core.Validationf("sync", "%s is not inside a git repository (sync is a git wrapper): %s", dir, firstLine(stderr))
	}
	return &Repo{git: git, top: strings.TrimSpace(out)}, nil
}

// Toplevel returns the work-tree root the repo was resolved to.
func (r *Repo) Toplevel() string { return r.top }

// RelPath returns p relative to the work-tree toplevel — the pathspec form the
// sync flow uses so `git add/commit -- <pathspec>` never sweeps up files
// outside the board. Both sides are symlink-resolved first: git reports the
// resolved toplevel (/private/var/... on macOS) while callers may hold the
// unresolved spelling (/var/...), and Rel on the mixed pair fabricates a
// bogus ..-path.
func (r *Repo) RelPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", core.Internalf("sync", "abs %s: %v", p, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	top := r.top
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", core.Internalf("sync", "%s is not under the git toplevel %s", p, r.top)
	}
	return filepath.ToSlash(rel), nil
}

// MidOperation reports a rebase or merge already in progress — sync must
// refuse to start on top of one (pre-flight), never try to be clever.
func (r *Repo) MidOperation() (string, bool) {
	if r.RebaseInProgress() {
		return "rebase", true
	}
	if p, _, err := runGit(r.git, r.top, "rev-parse", "--git-path", "MERGE_HEAD"); err == nil {
		if _, statErr := os.Stat(r.absGitPath(p)); statErr == nil {
			return "merge", true
		}
	}
	return "", false
}

// RebaseInProgress reports whether a rebase (merge- or apply-backed) is midway.
func (r *Repo) RebaseInProgress() bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		p, _, err := runGit(r.git, r.top, "rev-parse", "--git-path", dir)
		if err != nil {
			continue
		}
		if _, statErr := os.Stat(r.absGitPath(p)); statErr == nil {
			return true
		}
	}
	return false
}

// HasChanges reports whether the pathspec has anything to commit (working tree
// or index) — the guard that keeps a no-op sync from creating empty commits.
func (r *Repo) HasChanges(pathspec string) (bool, error) {
	out, stderr, err := runGit(r.git, r.top, "status", "--porcelain", "--", pathspec)
	if err != nil {
		return false, core.Internalf("sync", "git status: %s", firstLine(stderr))
	}
	return strings.TrimSpace(out) != "", nil
}

// Change is one dirty path under a pathspec (from git status --porcelain): its
// slash-form path relative to the work-tree toplevel, and whether git sees it as
// untracked (a brand-new file, "??"). It is what lets app.Sync tell a
// machine-written shard from a hand-edited body and scope the auto-commit.
type Change struct {
	Path      string
	Untracked bool
}

// DirtyChanges enumerates the working-tree changes under pathspec — the listing
// twin of HasChanges. Each entry is tagged untracked-or-not and the slice is
// sorted by path for deterministic output. Porcelain parsing stays here, in the
// adapter, so app never sees git's wire format (layer rule).
func (r *Repo) DirtyChanges(pathspec string) ([]Change, error) {
	// core.quotepath=false keeps non-ASCII paths literal (unquoted) so the parse
	// below is a plain byte-slice; furrow's own ids are ASCII, but a repo may hold
	// other files under the pathspec.
	out, stderr, err := runGit(r.git, r.top, "-c", "core.quotepath=false", "status", "--porcelain", "--", pathspec)
	if err != nil {
		return nil, core.Internalf("sync", "git status: %s", firstLine(stderr))
	}
	changes := []Change{} // [] not null, matching the store's slice style
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 4 { // each entry is "XY path"
			continue
		}
		path := l[3:]
		// A rename/copy is reported "orig -> new"; the live path is the target.
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+len(" -> "):]
		}
		changes = append(changes, Change{Path: filepath.ToSlash(path), Untracked: l[0] == '?' && l[1] == '?'})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// Commit stages and commits ONLY the given pathspecs (`git add -- <p>...` then
// `git commit -m <msg> -- <p>...`), so other dirty files in the board repo —
// notes, drafts, or a co-located operator's uncommitted body — are never swept
// into a sync commit. app.Sync passes an explicit, class-filtered path set
// (machine-written shards always; hand-edited bodies only when new or opted in).
// An empty pathspec set is a no-op (no empty commit).
func (r *Repo) Commit(message string, pathspecs ...string) error {
	if len(pathspecs) == 0 {
		return nil
	}
	if _, stderr, err := runGit(r.git, r.top, append([]string{"add", "--"}, pathspecs...)...); err != nil {
		return core.Internalf("sync", "git add: %s", firstLine(stderr))
	}
	if _, stderr, err := runGit(r.git, r.top, append([]string{"commit", "-q", "-m", message, "--"}, pathspecs...)...); err != nil {
		return core.Internalf("sync", "git commit: %s", firstLine(stderr))
	}
	return nil
}

// PullRebase runs `git -c rebase.autoStash=true pull --rebase` — autostash so
// dirty files OUTSIDE the board (already excluded from the sync commit) don't
// stop the pull. On conflict git leaves the rebase in progress; the caller
// (app.Sync) detects that, collects the paths, and aborts.
func (r *Repo) PullRebase() error {
	if _, stderr, err := runGit(r.git, r.top, "-c", "rebase.autoStash=true", "pull", "--rebase", "-q"); err != nil {
		return core.Internalf("sync", "git pull --rebase: %s", firstLine(stderr))
	}
	return nil
}

// ConflictedPaths lists the paths currently in conflict (diff filter U),
// sorted — the machine-actionable payload of a sync-conflict error.
func (r *Repo) ConflictedPaths() []string {
	out, _, err := runGit(r.git, r.top, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	paths := []string{} // [] not null in the envelope, matching the store's slice style
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	sort.Strings(paths)
	return paths
}

// AbortRebase restores the pre-rebase state (`git rebase --abort`). The local
// sync commit survives; the working tree is never left with conflict markers —
// a half-rebased board would make every later furrow command die in
// UnmarshalTask.
func (r *Repo) AbortRebase() error {
	if _, stderr, err := runGit(r.git, r.top, "rebase", "--abort"); err != nil {
		return core.Internalf("sync", "git rebase --abort: %s", firstLine(stderr))
	}
	return nil
}

// Push runs `git push`. A rejection because the remote moved ahead is returned
// as ErrNonFastForward (wrapped) so the caller can retry pull→push exactly
// once; everything else is an internal error carrying git's first stderr line.
func (r *Repo) Push() error {
	_, stderr, err := runGit(r.git, r.top, "push")
	if err == nil {
		return nil
	}
	if isNonFastForward(stderr) {
		return fmt.Errorf("%w: %s", ErrNonFastForward, firstLine(stderr))
	}
	return core.Internalf("sync", "git push: %s", errorLine(stderr))
}

// isNonFastForward classifies a push rejection from git's stderr. git phrases
// it a few ways depending on version and cause ("non-fast-forward",
// "fetch first", "[rejected]"); any of them means "remote moved — pull and
// retry", which is all sync needs to know.
func isNonFastForward(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "non-fast-forward") ||
		strings.Contains(s, "fetch first") ||
		strings.Contains(s, "[rejected]")
}

// absGitPath resolves `git rev-parse --git-path` output (relative to the
// toplevel for a normal repo) to an absolute path.
func (r *Repo) absGitPath(out string) string {
	p := strings.TrimSpace(out)
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(r.top, p)
}

// runGit executes one git command with dir as cwd, returning stdout and stderr
// separately (classification reads stderr; data reads stdout).
func runGit(git, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// firstLine trims a git stderr blob to its first non-empty line — enough for
// an error message without pasting a whole hint block.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "(no output)"
}

// errorLine picks the most diagnostic line of a git stderr blob: push failures
// open with a bland "To <url>" line, and the actual reason lives in the
// "error:"/"fatal:"/"! [remote rejected]" line below it. Falls back to
// firstLine when no such line exists.
func errorLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "error:") || strings.HasPrefix(t, "fatal:") || strings.HasPrefix(t, "!") {
			return t
		}
	}
	return firstLine(s)
}
