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

// addTask runs `add --json` and returns the created task's id. Ids are random,
// so tests capture them rather than hardcoding t-0001.
func addTask(t *testing.T, args ...string) string {
	t.Helper()
	out, code := run(t, append([]string{"--json", "add"}, args...)...)
	if code != 0 {
		t.Fatalf("add exit = %d:\n%s", code, out)
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("parse add --json: %v\n%s", err, out)
	}
	if task.ID == "" {
		t.Fatalf("add --json returned no id:\n%s", out)
	}
	return task.ID
}

func TestCLIAddLsShow(t *testing.T) {
	initStore(t)

	id := addTask(t, "first task", "-s", "ready")
	out, code := run(t, "ls", "--json")
	if code != 0 {
		t.Fatalf("ls exit = %d", code)
	}
	if !strings.Contains(out, `"id": "`+id+`"`) || !strings.Contains(out, "first task") {
		t.Errorf("ls --json missing task:\n%s", out)
	}

	out, code = run(t, "show", id)
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
	base := addTask(t, "base", "-s", "ready")
	follow := addTask(t, "follow", "-s", "ready", "--dep", base)
	run(t, "done", base) // base leaves next; follow becomes actionable

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
		if views[i].ID == follow {
			got = &views[i]
		}
	}
	if got == nil {
		t.Fatalf("%s (follow) should be actionable after its dep is done:\n%s", follow, out)
	}
	if got.Reason.InNextLane != "ready" {
		t.Errorf("reason.in_next_lane = %q, want ready", got.Reason.InNextLane)
	}
	if len(got.Reason.DepsSatisfied) != 1 || got.Reason.DepsSatisfied[0] != base {
		t.Errorf("reason.deps_satisfied = %v, want [%s]", got.Reason.DepsSatisfied, base)
	}
}

