package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// TestAutoCommit_EndToEndViaCLI is the whole-stack proof: a user-config
// [[board]] with autocommit=true, driven through the real cobra command tree
// (run -> root.Execute -> PersistentPostRunE), makes `furrow add` leave the
// board committed — exercising the seam the App-level tests can't (openApp
// stashing the App, the mutatingCommands gate, the post-run hook firing on
// success).
func TestAutoCommit_EndToEndViaCLI(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv(app.EnvDir, "")
	t.Setenv(app.EnvBoard, "")

	// The board is its own git repo: boardRoot/.furrow with `git init` at boardRoot.
	scope := t.TempDir()
	boardRoot := filepath.Join(scope, "central")
	if _, err := app.Init(boardRoot); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(boardRoot, app.DirName)
	gitAt := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = boardRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	gitAt("init", "-q", "-b", "main")
	gitAt("add", "-A")
	gitAt("commit", "-q", "-m", "board")

	// user config: [[board]] scoped to the whole tree, autocommit on.
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

	// Drive `furrow add` from a working dir under scope (no FURROW_DIR — resolution
	// must find the user-config board for autocommit to be in play).
	work := filepath.Join(scope, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	commitCount := func() int {
		n, err := strconv.Atoi(strings.TrimSpace(gitAt("rev-list", "--count", "HEAD")))
		if err != nil {
			t.Fatalf("rev-list --count HEAD: %v", err)
		}
		return n
	}
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
}
