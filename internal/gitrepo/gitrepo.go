// Package gitrepo is the git subprocess adapter behind furrow's git-touching
// flows — `furrow sync`, `furrow doctor`'s freshness probe, and post-mutation
// autocommit (see internal/app/autocommit.go): command assembly and error
// classification for the handful of git operations they need, and nothing else.
// It is deliberately a THIN wrapper — no daemon, no state, no porcelain parsing
// beyond what the failure contract requires (see docs/non-goals.md).
//
// Layering: this package shells out to git (its own kind of side effect), so it
// lives beside the store adapters and is driven only through internal/app —
// core never sees it. fsstore remains the only package that touches the store's
// files; gitrepo only ever asks git to move whole commits around.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ErrNonFastForward marks a push rejected because the remote moved ahead —
// the one push failure `furrow sync` retries (pull --rebase, push again).
var ErrNonFastForward = errors.New("push rejected: non-fast-forward")

// ErrTransientFetchRace marks a PullRebase failure caused by a concurrent
// writer in a SHARED checkout — a co-located operator's/bot's `git fetch`
// clobbering FETCH_HEAD or contending a ref/index lock while ours runs. It is
// transient (the tree self-resolves within a second), so app.Sync rides it out
// with bounded retries rather than failing; the sentinel is what tells "retry
// this" apart from a real conflict or an ordinary failure.
var ErrTransientFetchRace = errors.New("sync pull (fetch+rebase): transient concurrent-fetch race")

// gitFailed classifies a failed git invocation (KindGitFailed, exit 3). The
// message names the command and relays git's own words; there is no finer
// class because the remedy is always "read what git said" — the failures a
// caller can act on mechanically (conflict, push race, stale lock) are
// re-classified by app.Sync into their own kinds before they reach a consumer.
func gitFailed(format string, a ...any) *core.Error {
	return &core.Error{Code: core.CodeInternal, Kind: core.KindGitFailed, Msg: fmt.Sprintf(format, a...)}
}

// Repo drives git inside one working tree (the repo enclosing a .furrow board).
type Repo struct {
	git string // resolved git binary
	top string // absolute path of the work-tree toplevel
}

// Open locates the git repository enclosing dir (typically the .furrow
// directory itself). A dir outside any git work tree is a validation error
// (exit 2): sync is meaningless there and the fix — `git init` or moving the
// board — is the caller's.
func Open(ctx context.Context, dir string) (*Repo, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, &core.Error{
			Code: core.CodeInternal,
			Kind: core.KindGitMissing,
			Msg:  fmt.Sprintf("git not found on PATH: %v", err),
		}
	}
	out, stderr, err := runGit(ctx, git, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, &core.Error{
			Code: core.CodeValidation,
			Kind: core.KindNoGitRepo,
			Msg:  fmt.Sprintf("%s is not inside a git repository (sync is a git wrapper): %s", dir, firstLine(stderr)),
		}
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
		return "", core.Internalf("", "abs %s: %v", p, err)
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
		return "", core.Internalf("", "%s is not under the git toplevel %s", p, r.top)
	}
	return filepath.ToSlash(rel), nil
}

// MidOperation reports a git operation already in progress — a rebase, merge,
// cherry-pick, revert, or bisect. sync must refuse to start on top of one
// (pre-flight), never try to be clever: its auto-commit would `git add` the
// board into the operator's half-finished operation, and the eventual
// `--continue` would absorb the shards into the foreign commit (measured with a
// conflicted cherry-pick before cherry-pick/revert were probed here).
func (r *Repo) MidOperation(ctx context.Context) (string, bool) {
	if r.RebaseInProgress(ctx) {
		return "rebase", true
	}
	for _, probe := range []struct{ marker, op string }{
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	} {
		if p, _, err := runGit(ctx, r.git, r.top, "rev-parse", "--git-path", probe.marker); err == nil {
			if _, statErr := os.Stat(r.absGitPath(p)); statErr == nil {
				return probe.op, true
			}
		}
	}
	return "", false
}

