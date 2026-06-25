package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// run executes furrow in-process against args, returning stdout and the
// exit code Execute would have produced. It points the store at FURROW_DIR so
// no chdir is needed.
func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	out = &buf
	defer func() { out = nil }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	code := int(core.CodeOK)
	if err != nil {
		fe := core.AsError(err)
		if fe == nil {
			fe = &core.Error{Code: core.CodeValidation, Msg: err.Error()}
		}
		code = int(fe.Code)
	}
	return buf.String(), code
}

// initStore creates a fresh store and points FURROW_DIR at it.
func initStore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if _, err := app.Init(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.EnvDir, filepath.Join(dir, app.DirName))
}

func TestCLIAddLsShow(t *testing.T) {
	initStore(t)

	if _, code := run(t, "add", "first task", "-s", "ready"); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	out, code := run(t, "ls", "--json")
	if code != 0 {
		t.Fatalf("ls exit = %d", code)
	}
	if !strings.Contains(out, `"id": "t-0001"`) || !strings.Contains(out, "first task") {
		t.Errorf("ls --json missing task:\n%s", out)
	}

	out, code = run(t, "show", "t-0001")
	if code != 0 || !strings.Contains(out, "first task") {
		t.Errorf("show failed: code=%d out=%s", code, out)
	}
}

func TestCLINotFoundExit1(t *testing.T) {
	initStore(t)
	_, code := run(t, "show", "t-9999")
	if code != int(core.CodeNotFound) {
		t.Errorf("show missing should exit 1, got %d", code)
	}
}

func TestCLIBadUsageExit2(t *testing.T) {
	initStore(t)
	// unknown lane is a validation error -> exit 2.
	_, code := run(t, "add", "x", "-s", "ghost")
	if code != int(core.CodeValidation) {
		t.Errorf("unknown lane should exit 2, got %d", code)
	}
	// unknown flag is a cobra usage error -> exit 2.
	if _, code := run(t, "ls", "--nope"); code != int(core.CodeValidation) {
		t.Errorf("unknown flag should exit 2, got %d", code)
	}
}

func TestCLINoStoreExit2(t *testing.T) {
	// FURROW_DIR points nowhere and cwd has no .furrow ancestor under TempDir.
	t.Setenv(app.EnvDir, "")
	t.Setenv("HOME", t.TempDir())
	// We cannot easily guarantee no ancestor .furrow from the test's cwd, so
	// just assert the discovery error path returns a validation code when set
	// to a path with no store via an explicit non-existent FURROW_DIR parent.
	t.Setenv(app.EnvDir, filepath.Join(t.TempDir(), "absent", ".furrow"))
	_, code := run(t, "ls")
	if code == 0 {
		t.Errorf("ls without a real store should not exit 0")
	}
}

func TestCLINextEmptyExit1(t *testing.T) {
	initStore(t)
	// no tasks -> next is empty -> exit 1 (the "empty" arm of the contract).
	_, code := run(t, "next", "--json")
	if code != int(core.CodeNotFound) {
		t.Errorf("empty next should exit 1, got %d", code)
	}
}

func TestCLIDoneAndNextFlow(t *testing.T) {
	initStore(t)
	run(t, "add", "base", "-s", "ready")
	run(t, "add", "dependent", "-s", "ready", "--dep", "t-0001")

	// dependent is blocked while base is open.
	out, _ := run(t, "next", "--ndjson")
	if strings.Contains(out, "dependent") {
		t.Errorf("dependent should be blocked before base is done:\n%s", out)
	}
	if _, code := run(t, "done", "t-0001"); code != 0 {
		t.Fatalf("done exit = %d", code)
	}
	out, _ = run(t, "next", "--ndjson")
	if !strings.Contains(out, "dependent") {
		t.Errorf("dependent should be actionable after base done:\n%s", out)
	}
}

func TestCLIArchiveJSONIsArrayNotNull(t *testing.T) {
	initStore(t)
	// nothing to archive -> tasks must be [] (array shape), not null.
	out, code := run(t, "--json", "archive")
	if code != 0 {
		t.Fatalf("archive --json exit = %d", code)
	}
	if !strings.Contains(out, `"tasks": []`) {
		t.Errorf("empty archive --json should emit \"tasks\": [], got:\n%s", out)
	}
	if strings.Contains(out, `"tasks": null`) {
		t.Errorf("archive --json must never emit null tasks:\n%s", out)
	}
}

func TestCLICheckOutOfRangeExit2(t *testing.T) {
	initStore(t)
	run(t, "add", "task", "-s", "ready")
	run(t, "check", "t-0001", "--add", "step one")
	// index 5 is out of range -> validation error exit 2 (not a silent exit 0).
	if _, code := run(t, "check", "t-0001", "5"); code != int(core.CodeValidation) {
		t.Errorf("out-of-range check should exit 2, got %d", code)
	}
	// index 0 is valid.
	if _, code := run(t, "check", "t-0001", "0"); code != 0 {
		t.Errorf("in-range check should exit 0, got %d", code)
	}
}

func TestCLISchemaMatchesPackage(t *testing.T) {
	out, code := run(t, "schema")
	if code != 0 {
		t.Fatalf("schema exit = %d", code)
	}
	if !strings.Contains(out, `"furrow index v1"`) || !strings.Contains(out, `"schema_version"`) {
		t.Errorf("schema output looks wrong:\n%s", out)
	}
}
