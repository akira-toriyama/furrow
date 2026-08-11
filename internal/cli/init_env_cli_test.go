package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// t-jqde: `furrow init` follows the same placement precedence every other
// command reads by. Under FURROW_DIR it used to build ./.furrow anyway (exit
// 0) while the very next command failed exit 2 on the still-missing env path —
// this repo's .gitignore carries the scar of exactly that stray board.
func TestInitHonorsEnvOverrides(t *testing.T) {
	base := t.TempDir()
	store := filepath.Join(base, "intended", "board-dir")
	t.Setenv(app.EnvDir, store)
	t.Setenv(app.EnvBoard, "")
	t.Chdir(t.TempDir()) // cwd is somewhere else entirely

	out, code := run(t, "init")
	if code != 0 {
		t.Fatalf("init exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, store) {
		t.Errorf("init must report the env path, got: %q", out)
	}
	if fi, err := os.Stat(store); err != nil || !fi.IsDir() {
		t.Errorf("store must exist at FURROW_DIR: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".", app.DirName)); err == nil {
		t.Error("no stray ./.furrow may be created")
	}

	// The whole point: the very next command, same env, works.
	if out, code := run(t, "add", "first task"); code != 0 {
		t.Fatalf("add after init exit %d:\n%s", code, out)
	}

	// A contradicting argument is refused — never a silent elsewhere.
	fe, _ := runErr(t, "init", filepath.Join(base, "other"))
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("init <dir> under a set FURROW_DIR must be exit 2, got %+v", fe)
	}
}

// FURROW_BOARD is the second override, same contract. Its discovery scope is
// DERIVED (the board path's grandparent), so the working dir sits under it —
// exactly the agent-bootstrap shape the override exists for.
func TestInitHonorsBoardEnv(t *testing.T) {
	base := t.TempDir()
	store := filepath.Join(base, "central", ".furrow")
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.EnvDir, "")
	t.Setenv(app.EnvBoard, store)
	t.Chdir(work)

	if out, code := run(t, "init"); code != 0 {
		t.Fatalf("init exit %d:\n%s", code, out)
	}
	if fi, err := os.Stat(store); err != nil || !fi.IsDir() {
		t.Errorf("store must exist at FURROW_BOARD: %v", err)
	}
	fe2, out2 := runErr(t, "add", "first task")
	if fe2 != nil {
		t.Fatalf("add after init failed: %+v\n%s", fe2, out2)
	}

	// An AGREEING argument reaches the already-exists check (agreement of
	// intent is fine; the store existing is its own validation error).
	fe3, _ := runErr(t, "init", filepath.Join(base, "central"))
	if fe3 == nil || !strings.Contains(fe3.Msg, "already exists") {
		t.Fatalf("agreeing arg should reach the already-exists check, got %+v", fe3)
	}
}
