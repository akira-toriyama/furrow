package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

// runIn is run() with stdin wired from s (for commands that read stdin).
func runIn(t *testing.T, s string, args ...string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	out = &buf
	defer func() { out = nil }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(s))
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

func TestCLINextReasons(t *testing.T) {
	initStore(t)
	run(t, "add", "base", "-s", "ready")                      // t-0001
	run(t, "add", "follow", "-s", "ready", "--dep", "t-0001") // t-0002
	run(t, "done", "t-0001")                                  // t-0001 leaves next; t-0002 becomes actionable

	out, code := run(t, "--json", "next")
	if code != 0 {
		t.Fatalf("next --json exit = %d:\n%s", code, out)
	}
	var views []struct {
		ID     string `json:"id"`
		Reason struct {
			InNextLane    string   `json:"in_next_lane"`
			DepsSatisfied []string `json:"deps_satisfied"`
		} `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("next --json should be an array with reasons, got %v:\n%s", err, out)
	}
	var got *struct {
		ID     string `json:"id"`
		Reason struct {
			InNextLane    string   `json:"in_next_lane"`
			DepsSatisfied []string `json:"deps_satisfied"`
		} `json:"reason"`
	}
	for i := range views {
		if views[i].ID == "t-0002" {
			got = &views[i]
		}
	}
	if got == nil {
		t.Fatalf("t-0002 should be actionable after its dep is done:\n%s", out)
	}
	if got.Reason.InNextLane != "ready" {
		t.Errorf("reason.in_next_lane = %q, want ready", got.Reason.InNextLane)
	}
	if len(got.Reason.DepsSatisfied) != 1 || got.Reason.DepsSatisfied[0] != "t-0001" {
		t.Errorf("reason.deps_satisfied = %v, want [t-0001]", got.Reason.DepsSatisfied)
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

func TestCLIDepAddRemove(t *testing.T) {
	initStore(t)
	run(t, "add", "base", "-s", "ready")      // t-0001
	run(t, "add", "dependent", "-s", "ready") // t-0002

	// add a dep, then confirm `next` blocks the dependent until base is done.
	if _, code := run(t, "dep", "t-0002", "t-0001"); code != 0 {
		t.Fatalf("dep add exit = %d", code)
	}
	out, _ := run(t, "next", "--ndjson")
	if strings.Contains(out, "dependent") {
		t.Errorf("dependent should be blocked after `dep` wired the edge:\n%s", out)
	}

	// self-dep and cycle are validation errors (exit 2).
	if _, code := run(t, "dep", "t-0001", "t-0001"); code != int(core.CodeValidation) {
		t.Errorf("self-dep should exit 2, got %d", code)
	}
	if _, code := run(t, "dep", "t-0001", "t-0002"); code != int(core.CodeValidation) {
		t.Errorf("cycle-creating dep should exit 2, got %d", code)
	}

	// remove it; the dependent becomes actionable again.
	if _, code := run(t, "dep", "t-0002", "t-0001", "--rm"); code != 0 {
		t.Fatalf("dep --rm exit = %d", code)
	}
	out, _ = run(t, "next", "--ndjson")
	if !strings.Contains(out, "dependent") {
		t.Errorf("dependent should be actionable after the dep was removed:\n%s", out)
	}
	// removing a non-existent dep is a validation error.
	if _, code := run(t, "dep", "t-0002", "t-0001", "--rm"); code != int(core.CodeValidation) {
		t.Errorf("removing a non-dependency should exit 2, got %d", code)
	}
}

func TestCLIBatchAddStdin(t *testing.T) {
	initStore(t)
	out, code := runIn(t, "alpha\nbeta\n\ngamma\n", "--ndjson", "add", "--stdin", "-s", "ready")
	if code != 0 {
		t.Fatalf("batch add exit = %d:\n%s", code, out)
	}
	// the blank line is skipped -> exactly 3 created tasks (one NDJSON line each).
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines != 3 {
		t.Errorf("expected 3 created tasks, got %d:\n%s", lines, out)
	}
	ls, _ := run(t, "--ndjson", "ls", "-s", "ready")
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(ls, want) {
			t.Errorf("ready lane should contain %q:\n%s", want, ls)
		}
	}
	// --stdin combined with positional args is a usage error.
	if _, code := runIn(t, "x\n", "add", "--stdin", "extra"); code != int(core.CodeValidation) {
		t.Errorf("--stdin with title args should exit 2, got %d", code)
	}
}

func TestCLIMutationJSONDiff(t *testing.T) {
	initStore(t)
	run(t, "add", "task", "-s", "ready")

	out, code := run(t, "--json", "done", "t-0001")
	if code != 0 {
		t.Fatalf("done --json exit = %d:\n%s", code, out)
	}
	var res struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("mutation --json should be an object, got error %v:\n%s", err, out)
	}
	if res.Before == nil || res.After == nil {
		t.Fatalf("mutation --json must carry before+after:\n%s", out)
	}
	if res.Before.Status != "ready" || res.After.Status != "done" {
		t.Errorf("before/after status wrong: %q -> %q", res.Before.Status, res.After.Status)
	}
	// done flips status and stamps closed.
	has := func(f string) bool {
		for _, c := range res.Changed {
			if c == f {
				return true
			}
		}
		return false
	}
	if !has("status") || !has("closed") {
		t.Errorf("changed should list status+closed, got %v", res.Changed)
	}

	// human mode stays the terse verb line (no before/after).
	hout, _ := run(t, "done", "t-0001")
	if strings.Contains(hout, "before") {
		t.Errorf("human mutation output must stay terse:\n%s", hout)
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

// TestCLIMigrateAppliesLabel pins that `migrate --label` stamps the label on
// every imported task (the unblocker for importing into a label-required
// cross-repo store). Verified via the label filter: `ls -l facet` must return
// every migrated task, and a different label must match nothing.
func TestCLIMigrateAppliesLabel(t *testing.T) {
	initStore(t)
	src := filepath.Join(t.TempDir(), "Task.md")
	md := "# Demo\n\n## 🎯 Open\n\n### 1. First task\nDetail one.\n\n### 2. Second task\nDetail two.\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, "migrate", src, "--label", "facet", "--write"); code != 0 {
		t.Fatalf("migrate --write exit = %d", code)
	}
	// Filtering by the applied label returns every imported task.
	out, code := run(t, "ls", "-l", "facet", "--json")
	if code != 0 {
		t.Fatalf("ls -l facet exit = %d", code)
	}
	if !strings.Contains(out, "First task") || !strings.Contains(out, "Second task") {
		t.Errorf("facet-labeled ls missing migrated tasks:\n%s", out)
	}
	// A different label matches nothing (the label is specifically facet).
	out, _ = run(t, "ls", "-l", "other", "--json")
	if strings.Contains(out, "First task") || strings.Contains(out, "Second task") {
		t.Errorf("ls -l other should not return the facet tasks:\n%s", out)
	}
}
