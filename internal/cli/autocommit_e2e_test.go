package cli

import (
	"strings"
	"testing"
)

// Autocommit on a board that is no git repository: the mutation succeeds
// (exit 0, the task exists) and the flush's warning reaches stderr with the
// `warning:` prefix and the remedy, through the post-run hook's own loop.
func TestAutoCommit_WarnsOnStderrWhenTheBoardIsNoGitRepo(t *testing.T) {
	fx := autoCommitLayout(t, "central", false)
	t.Chdir(fx.work)

	stdout, stderr, code := runSplit(t, "add", "no repo to commit into")
	if code != 0 || !strings.Contains(stdout, "t-") {
		t.Fatalf("the add itself must succeed (exit %d):\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "warning: autocommit: board "+fx.board+" is not inside a git repository") || !strings.Contains(stderr, "git init") {
		t.Errorf("stderr must carry the flush's warning with its prefix and remedy:\n%s", stderr)
	}
	if strings.Contains(stdout, "autocommit") {
		t.Errorf("the warning belongs on stderr, not in the data stream:\n%s", stdout)
	}
}

// TestAutoCommit_EndToEndViaCLI is the whole-stack proof: a user-config
// [[board]] with autocommit=true, driven through the real cobra command tree
// (run -> root.Execute -> PersistentPostRunE), makes `furrow add` leave the
// board committed — exercising the seam the App-level tests can't (openApp
// stashing the App, the mutatingCommands gate, the post-run hook firing on
// success).
func TestAutoCommit_EndToEndViaCLI(t *testing.T) {
	gitAt, commitCount := autoCommitBoard(t)

	before := commitCount()

	if _, code := run(t, "add", "e2e task"); code != 0 {
		t.Fatalf("add exit code %d, want 0", code)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want exactly one autocommit after `add`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after autocommit:\n%s", s)
	}
	if subj := strings.TrimSpace(gitAt("log", "-1", "--format=%s")); !strings.Contains(subj, "furrow add") {
		t.Errorf("commit subject = %q, want it to name `furrow add`", subj)
	}

	// A READ command (ls) must NOT create a commit — the gate excludes reads.
	before = commitCount()
	if _, code := run(t, "ls"); code != 0 {
		t.Fatalf("ls exit code %d, want 0", code)
	}
	if got := commitCount(); got != before {
		t.Errorf("a read command must not autocommit; commit count %d -> %d", before, got)
	}

	// A NESTED mutator fires too: the gate is keyed by the top-level name, so
	// `epic activate` — whose activation record is a BODY write — commits its
	// shard and prose in one autocommit named after the full command path.
	box := addEpic(t, "box", "-r", "o/r")
	before = commitCount()
	if out, code := run(t, "epic", "activate", box); code != 0 {
		t.Fatalf("epic activate exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want one autocommit after `epic activate`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after `epic activate` (its body write was left behind):\n%s", s)
	}
	if subj := strings.TrimSpace(gitAt("log", "-1", "--format=%s")); !strings.Contains(subj, "furrow epic activate") {
		t.Errorf("commit subject = %q, want it to name `furrow epic activate`", subj)
	}

	// A deletion is a furrow-owned change: `rm --yes` commits the removed shard
	// AND body in one autocommit, leaving nothing for a later sync to disclose.
	id := addTask(t, "withdrawn")
	before = commitCount()
	if out, code := run(t, "rm", id, "--yes"); code != 0 {
		t.Fatalf("rm exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want one autocommit after `rm`, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after rm (a deleted body left pending):\n%s", s)
	}
	if shown := gitAt("show", "--stat", "--format=", "HEAD"); !strings.Contains(shown, "bodies/"+id+".md") || !strings.Contains(shown, "tasks/"+id+".json") {
		t.Errorf("rm's autocommit must carry both deletions:\n%s", shown)
	}
}
