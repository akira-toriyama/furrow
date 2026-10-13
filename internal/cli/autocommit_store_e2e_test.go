package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAutoCommit_InsideBoardTreeViaCLI is t-19gf's reproduction through the
// real command tree: the documented standalone recipe (a [[board]] naming
// <repo>/claude_workspace/.furrow, autocommit on) run from INSIDE
// claude_workspace/, where the walk finds the store (source local) and the
// [[board]] scope never resolves the command. The write must still be
// committed, and `furrow board` must say so.
func TestAutoCommit_InsideBoardTreeViaCLI(t *testing.T) {
	fx := autoCommitLayout(t, "claude_workspace", true)
	inside := filepath.Join(fx.boardRoot, "notes")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)
	gitAt, commitCount := fx.gitAt, fx.commitCount

	out, code := run(t, "board")
	if code != 0 {
		t.Fatalf("board exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "source: local") && !strings.Contains(out, "local") {
		t.Fatalf("board must report the walk (local) found the store:\n%s", out)
	}
	if !strings.Contains(out, "autocommit: on") {
		t.Errorf("board must report autocommit on for a store the user config names:\n%s", out)
	}

	before := commitCount()
	if out, code := run(t, "add", "from inside the board tree"); code != 0 {
		t.Fatalf("add exit %d:\n%s", code, out)
	}
	if got := commitCount(); got != before+1 {
		t.Errorf("want exactly one autocommit after `add` from inside the board's tree, commit count %d -> %d", before, got)
	}
	if s := strings.TrimSpace(gitAt("status", "--porcelain")); s != "" {
		t.Errorf("board must be clean after autocommit:\n%s", s)
	}
}
