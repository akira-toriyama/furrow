package app

import (
	"os"
	"path/filepath"
	"testing"
)

// `furrow board` is contractually the command that NEVER fails — the last one
// that still answers when board and binary disagree, which is what makes it a CI
// pre-flight. Probing git could have broken that, so every outcome must fold
// into the closed state vocabulary instead of an error. Each row here is one of
// those outcomes, driven through real git.
func TestBoardGitStates(t *testing.T) {
	git, cloneA, _ := setupClones(t)

	g := openBoard(t, cloneA).Board().Git
	if g.State != GitOK {
		t.Fatalf("a pushed clone should be %q, got %q", GitOK, g.State)
	}
	if g.Commit == "" || g.CommitTime == nil || g.Subject != "board" {
		t.Errorf("HEAD not reported: %+v", g)
	}
	if !g.CommitTime.Equal(g.CommitTime.UTC()) {
		t.Errorf("commit_time must be UTC, got %s", g.CommitTime)
	}
	if g.Dirty || g.Ahead != 0 || g.Behind != 0 {
		t.Errorf("a freshly pushed board is clean and level: %+v", g)
	}

	// An uncommitted write under .furrow/ is dirty — the "someone is mid-edit"
	// signal a spot write needs before it starts.
	if _, err := openBoard(t, cloneA).Add("uncommitted", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if g := openBoard(t, cloneA).Board().Git; !g.Dirty {
		t.Errorf("an uncommitted shard must read as dirty: %+v", g)
	}

	// Committed but unpushed = ahead. With autocommit off, commit by hand.
	runGitT(t, git, cloneA, "add", "-A")
	runGitT(t, git, cloneA, "commit", "-q", "-m", "local work")
	g = openBoard(t, cloneA).Board().Git
	if g.Ahead != 1 || g.Behind != 0 || g.Dirty {
		t.Errorf("one unpushed commit should be ahead=1, clean: %+v", g)
	}
	if g.Subject != "local work" {
		t.Errorf("subject should track HEAD, got %q", g.Subject)
	}
}

// A board outside git at all — the standalone case. It is a STATE, never an
// error, and the human line stays silent about it.
func TestBoardGitNotARepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	g := openBoard(t, dir).Board().Git
	if g.State != GitNotARepo {
		t.Errorf("a board outside git should be %q, got %q", GitNotARepo, g.State)
	}
}

// A git repo with NO commits: there is no HEAD to compare, and git's complaint
// about that is not the "no upstream configured" wording AheadBehind
// classifies — so without the explicit gate this reported `unavailable`, i.e.
// "the probe broke", for a board that is merely new.
func TestBoardGitFreshRepoIsNoUpstreamNotUnavailable(t *testing.T) {
	git := gitOrSkip(t)
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, dir, "init", "-q", "-b", "main")

	g := openBoard(t, dir).Board().Git
	if g.State != GitNoUpstream {
		t.Errorf("a repo with no commits should be %q, got %q", GitNoUpstream, g.State)
	}
	if g.Commit != "" || g.CommitTime != nil {
		t.Errorf("there is no HEAD to report: %+v", g)
	}
	if !g.Dirty {
		t.Errorf("the just-initialized board is uncommitted, so dirty: %+v", g)
	}
}

// Dirty is scoped to .furrow/ deliberately: a co-located operator's dirty code
// file is not the board's business, and on a repo-local board every ordinary
// source edit would otherwise report the BOARD as dirty.
func TestBoardGitDirtyIsScopedToTheStore(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	if err := os.WriteFile(filepath.Join(cloneA, "notes.md"), []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "status", "--porcelain") // sanity: the file is really there

	if g := openBoard(t, cloneA).Board().Git; g.Dirty {
		t.Errorf("a dirty file OUTSIDE .furrow must not mark the board dirty: %+v", g)
	}
}
