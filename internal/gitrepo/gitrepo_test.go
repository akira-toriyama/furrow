package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// gitOrSkip mirrors fsstore/conflict_test.go's convention: these are real-git
// tests, skipped where git is absent.
func gitOrSkip(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	return git
}

// runGitT runs one git command in dir, failing the test on error.
func runGitT(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initRepo creates a git repo with committable identity and one base commit.
func initRepo(t *testing.T, git string) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, git, dir, "init", "-q", "-b", "main")
	runGitT(t, git, dir, "config", "user.name", "t")
	runGitT(t, git, dir, "config", "user.email", "t@e")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-q", "-m", "base")
	return dir
}

func TestOpenOutsideGitIsValidation(t *testing.T) {
	gitOrSkip(t)
	_, err := Open(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Open outside a git repo must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d (validation)", got, core.CodeValidation)
	}
}

func TestOpenResolvesToplevelFromSubdir(t *testing.T) {
	git := gitOrSkip(t)
	dir := initRepo(t, git)
	sub := filepath.Join(dir, ".furrow")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Open(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	// macOS tempdirs live under /var -> /private/var; compare resolved paths.
	wantTop, _ := filepath.EvalSymlinks(dir)
	gotTop, _ := filepath.EvalSymlinks(r.Toplevel())
	if gotTop != wantTop {
		t.Errorf("Toplevel = %q, want %q", gotTop, wantTop)
	}
	rel, err := r.RelPath(sub)
	if err != nil {
		t.Fatal(err)
	}
	if rel != ".furrow" {
		t.Errorf("RelPath = %q, want .furrow", rel)
	}
}

// The auto-commit is pathspec-limited: a dirty file OUTSIDE .furrow must
// survive uncommitted — sweeping a user's notes into a sync commit is the
// exact failure the pathspec exists to prevent.
func TestCommitIsPathspecLimited(t *testing.T) {
	git := gitOrSkip(t)
	dir := initRepo(t, git)
	fdir := filepath.Join(dir, ".furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "meta.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Open(context.Background(), fdir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.HasChanges(context.Background(), ".furrow")
	if err != nil || !changed {
		t.Fatalf("HasChanges = %v, %v; want true, nil", changed, err)
	}
	if err := r.Commit(context.Background(), "test: sync", ".furrow"); err != nil {
		t.Fatal(err)
	}

	status := runGitT(t, git, dir, "status", "--porcelain")
	if !strings.Contains(status, "notes.md") {
		t.Errorf("notes.md must stay uncommitted, status:\n%s", status)
	}
	if strings.Contains(status, ".furrow") {
		t.Errorf(".furrow must be committed, status:\n%s", status)
	}
	if changed, _ := r.HasChanges(context.Background(), ".furrow"); changed {
		t.Error("HasChanges must be false after the commit")
	}
}

// DirtyChanges enumerates the dirty paths under a pathspec, tagging untracked
// files, and a variadic Commit of only the shard/meta paths leaves a modified
// body dirty — the two primitives app.Sync's class-split is built on.
func TestDirtyChangesTagsUntrackedAndScopesCommit(t *testing.T) {
	git := gitOrSkip(t)
	dir := initRepo(t, git)
	fdir := filepath.Join(dir, ".furrow")
	bdir := filepath.Join(fdir, "bodies")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(bdir, "t-1.md")
	if err := os.WriteFile(bodyPath, []byte("# one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(context.Background(), fdir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(context.Background(), "seed", ".furrow"); err != nil { // body now tracked + committed
		t.Fatal(err)
	}
	// A modification to the tracked body, plus a brand-new untracked meta.json.
	if err := os.WriteFile(bodyPath, []byte("# one\n\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "meta.json"), []byte("{\"schema_version\":3}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes, err := r.DirtyChanges(context.Background(), ".furrow")
	if err != nil {
		t.Fatal(err)
	}
	untracked := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range changes {
		seen[c.Path] = true
		untracked[c.Path] = c.Untracked
	}
	if !seen[".furrow/bodies/t-1.md"] || untracked[".furrow/bodies/t-1.md"] {
		t.Errorf("modified body: seen=%v untracked=%v; want seen, tracked", seen[".furrow/bodies/t-1.md"], untracked[".furrow/bodies/t-1.md"])
	}
	if !seen[".furrow/meta.json"] || !untracked[".furrow/meta.json"] {
		t.Errorf("new meta.json: seen=%v untracked=%v; want seen, untracked", seen[".furrow/meta.json"], untracked[".furrow/meta.json"])
	}

	// Committing only the meta path must leave the modified body dirty.
	if err := r.Commit(context.Background(), "meta only", ".furrow/meta.json"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(runGitT(t, git, dir, "status", "--porcelain", "--", ".furrow/bodies/t-1.md")) == "" {
		t.Error("modified body must remain uncommitted after a meta-only commit")
	}
}

// Push against a remote that moved is classified ErrNonFastForward — the one
// failure Sync retries.
func TestPushClassifiesNonFastForward(t *testing.T) {
	git := gitOrSkip(t)
	origin := t.TempDir()
	runGitT(t, git, origin, "init", "-q", "--bare", "-b", "main")

	seed := initRepo(t, git)
	runGitT(t, git, seed, "remote", "add", "origin", origin)
	runGitT(t, git, seed, "push", "-q", "-u", "origin", "main")

	cloneDir := filepath.Join(t.TempDir(), "b")
	runGitT(t, git, filepath.Dir(cloneDir), "clone", "-q", origin, cloneDir)
	runGitT(t, git, cloneDir, "config", "user.name", "t")
	runGitT(t, git, cloneDir, "config", "user.email", "t@e")

	// Remote moves ahead (seed pushes a new commit)…
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, seed, "add", "-A")
	runGitT(t, git, seed, "commit", "-q", "-m", "ahead")
	runGitT(t, git, seed, "push", "-q")

	// …while the clone commits its own and pushes without pulling.
	if err := os.WriteFile(filepath.Join(cloneDir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneDir, "add", "-A")
	runGitT(t, git, cloneDir, "commit", "-q", "-m", "behind")

	r, err := Open(context.Background(), cloneDir)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Push(context.Background())
	if err == nil {
		t.Fatal("push from a behind clone must fail")
	}
	if !errors.Is(err, ErrNonFastForward) {
		t.Errorf("want ErrNonFastForward, got: %v", err)
	}
}

func TestMidOperationDetectsMerge(t *testing.T) {
	git := gitOrSkip(t)
	dir := initRepo(t, git)

	// Build two diverging branches editing the same file, then start a merge
	// that conflicts — MERGE_HEAD exists while it is unresolved.
	runGitT(t, git, dir, "checkout", "-q", "-b", "x")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("from x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, dir, "commit", "-aqm", "x")
	runGitT(t, git, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, dir, "commit", "-aqm", "y")
	cmd := exec.Command(git, "merge", "x")
	cmd.Dir = dir
	_ = cmd.Run() // expected to fail with a conflict

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	op, busy := r.MidOperation(context.Background())
	if !busy || op != "merge" {
		t.Errorf("MidOperation = %q,%v; want merge,true", op, busy)
	}
}

// isTransientRace must fire on the concurrent-writer signatures a shared
// checkout produces (a co-writer's fetch clobbering FETCH_HEAD or contending a
// ref/index lock) and NOT on a genuine conflict, a non-fast-forward, or an
// ordinary failure — misclassifying either of the latter as transient would
// spin the retry loop on an error that never clears.
func TestIsTransientRace(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"multiple branches (FETCH_HEAD clobbered)", "fatal: Cannot rebase onto multiple branches.", true},
		{"cannot lock ref during fetch", "error: cannot lock ref 'refs/remotes/origin/main': is at 0000 but expected 1111", true},
		{"unable to update local ref", "error: unable to update local ref refs/remotes/origin/main", true},
		{"index.lock held by a co-writer", "fatal: Unable to create '/b/.git/index.lock': File exists.", true},
		{"another git process running", "fatal: Another git process seems to be running this repository", true},
		{"a real rebase conflict is NOT transient", "CONFLICT (content): Merge conflict in .furrow/tasks/t-k3m9p.json", false},
		{"a non-fast-forward push is NOT transient", "! [rejected]        main -> main (non-fast-forward)", false},
		{"an ordinary failure is NOT transient", "fatal: not a git repository", false},
		{"empty stderr", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientRace(tc.stderr); got != tc.want {
				t.Errorf("isTransientRace(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// AheadBehind reads only local knowledge (no fetch), so the counts move when
// commits land locally or a fetch updates the tracking ref — exactly what
// `furrow doctor` reports.
func TestAheadBehind(t *testing.T) {
	git := gitOrSkip(t)
	ctx := context.Background()

	t.Run("no upstream is a state, not an error", func(t *testing.T) {
		dir := initRepo(t, git)
		r, err := Open(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		ahead, behind, hasUpstream, err := r.AheadBehind(ctx)
		if err != nil {
			t.Fatalf("AheadBehind on an upstream-less repo must not error: %v", err)
		}
		if hasUpstream || ahead != 0 || behind != 0 {
			t.Errorf("got ahead=%d behind=%d hasUpstream=%t, want 0/0/false", ahead, behind, hasUpstream)
		}
	})

	t.Run("counts local commits as ahead and fetched ones as behind", func(t *testing.T) {
		origin := t.TempDir()
		runGitT(t, git, origin, "init", "-q", "--bare", "-b", "main")
		seed := initRepo(t, git)
		runGitT(t, git, seed, "remote", "add", "origin", origin)
		runGitT(t, git, seed, "push", "-q", "-u", "origin", "main")

		cloneDir := filepath.Join(t.TempDir(), "b")
		runGitT(t, git, filepath.Dir(cloneDir), "clone", "-q", origin, cloneDir)
		runGitT(t, git, cloneDir, "config", "user.name", "t")
		runGitT(t, git, cloneDir, "config", "user.email", "t@e")

		r, err := Open(ctx, cloneDir)
		if err != nil {
			t.Fatal(err)
		}
		ahead, behind, hasUpstream, err := r.AheadBehind(ctx)
		if err != nil || !hasUpstream || ahead != 0 || behind != 0 {
			t.Fatalf("fresh clone: got ahead=%d behind=%d hasUpstream=%t err=%v, want 0/0/true/nil", ahead, behind, hasUpstream, err)
		}

		// A local commit -> ahead 1.
		if err := os.WriteFile(filepath.Join(cloneDir, "b.txt"), []byte("b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitT(t, git, cloneDir, "add", "-A")
		runGitT(t, git, cloneDir, "commit", "-q", "-m", "local")

		// The remote moves too (seed pushes), and the clone FETCHES (no rebase) —
		// so the tracking ref knows, the way a real stale board does.
		if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGitT(t, git, seed, "add", "-A")
		runGitT(t, git, seed, "commit", "-q", "-m", "remote")
		runGitT(t, git, seed, "push", "-q")
		runGitT(t, git, cloneDir, "fetch", "-q")

		ahead, behind, hasUpstream, err = r.AheadBehind(ctx)
		if err != nil || !hasUpstream {
			t.Fatalf("AheadBehind: hasUpstream=%t err=%v, want true/nil", hasUpstream, err)
		}
		if ahead != 1 || behind != 1 {
			t.Errorf("got ahead=%d behind=%d, want 1/1", ahead, behind)
		}
	})
}
