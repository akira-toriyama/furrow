package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/gittest"
)

// A git hook exports GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE to its children,
// so a furrow run from another repo's hook used to resolve the board's
// toplevel to THAT repo and commit the board's shards into its history. The
// adapter names the directory it means and must not let the environment
// override it.
func TestGitEnvDropsTheHooksRepositoryVariables(t *testing.T) {
	git := gittest.GitOrSkip(t)
	other := initRepo(t, git) // the repo whose hook is running
	board := initRepo(t, git)
	fdir := filepath.Join(board, ".furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	r, err := Open(context.Background(), fdir)
	if err != nil {
		t.Fatal(err)
	}
	wantTop, _ := filepath.EvalSymlinks(board)
	if gotTop, _ := filepath.EvalSymlinks(r.Toplevel()); gotTop != wantTop {
		t.Fatalf("Toplevel under the hook's GIT_DIR = %q, want the board's own %q", gotTop, wantTop)
	}
	if err := os.WriteFile(filepath.Join(fdir, "meta.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(context.Background(), "board write", ".furrow"); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := r.LastCommit(context.Background()); err != nil || !ok || c.Subject != "board write" {
		t.Errorf("the board repo's HEAD = %+v (ok=%v err=%v); want the sync commit", c, ok, err)
	}
	o, err := Open(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if c, _, _ := o.LastCommit(context.Background()); c.Subject != "base" {
		t.Errorf("the hook's repo gained the board's commit: HEAD = %+v", c)
	}
	if changes, _ := o.DirtyChanges(context.Background(), "."); len(changes) != 0 {
		t.Errorf("the hook's repo was touched: %+v", changes)
	}
}

// `color.ui = always` colours even a piped diff; the escape codes in front of
// every `+++` and `+` left AddedLines with nothing, and the activation-switch
// disclosure went quiet — the noprefix defect (t-3t68) in another coat.
func TestAddedLinesIgnoresOperatorColorConfig(t *testing.T) {
	git := gittest.GitOrSkip(t)
	origin := t.TempDir()
	gittest.RunGit(t, git, origin, "init", "-q", "--bare", "-b", "main")
	seed := initRepo(t, git)
	gittest.RunGit(t, git, seed, "remote", "add", "origin", origin)
	gittest.RunGit(t, git, seed, "push", "-q", "-u", "origin", "main")
	gittest.RunGit(t, git, seed, "config", "color.ui", "always")

	if err := os.WriteFile(filepath.Join(seed, "notes.md"), []byte("2026-09-15 activated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, seed, "add", "-A")
	gittest.RunGit(t, git, seed, "commit", "-q", "-m", "unpushed")

	r, err := Open(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	got := r.AddedLines(context.Background(), ".")
	want := []AddedLine{{Path: "notes.md", Text: "2026-09-15 activated"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AddedLines under color.ui=always = %+v, want %+v", got, want)
	}
}

// A path with a space or a quote came back C-quoted from the line-oriented
// porcelain, matched no classifier, and surfaced in foreign_files as
// `"odd \"name\".md"`. -z keeps it literal; --no-renames keeps a moved file as
// the deletion and the addition the callers classify.
func TestDirtyChangesKeepsOddPathsLiteralAndSplitsRenames(t *testing.T) {
	git := gittest.GitOrSkip(t)
	dir := initRepo(t, git)
	fdir := filepath.Join(dir, ".furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(context.Background(), fdir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(context.Background(), "seed", ".furrow"); err != nil {
		t.Fatal(err)
	}
	odd := `odd "name" é.md`
	if err := os.WriteFile(filepath.Join(fdir, odd), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, dir, "mv", ".furrow/a.md", ".furrow/b.md")

	changes, err := r.DirtyChanges(context.Background(), ".furrow")
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Path: ".furrow/a.md", Deleted: true},
		{Path: ".furrow/b.md"},
		{Path: ".furrow/" + odd, Untracked: true},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("DirtyChanges = %+v, want %+v", changes, want)
	}
}
