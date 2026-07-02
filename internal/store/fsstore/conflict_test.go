package fsstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// TestTwoOperatorsAddNoGitConflict is the payoff of sharding: two operators on
// separate branches each `add` a different task, and a git merge of the two is a
// conflict-free union. Under the old monolithic index.json both adds edited the
// same sorted array and collided on the merge; with one shard per id they touch
// disjoint files, so 3-way merge just takes both.
func TestTwoOperatorsAddNoGitConflict(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	// Some CI images default init.defaultBranch to main; pin it so branch names
	// are predictable.
	run("init", "-q", "-b", "base")

	fdir := filepath.Join(repo, ".furrow")
	store := New(fdir, lanes, "t-", 5)

	// Base commit: a store with one shared task.
	if err := store.Save(&core.Index{Tasks: []core.Task{mkTask("t-base0", "shared", "ready", 100)}}); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")

	// Operator A on branch a: add a distinct task on top of base.
	run("checkout", "-q", "-b", "a")
	idxA, _ := store.Load()
	idxA.Add(mkTask("t-aaaa1", "from A", "ready", 110))
	if err := store.Save(idxA); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "add from A")

	// Operator B branches from base and adds a *different* task.
	run("checkout", "-q", "base")
	run("checkout", "-q", "-b", "b")
	idxB, _ := store.Load() // base state (A's shard is not on this branch)
	idxB.Add(mkTask("t-bbbb2", "from B", "ready", 110))
	if err := store.Save(idxB); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "add from B")

	// Merge A into B: with one shard per id this must be a clean, automatic merge.
	cmd := exec.Command(git, "merge", "--no-edit", "a")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("merge of two independent adds conflicted (sharding should prevent this):\n%s", out)
	}

	// The merged tree has all three tasks and remains loadable.
	merged, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t-base0", "t-aaaa1", "t-bbbb2"} {
		if !merged.Has(id) {
			t.Errorf("merged store is missing %s: %+v", id, merged)
		}
	}
}