// RebaseInProgress reports whether a rebase (merge- or apply-backed) is midway.
func (r *Repo) RebaseInProgress(ctx context.Context) bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		p, _, err := runGit(ctx, r.git, r.top, "rev-parse", "--git-path", dir)
		if err != nil {
			continue
		}
		if _, statErr := os.Stat(r.absGitPath(p)); statErr == nil {
			return true
		}
	}
	return false
}

// Change is one dirty path under a pathspec (from git status --porcelain): its
// slash-form path relative to the work-tree toplevel, and whether git sees it as
// untracked (a brand-new file, "??"). It is what lets app.Sync tell a
// machine-written shard from a hand-edited body and scope the auto-commit.
type Change struct {
	Path      string
	Untracked bool
}

// DirtyChanges enumerates the working-tree changes under pathspec — the guard
// that keeps a no-op sync from creating an empty commit, and the listing that
// tells app WHAT changed. Each entry is tagged untracked-or-not and the slice is
// sorted by path for deterministic output. Porcelain parsing stays here, in the
// adapter, so app never sees git's wire format (layer rule).
func (r *Repo) DirtyChanges(ctx context.Context, pathspec string) ([]Change, error) {
	// core.quotepath=false keeps non-ASCII paths literal (unquoted) so the parse
	// below is a plain byte-slice; furrow's own ids are ASCII, but a repo may hold
	// other files under the pathspec.
	//
	// -uall is load-bearing, not tidiness: git's DEFAULT collapses a wholly-untracked
	// directory to one "?? .furrow/bodies/" entry, so on a board whose bodies/ has no
	// tracked file yet (a fresh one — git cannot track an empty dir), every body is
	// hidden behind the directory. The caller classifies paths (body vs machine-written
	// shard) to decide what to commit and what to check, and a directory is neither: the
	// bodies would be committed while being counted as no body at all — invisible to
	// committed_bodies AND to the conflict-marker guard. Enumerate files, always.
	out, stderr, err := runGit(ctx, r.git, r.top, "-c", "core.quotepath=false", "status", "--porcelain", "-uall", "--", pathspec)
	if err != nil {
		return nil, gitFailed("git status: %s", firstLine(stderr))
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

// Head returns HEAD's full sha, or "" when it cannot be resolved (no commits
// yet, or a cancelled ctx). Best-effort by the same contract as AddedLines: its
// only callers are display reads that must never fail a sync.
func (r *Repo) Head(ctx context.Context) string {
	out, _, err := runGit(ctx, r.git, r.top, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// FileChange is one path a commit range touched: git's one-letter status
// ('A'dded, 'M'odified, 'D'eleted) and the slash-form path relative to the
// work-tree toplevel. The parse stays here, in the adapter, so app never sees
// diff wire format.
type FileChange struct {
	Status byte
	Path   string
}

// ChangedFiles returns the paths whose content differs between the two
// commits, restricted to pathspec — a TREE diff (two-dot), so it answers "what
// is different" even when from is no longer an ancestor of to (a rebase moved
// it aside). --no-renames keeps every change a plain A/M/D: furrow's shards
// are keyed by frozen ids, so a "rename" here could only be git's similarity
// heuristic pairing two unrelated shards. Best-effort display read: any
// failure returns nil.
func (r *Repo) ChangedFiles(ctx context.Context, from, to, pathspec string) []FileChange {
	out, _, err := runGit(ctx, r.git, r.top, "-c", "core.quotepath=false",
		"diff", "--name-status", "--no-renames", from+".."+to, "--", pathspec)
	if err != nil {
		return nil
	}
	var changes []FileChange
	for _, l := range strings.Split(out, "\n") {
		status, path, found := strings.Cut(l, "\t")
		if !found || status == "" {
			continue
		}
		changes = append(changes, FileChange{Status: status[0], Path: filepath.ToSlash(path)})
	}
	return changes
}

// FileAt returns the file's bytes as of rev (`git show rev:path`; path is
// relative to the work-tree toplevel), or nil when the rev or path does not
// resolve. Same best-effort contract as ChangedFiles.
func (r *Repo) FileAt(ctx context.Context, rev, path string) []byte {
	out, _, err := runGit(ctx, r.git, r.top, "show", rev+":"+path)
	if err != nil {
		return nil
	}
	return []byte(out)
}

// AddedLine is one line the unpushed commits add to a file: its slash-form path
// relative to the work-tree toplevel, and the line's text without the leading
// "+". The parse stays here, in the adapter, so app never sees diff wire format.
type AddedLine struct {
	Path string
	Text string
}

// AddedLines returns the lines that commits not yet on the upstream
// (@{upstream}..HEAD) add under pathspec — what a `git push` from here would
// publish. It is a best-effort DISPLAY read: any failure (no upstream
// configured, detached HEAD) returns nil rather than an error, because no
// caller should ever fail a sync over a summary line. --unified=0 keeps context
// lines out so every "+" line really was added.
func (r *Repo) AddedLines(ctx context.Context, pathspec string) []AddedLine {
	out, _, err := runGit(ctx, r.git, r.top, "-c", "core.quotepath=false",
		"diff", "--unified=0", "@{upstream}..HEAD", "--", pathspec)
	if err != nil {
		return nil
	}
	var lines []AddedLine
	path := ""
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ b/"):
			path = filepath.ToSlash(strings.TrimPrefix(l, "+++ b/"))
		case strings.HasPrefix(l, "+++"):
			path = "" // a deletion's "+++ /dev/null"
		case strings.HasPrefix(l, "+") && path != "":
			lines = append(lines, AddedLine{Path: path, Text: strings.TrimPrefix(l, "+")})
		}
	}
	return lines
}

// Commit stages and commits ONLY the given pathspecs (`git add -- <p>...` then
// `git commit -m <msg> -- <p>...`), so other dirty files in the board repo —
// notes, drafts, or a co-located operator's uncommitted body — are never swept
// into a sync commit. app.Sync passes an explicit, class-filtered path set
// (machine-written shards always; hand-edited bodies only when new or opted in).
// An empty pathspec set is a no-op (no empty commit).
func (r *Repo) Commit(ctx context.Context, message string, pathspecs ...string) error {
	if len(pathspecs) == 0 {
		return nil
	}
	if _, stderr, err := runGit(ctx, r.git, r.top, append([]string{"add", "--"}, pathspecs...)...); err != nil {
		return gitFailed("git add: %s", firstLine(stderr))
	}
	if _, stderr, err := runGit(ctx, r.git, r.top, append([]string{"commit", "-q", "-m", message, "--"}, pathspecs...)...); err != nil {
		return gitFailed("git commit: %s", firstLine(stderr))
	}
	return nil
}

// PullRebase brings the board up to date the concurrency-safe way: `git fetch`
// then `git rebase --autostash @{u}` — rebasing onto the UPSTREAM TRACKING REF,
// never FETCH_HEAD. `git pull --rebase` reads FETCH_HEAD, which a co-writer's
// concurrent fetch in a shared checkout can rewrite with multiple entries
// between our fetch and our rebase, yielding `fatal: Cannot rebase onto multiple
// branches`; rebasing onto @{u} sidesteps that class entirely. --autostash keeps
// dirty files OUTSIDE the board (already excluded from the sync commit) from
// stopping the rebase.
//
// Two failure shapes matter to the caller. A rebase CONFLICT leaves the rebase
// in progress; PullRebase returns an internal error and app.Sync detects the
// in-progress rebase, collects the paths, and aborts. A transient
// concurrent-access race (a co-writer's fetch clobbering a ref/index lock, or
// the residual multiple-branches window) leaves NO rebase in progress and is
// returned wrapped in ErrTransientFetchRace so app.Sync retries it.
//
// A THIRD shape hides inside SUCCESS, and it is why the caller must probe the
// stash afterwards (see app.Sync / StashEntries). git re-applies the autostash at
// the very end, onto the freshly-rebased tree — which now carries upstream's
// changes, so that apply can CONFLICT even though the rebase itself did not. When
// it does, git keeps the changes in the stash (`git stash store -m autostash`),
// warns on stderr, and STILL EXITS 0: the dirty files are simply not in the
// working tree any more. No exit code, no in-progress rebase — nothing here can
// see it, which is exactly how hand-edited prose gets silently stranded.
func (r *Repo) PullRebase(ctx context.Context) error {
	// fetch updates the remote-tracking ref; a co-writer's concurrent fetch can
	// transiently lose a ref/index-lock race here — retryable, not fatal.
	if _, stderr, err := runGit(ctx, r.git, r.top, "fetch", "-q"); err != nil {
		if isTransientRace(stderr) {
			return fmt.Errorf("%w: %s", ErrTransientFetchRace, firstLine(stderr))
		}
		return gitFailed("git fetch: %s", firstLine(stderr))
	}
	// rebase onto @{u} (the ref fetch just moved), autostashing anything outside
	// the sync commit. A conflict stops the rebase in progress (the caller's to
	// abort); a transient race here left no rebase to abort, so classify it.
	if _, stderr, err := runGit(ctx, r.git, r.top, "rebase", "--autostash", "-q", "@{u}"); err != nil {
		if !r.RebaseInProgress(ctx) && isTransientRace(stderr) {
			return fmt.Errorf("%w: %s", ErrTransientFetchRace, firstLine(stderr))
		}
		return gitFailed("git rebase: %s", firstLine(stderr))
	}
	return nil
}

// isTransientRace classifies a fetch/rebase stderr as a concurrent-writer race
// in a shared checkout — a condition that self-resolves, so sync retries rather
// than fails. Two families: a fetch losing a ref/index-lock contest against a
// co-writer's fetch ("cannot lock ref", "unable to update local ref",
// "index.lock", "another git process"), and the residual FETCH_HEAD-clobbered
// "cannot rebase onto multiple branches" (the split above makes this rare, but
// classify it defensively). A real conflict ("CONFLICT", "could not apply") and
// a plain failure are deliberately NOT matched — retrying those spins forever.
func isTransientRace(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "cannot rebase onto multiple branches") ||
		strings.Contains(s, "cannot lock ref") ||
		strings.Contains(s, "unable to update local ref") ||
		strings.Contains(s, "index.lock") ||
		strings.Contains(s, "another git process")
}

// ConflictedPaths lists the paths currently in conflict (diff filter U),
// sorted — the machine-actionable payload of a sync-conflict error.
func (r *Repo) ConflictedPaths(ctx context.Context) []string {
	out, _, err := runGit(ctx, r.git, r.top, "diff", "--name-only", "--diff-filter=U")
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

// AutostashSubject is the reflog subject git gives an autostash it could not put
// back: `git stash store -m autostash`. It is how an autostash entry is told apart
// from an operator's own `git stash` (whose subject is "WIP on <branch>: …"), and
// it is git's own literal, not a message we format — so matching it is stable
// across locales (git localizes prose, not this).
const AutostashSubject = "autostash"

// StashEntry is one entry of `git stash list`: its ref spelling at the time of the
// listing (stash@{N} — an index that SHIFTS as entries are pushed and dropped), the
// stash commit oid (the stable handle), and the reflog subject that says where it
// came from (AutostashSubject for one git stored on our behalf).
type StashEntry struct {
	Ref     string
	Commit  string
	Subject string
}

// StashEntries lists the repo's stash entries, newest first (git's own order). A
// repo with no stash yields none. Errors are swallowed to an empty list on
// purpose: this is a REPORTING probe on the failure path of a sync that has
// already decided its outcome — a repo whose stash cannot be listed must not turn
// a conflict report into a different error.
func (r *Repo) StashEntries(ctx context.Context) []StashEntry {
	// %gd = the stash@{N} selector, %H = the commit, %gs = the reflog subject.
	out, _, err := runGit(ctx, r.git, r.top, "stash", "list", "--format=%gd%x09%H%x09%gs")
	if err != nil {
		return nil
	}
	var entries []StashEntry
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		entries = append(entries, StashEntry{Ref: parts[0], Commit: parts[1], Subject: parts[2]})
	}
	return entries
}

// StashedPaths lists the files a stash commit carries, sorted — the payload that
// makes a stranded stash actionable ("which of my edits are in there?"). Same
// swallow-to-empty contract as StashEntries.
func (r *Repo) StashedPaths(ctx context.Context, commit string) []string {
	out, _, err := runGit(ctx, r.git, r.top, "stash", "show", "--name-only", commit)
	if err != nil {
		return nil
	}
	paths := []string{} // [] not null in the envelope, matching the store's slice style
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, filepath.ToSlash(l))
		}
	}
	sort.Strings(paths)
	return paths
}

