package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `edit --body` is a store write like `note`: it must leave the board committed
// (t-m0v2: it used to leave `M bodies/<id>.md` behind, silently — neither
// committed nor journaled — while note beside it committed).
func TestAutoCommit_EditBodyCommits(t *testing.T) {
	gitAt, commitCount := autoCommitBoard(t)
	id := addTask(t, "rewrite me")
	before := commitCount()
	if out, code := runIn(t, "# rewritten\n\nnew plan\n", "edit", id, "--body", "-"); code != 0 {
		t.Fatalf("edit --body - exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want exactly one autocommit after `edit --body`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after edit --body (its body write was left behind):\n%s", s)
	}
	if subj := strings.TrimSpace(gitAt("log", "-1", "--format=%s")); !strings.Contains(subj, "furrow edit") {
		t.Errorf("commit subject = %q, want it to name `furrow edit`", subj)
	}
	if shown := gitAt("show", "--stat", "--format=", "HEAD"); !strings.Contains(shown, "bodies/"+id+".md") {
		t.Errorf("edit's autocommit must carry the body it replaced:\n%s", shown)
	}
}

// The editor / path-printing arm writes nothing in-process, so the hook must
// not fire for it: a pre-dirty machine file (a board-level .gitignore, exactly
// what a flush would sweep) stays uncommitted across a bare `furrow edit`.
func TestAutoCommit_EditPathArmDoesNotCommit(t *testing.T) {
	gitAt, commitCount := autoCommitBoard(t)
	id := addTask(t, "look only")
	boardDir := strings.TrimSpace(mustRun(t, "edit", id))
	if !strings.HasSuffix(boardDir, filepath.Join("bodies", id+".md")) {
		t.Fatalf("no-TTY edit must print the body path, got %q", boardDir)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(boardDir)), ".gitignore"), []byte("scratch/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := commitCount()
	if out, code := run(t, "edit", id); code != 0 {
		t.Fatalf("edit exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before {
		t.Errorf("the path-printing arm of edit must not autocommit; commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); !strings.Contains(s, ".gitignore") {
		t.Errorf("the pre-dirty .gitignore must still be uncommitted:\n%s", s)
	}
}

// unarchive moves a shard and its body back out of archive/: one autocommit,
// clean tree (it was missing from the gate while archive, its inverse, was in).
func TestAutoCommit_UnarchiveCommits(t *testing.T) {
	gitAt, commitCount := autoCommitBoard(t)
	id := addTask(t, "to the attic and back")
	mustRun(t, "done", id)
	mustRun(t, "archive", id, "--yes")
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Fatalf("archive --yes must have autocommitted (fixture assumption):\n%s", s)
	}
	before := commitCount()
	if out, code := run(t, "unarchive", id); code != 0 {
		t.Fatalf("unarchive exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want exactly one autocommit after `unarchive`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after unarchive:\n%s", s)
	}
	if shown := gitAt("show", "--stat", "--format=", "HEAD"); !strings.Contains(shown, "bodies/"+id+".md") || !strings.Contains(shown, "tasks/"+id+".json") {
		t.Errorf("unarchive's autocommit must carry the restored shard and body:\n%s", shown)
	}
}

// tidy --yes rewrites shards: one autocommit, clean tree.
func TestAutoCommit_TidyApplyCommits(t *testing.T) {
	gitAt, commitCount := autoCommitBoard(t)
	dep := addTask(t, "prerequisite")
	id := addTask(t, "dependent")
	mustRun(t, "dep", id, dep)
	mustRun(t, "done", dep)
	before := commitCount()
	if out, code := run(t, "tidy", "--done-deps", "--yes"); code != 0 {
		t.Fatalf("tidy exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want exactly one autocommit after `tidy --yes`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after tidy --yes:\n%s", s)
	}
	if shown := gitAt("show", "--stat", "--format=", "HEAD"); !strings.Contains(shown, "tasks/"+id+".json") {
		t.Errorf("tidy's autocommit must carry the pruned shard:\n%s", shown)
	}
}
