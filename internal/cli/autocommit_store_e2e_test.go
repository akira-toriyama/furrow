package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/gittest"
)

// TestAutoCommit_InsideBoardTreeViaCLI is t-19gf's reproduction through the
// real command tree: the documented standalone recipe (a [[board]] naming
// <repo>/claude_workspace/.furrow, autocommit on) run from INSIDE
// claude_workspace/, where the walk finds the store (source local) and the
// [[board]] scope never resolves the command. The write must still be
// committed, and `furrow board` must say so.
func TestAutoCommit_InsideBoardTreeViaCLI(t *testing.T) {
	git := gittest.GitOrSkip(t)
	t.Setenv(app.EnvDir, "")
	t.Setenv(app.EnvBoard, "")

	scope := t.TempDir()
	boardRoot := filepath.Join(scope, "claude_workspace")
	if _, err := app.Init(boardRoot); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(boardRoot, app.DirName)
	gitAt := func(args ...string) string { return gittest.RunGit(t, git, boardRoot, args...) }
	gitAt("init", "-q", "-b", "main")
	gitAt("add", "-A")
	gitAt("commit", "-q", "-m", "board")

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	fdir := filepath.Join(cfgDir, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[[board]]\npath = \"" + board + "\"\nscopes = [\"" + scope + "\"]\nrepo = \"\"\nautocommit = true\n"
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	inside := filepath.Join(boardRoot, "notes")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)

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

	commitCount := func() int {
		n, err := strconv.Atoi(strings.TrimSpace(gitAt("rev-list", "--count", "HEAD")))
		if err != nil {
			t.Fatalf("rev-list --count HEAD: %v", err)
		}
		return n
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