// Commit describes HEAD: its full sha (%H — what app/board.go publishes as
// `commit`), its committer time, and its subject (the first line of the
// message). It answers "when did this board last change,
// and to what?" — on a board with no upstream, which is every standalone one,
// that is the ONLY collision signal there is, since ahead/behind has nothing to
// compare against.
type Commit struct {
	SHA     string
	When    time.Time
	Subject string
}

// LastCommit reads HEAD. hasCommit is false (with a nil error) on a repo with
// no commits yet — a freshly `git init`ed board is a state to report, not a
// failure, exactly as AheadBehind treats a missing upstream.
func (r *Repo) LastCommit(ctx context.Context) (c Commit, hasCommit bool, err error) {
	// %H %cI %s — full sha, committer date in strict ISO 8601, subject. Split on
	// the first two spaces so a subject containing spaces survives intact.
	out, stderr, err := runGit(ctx, r.git, r.top, "log", "-1", "--format=%H %cI %s")
	if err != nil {
		if isNoCommits(stderr) {
			return Commit{}, false, nil
		}
		return Commit{}, false, gitFailed("git log -1: %s", firstLine(stderr))
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), " ", 3)
	if len(parts) < 2 {
		return Commit{}, false, gitFailed("git log -1: unexpected output %q", strings.TrimSpace(out))
	}
	when, terr := time.Parse(time.RFC3339, parts[1])
	if terr != nil {
		return Commit{}, false, gitFailed("git log -1: unparsable date %q", parts[1])
	}
	c = Commit{SHA: parts[0], When: when}
	if len(parts) == 3 {
		c.Subject = parts[2]
	}
	return c, true, nil
}