func TestCLIDoneAndNextFlow(t *testing.T) {
	initStore(t)
	base := addTask(t, "base", "-s", "ready")
	addTask(t, "dependent", "-s", "ready", "--dep", base)

	// dependent is blocked while base is open.
	out, _ := run(t, "next", "--ndjson")
	if strings.Contains(out, "dependent") {
		t.Errorf("dependent should be blocked before base is done:\n%s", out)
	}
	if _, code := run(t, "done", base); code != 0 {
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
	id := addTask(t, "task", "-s", "ready")
	run(t, "check", id, "--add", "step one")
	// index 5 is out of range -> validation error exit 2 (not a silent exit 0).
	if _, code := run(t, "check", id, "5"); code != int(core.CodeValidation) {
		t.Errorf("out-of-range check should exit 2, got %d", code)
	}
	// index 0 is valid.
	if _, code := run(t, "check", id, "0"); code != 0 {
		t.Errorf("in-range check should exit 0, got %d", code)
	}
}

func TestCLIDepAddRemove(t *testing.T) {
	initStore(t)
	base := addTask(t, "base", "-s", "ready")
	dependent := addTask(t, "dependent", "-s", "ready")

	// add a dep, then confirm `next` blocks the dependent until base is done.
	if _, code := run(t, "dep", dependent, base); code != 0 {
		t.Fatalf("dep add exit = %d", code)
	}
	out, _ := run(t, "next", "--ndjson")
	if strings.Contains(out, "dependent") {
		t.Errorf("dependent should be blocked after `dep` wired the edge:\n%s", out)
	}

	// self-dep and cycle are validation errors (exit 2).
	if _, code := run(t, "dep", base, base); code != int(core.CodeValidation) {
		t.Errorf("self-dep should exit 2, got %d", code)
	}
	if _, code := run(t, "dep", base, dependent); code != int(core.CodeValidation) {
		t.Errorf("cycle-creating dep should exit 2, got %d", code)
	}

	// remove it; the dependent becomes actionable again.
	if _, code := run(t, "dep", dependent, base, "--rm"); code != 0 {
		t.Fatalf("dep --rm exit = %d", code)
	}
	out, _ = run(t, "next", "--ndjson")
	if !strings.Contains(out, "dependent") {
		t.Errorf("dependent should be actionable after the dep was removed:\n%s", out)
	}
	// removing a non-existent dep is a validation error.
	if _, code := run(t, "dep", dependent, base, "--rm"); code != int(core.CodeValidation) {
		t.Errorf("removing a non-dependency should exit 2, got %d", code)
	}
}

func TestCLILabelAddRemove(t *testing.T) {
	initStore(t)
	id := addTask(t, "task", "-s", "ready", "-l", "chord", "-l", "shared")

	out, code := run(t, "--json", "label", id, "--add", "sill", "--remove", "chord")
	if code != 0 {
		t.Fatalf("label --json exit = %d:\n%s", code, out)
	}
	var res struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("label --json should be an object: %v\n%s", err, out)
	}
	if res.Before == nil || res.After == nil {
		t.Fatalf("label --json must carry before+after:\n%s", out)
	}
	if got := strings.Join(res.After.Labels, ","); got != "shared,sill" {
		t.Errorf("after labels = %q, want %q", got, "shared,sill")
	}
	has := false
	for _, c := range res.Changed {
		if c == "labels" {
			has = true
		}
	}
	if !has {
		t.Errorf("changed should include \"labels\": %v", res.Changed)
	}

	// no --add/--remove is a bad-usage error (exit 2).
	if _, code := run(t, "label", id); code != int(core.CodeValidation) {
		t.Errorf("label with no flags should exit 2, got %d", code)
	}
	// unknown id is not-found (exit 1).
	if _, code := run(t, "label", "t-9999", "--add", "x"); code != int(core.CodeNotFound) {
		t.Errorf("label on unknown id should exit 1, got %d", code)
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
	id := addTask(t, "task", "-s", "ready")

	out, code := run(t, "--json", "done", id)
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
	hout, _ := run(t, "done", id)
	if strings.Contains(hout, "before") {
		t.Errorf("human mutation output must stay terse:\n%s", hout)
	}
}

func TestCLISchemaMatchesPackage(t *testing.T) {
	// Default (no arg) prints the task-shard schema.
	out, code := run(t, "schema")
	if code != 0 {
		t.Fatalf("schema exit = %d", code)
	}
	if !strings.Contains(out, `"furrow task shard v2"`) || !strings.Contains(out, `"checklist"`) {
		t.Errorf("task schema output looks wrong:\n%s", out)
	}
	// v2 promotes repos to a first-class, required set.
	if !strings.Contains(out, `"repos"`) {
		t.Errorf("task schema v2 must declare repos:\n%s", out)
	}
	// A task shard carries no schema_version PROPERTY — that belongs to meta.json
	// (the description prose may still mention it).
	if strings.Contains(out, `"schema_version": {`) {
		t.Errorf("task schema must not declare a schema_version property:\n%s", out)
	}

	// `schema meta` prints the meta.json schema, which owns the schema_version.
	mout, mcode := run(t, "schema", "meta")
	if mcode != 0 {
		t.Fatalf("schema meta exit = %d", mcode)
	}
	if !strings.Contains(mout, `"furrow meta v2"`) || !strings.Contains(mout, `"schema_version": {`) {
		t.Errorf("meta schema output looks wrong:\n%s", mout)
	}

	// An unknown kind is a usage error.
	if _, code := run(t, "schema", "bogus"); code == 0 {
		t.Error("schema with an unknown kind should be a non-zero usage error")
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

func TestCLIValueEffort(t *testing.T) {
	initStore(t)
	id := addTask(t, "estimate me", "-s", "ready")

	parseAfter := func(out string) (*core.Task, []string) {
		t.Helper()
		var res struct {
			After   *core.Task `json:"after"`
			Changed []string   `json:"changed"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("parse mutation json: %v\n%s", err, out)
		}
		return res.After, res.Changed
	}

	out, code := run(t, "--json", "value", id, "4")
	if code != 0 {
		t.Fatalf("value exit = %d:\n%s", code, out)
	}
	after, changed := parseAfter(out)
	if after.Value == nil || *after.Value != 4 {
		t.Errorf("value should be 4, got %v", after.Value)
	}
	if !strings.Contains(strings.Join(changed, ","), "value") {
		t.Errorf("changed should list value, got %v", changed)
	}

	out, _ = run(t, "--json", "effort", id, "2")
	after, _ = parseAfter(out)
	if after.Effort == nil || *after.Effort != 2 {
		t.Errorf("effort should be 2, got %v", after.Effort)
	}

	// show --json carries both (so ROI = value/effort is derivable downstream).
	out, _ = run(t, "show", id, "--json")
	if !strings.Contains(out, `"value": 4`) || !strings.Contains(out, `"effort": 2`) {
		t.Errorf("show --json missing value/effort:\n%s", out)
	}

	// clamp-don't-reject: 9 lands as 5.
	out, _ = run(t, "--json", "value", id, "9")
	after, _ = parseAfter(out)
	if after.Value == nil || *after.Value != 5 {
		t.Errorf("out-of-range value should clamp to 5, got %v", after.Value)
	}

	// --clear returns the task to unset.
	out, _ = run(t, "--json", "value", id, "--clear")
	after, _ = parseAfter(out)
	if after.Value != nil {
		t.Errorf("--clear should unset value, got %v", after.Value)
	}
}

func TestCLIAddWithEstimate(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "add", "scoped", "-s", "ready", "--value", "3", "--effort", "2")
	if code != 0 {
		t.Fatalf("add exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"value": 3`) || !strings.Contains(out, `"effort": 2`) {
		t.Errorf("add --json missing value/effort:\n%s", out)
	}
}

func TestCLIValueRequiresArgOrClear(t *testing.T) {
	initStore(t)
	id := addTask(t, "x", "-s", "ready")
	// neither n nor --clear is a usage error.
	if _, code := run(t, "value", id); code != int(core.CodeValidation) {
		t.Errorf("value with no score and no --clear should exit 2, got %d", code)
	}
}

func TestCLIShowDisplaysEstimate(t *testing.T) {
	initStore(t)
	id := addTask(t, "x", "-s", "ready", "--value", "4", "--effort", "2")
	out, code := run(t, "show", id) // human output (no --json)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	for _, want := range []string{"value:", "effort:", "roi:"} {
		if !strings.Contains(out, want) {
			t.Errorf("human show should display %q:\n%s", want, out)
		}
	}
}
