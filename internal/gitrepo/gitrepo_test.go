package gitrepo

import (
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
	_, err := Open(t.TempDir())
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
	r, err := Open(sub)
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

	r, err := Open(fdir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.HasChanges(".furrow")
	if err != nil || !changed {
		t.Fatalf("HasChanges = %v, %v; want true, nil", changed, err)
	}
	if err := r.Commit("test: sync", ".furrow"); err != nil {
		t.Fatal(err)
	}

	status := runGitT(t, git, dir, "status", "--porcelain")
	if !strings.Contains(status, "notes.md") {
		t.Errorf("notes.md must stay uncommitted, status:\n%s", status)
	}
	if strings.Contains(status, ".furrow") {
		t.Errorf(".furrow must be committed, status:\n%s", status)
	}
	if changed, _ := r.HasChanges(".furrow"); changed {
		t.Error("HasChanges must be false after the commit")
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

	r, err := Open(cloneDir)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Push()
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

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	op, busy := r.MidOperation()
	if !busy || op != "merge" {
		t.Errorf("MidOperation = %q,%v; want merge,true", op, busy)
	}
}