// isNoCommits recognizes git's "this repo has no commits yet" complaint, which
// several git versions word differently.
func isNoCommits(stderr string) bool {
	l := strings.ToLower(stderr)
	return strings.Contains(l, "does not have any commits yet") ||
		strings.Contains(l, "bad default revision") ||
		strings.Contains(l, "unknown revision")
}

// AheadBehind reports how the checked-out branch relates to its upstream
// tracking ref — ahead = local commits not upstream, behind = upstream commits
// not local — from LOCAL knowledge only: it never fetches (`furrow doctor` is
// read-only and network-free), so the counts are as fresh as the last fetch.
// hasUpstream is false (with zero counts and a nil error) when there is no
// tracking ref to compare against — an un-tracked branch or a detached HEAD —
// because a board without an upstream (every standalone board — git, no
// remote — and any un-tracked branch) is a state to report, not a failure.
func (r *Repo) AheadBehind(ctx context.Context) (ahead, behind int, hasUpstream bool, err error) {
	// Left-right count over the symmetric difference: the left column is @{u}'s
	// own commits (behind), the right is HEAD's (ahead).
	out, stderr, err := runGit(ctx, r.git, r.top, "rev-list", "--left-right", "--count", "@{u}...HEAD")
	if err != nil {
		if isNoUpstream(stderr) {
			return 0, 0, false, nil
		}
		return 0, 0, false, gitFailed("git rev-list @{u}...HEAD: %s", firstLine(stderr))
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false, gitFailed("git rev-list --left-right --count: unexpected output %q", strings.TrimSpace(out))
	}
	if behind, err = strconv.Atoi(fields[0]); err == nil {
		ahead, err = strconv.Atoi(fields[1])
	}
	if err != nil {
		return 0, 0, false, gitFailed("git rev-list --left-right --count: unexpected output %q", strings.TrimSpace(out))
	}
	return ahead, behind, true, nil
}

