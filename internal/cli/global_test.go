package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// TestLs_GlobalBoardNoGitWarnOnStderr drives a real `ls` from a directory that
// is under a central board's scope but is not inside any git repo. The
// board activates (so the task shows on stdout) and the "no repo scope" warning
// lands on stderr, never stdout.
func TestLs_GlobalBoardNoGitWarnOnStderr(t *testing.T) {
	t.Setenv(app.EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	central := filepath.Join(scope, "projects")
	if _, err := app.Init(central); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(central, app.DirName)

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	fdir := filepath.Join(cfgDir, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[[board]]\npath = \"" + board + "\"\nscopes = [\"" + scope + "\"]\nrepo = \"auto\"\n"
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed one task on the board (FURROW_DIR opens it directly), then clear it.
	t.Setenv(app.EnvDir, board)
	a, err := app.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("seed task", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.EnvDir, "")

	plain := filepath.Join(scope, "plain") // under scope, but not a git repo
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(plain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	so, se := mustSplit(t, "ls")
	if !strings.Contains(se, "no enclosing git repo") {
		t.Errorf("missing no-git warning on stderr:\n%s", se)
	}
	if strings.Contains(so, "no enclosing git repo") {
		t.Errorf("warning leaked into stdout:\n%s", so)
	}
	if !strings.Contains(so, "seed task") {
		t.Errorf("board task missing from stdout:\n%s", so)
	}
}