// isNoUpstream classifies the rev-parse/rev-list stderr for "@{u} names
// nothing here": an un-tracked branch ("no upstream configured", or "no
// tracking information" from older gits) or a detached HEAD ("HEAD does not
// point to a branch"). Any other failure is a real error.
func isNoUpstream(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no upstream") ||
		strings.Contains(s, "no tracking information") ||
		strings.Contains(s, "does not point to a branch")
}

// AbortRebase restores the pre-rebase state (`git rebase --abort`). The local
// sync commit survives; the working tree is never left with conflict markers —
// a half-rebased board would make every later furrow command die in
// UnmarshalTask.
func (r *Repo) AbortRebase(ctx context.Context) error {
	// Abort is cleanup that MUST complete even mid-cancellation: if ctx is already
	// cancelled (a Ctrl-C landed just as a conflict surfaced), running it under ctx
	// would be killed immediately and leave the board half-rebased — the one state
	// the contract promises never to leave. Detach from cancellation (values kept)
	// so the abort always runs to completion.
	if _, stderr, err := runGit(context.WithoutCancel(ctx), r.git, r.top, "rebase", "--abort"); err != nil {
		return gitFailed("git rebase --abort: %s", firstLine(stderr))
	}
	return nil
}

// Push runs `git push`. A rejection because the remote moved ahead is returned
// as ErrNonFastForward (wrapped) so the caller can retry pull→push exactly
// once; everything else is an internal error carrying git's first stderr line.
func (r *Repo) Push(ctx context.Context) error {
	_, stderr, err := runGit(ctx, r.git, r.top, "push")
	if err == nil {
		return nil
	}
	if isNonFastForward(stderr) {
		return fmt.Errorf("%w: %s", ErrNonFastForward, firstLine(stderr))
	}
	return gitFailed("git push: %s", errorLine(stderr))
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

// GitPath resolves name to its absolute location inside this repo's git
// directory (`git rev-parse --git-path`) — worktree-aware, so per-checkout
// local state (e.g. app's touched-bodies journal) lands beside the checkout
// that owns it, invisible to `git status` and to sync.
func (r *Repo) GitPath(ctx context.Context, name string) (string, error) {
	out, stderr, err := runGit(ctx, r.git, r.top, "rev-parse", "--git-path", name)
	if err != nil {
		return "", gitFailed("git rev-parse --git-path: %s", firstLine(stderr))
	}
	return r.absGitPath(out), nil
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
// separately (classification reads stderr; data reads stdout). It is
// cancellable: exec.CommandContext kills the git child when ctx is done (a
// Ctrl-C/SIGTERM-cancelled sync), so a long fetch/push unwinds promptly instead
// of hanging.
func runGit(ctx context.Context, git, dir string, args ...string) (stdout, stderr string, err error) {
	// #nosec G204 -- git is resolved from PATH; args are furrow-built git
	// subcommands and refs/paths, never an unescaped user shell string.
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// gitEnv is the environment for a git subprocess with the locale forced to C.
// furrow classifies transient sync races by matching English substrings of
// git's stderr (see isTransientRace), so git must NOT emit localized messages
// under a non-English LANG/LC_ALL/LANGUAGE — otherwise a retryable race is
// misread as a permanent failure. Existing locale vars are STRIPPED rather than
// only appended-over, because getenv resolves a duplicate key to the first
// occurrence on some platforms, so a trailing "LC_ALL=C" would not reliably win.
// The git porcelain furrow parses (e.g. `diff --diff-filter=U`) is not
// localized, so forcing C is safe.
func gitEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "LC_ALL="),
			strings.HasPrefix(kv, "LANG="),
			strings.HasPrefix(kv, "LANGUAGE="),
			strings.HasPrefix(kv, "LC_MESSAGES="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_ALL=C")
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
