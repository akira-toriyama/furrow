package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// TestCLISetVerb pins t-kx76 (e): `set` applies lane+value+effort+label in one
// command and reports {before,after,changed} under --json.
func TestCLISetVerb(t *testing.T) {
	initStore(t)
	id := addTask(t, "triage", "-s", "inbox")

	out, code := run(t, "--json", "set", id, "-s", "ready", "--value", "4", "--effort", "2", "--add-label", "bug")
	if code != 0 {
		t.Fatalf("set exit %d:\n%s", code, out)
	}
	var res struct {
		After struct {
			Status string   `json:"status"`
			Value  int      `json:"value"`
			Effort int      `json:"effort"`
			Labels []string `json:"labels"`
		} `json:"after"`
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal(showOne(t, out), &res); err != nil {
		t.Fatalf("parse set --json: %v\n%s", err, out)
	}
	if res.After.Status != "ready" || res.After.Value != 4 || res.After.Effort != 2 {
		t.Errorf("set did not apply all fields: %+v", res.After)
	}
	if len(res.After.Labels) != 1 || res.After.Labels[0] != "bug" {
		t.Errorf("set --add-label failed: %v", res.After.Labels)
	}
	// unknown lane is exit 2 with candidates, like move.
	fe, _ := runErr(t, "set", id, "-s", "ghost")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("set to an unknown lane should exit 2 with candidates, got %+v", fe)
	}
	if _, code := run(t, "set", id); code != int(core.CodeValidation) {
		t.Errorf("`set` with no change should exit 2, got %d", code)
	}
}

// TestCLIDepVariadic pins that `dep <id> <d1> <d2>` adds both in one call.
func TestCLIDepVariadic(t *testing.T) {
	initStore(t)
	base := addTask(t, "base", "-s", "ready")
	d1 := addTask(t, "d1", "-s", "ready")
	d2 := addTask(t, "d2", "-s", "ready")

	out, code := run(t, "--json", "dep", base, d1, d2)
	if code != 0 {
		t.Fatalf("dep variadic exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, d1) || !strings.Contains(out, d2) {
		t.Errorf("dep should add both deps:\n%s", out)
	}
	out, _ = run(t, "--json", "dep", base, d1, d2, "--rm")
	var res struct {
		After struct {
			Deps []string `json:"deps"`
		} `json:"after"`
	}
	json.Unmarshal([]byte(out), &res)
	if len(res.After.Deps) != 0 {
		t.Errorf("dep --rm should drop both: %v", res.After.Deps)
	}
}

// TestCLIAddDashTitleHint pins the おまけ: a title starting with '-' errors with
// a `--` hint, not a bare cobra usage error.
func TestCLIAddDashTitleHint(t *testing.T) {
	initStore(t)
	fe, _ := runErr(t, "add", "--bogus-title")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("a dash-leading title should exit 2, got %+v", fe)
	}
	if !strings.Contains(fe.Msg, "--") {
		t.Errorf("the error should hint at the `--` separator: %q", fe.Msg)
	}
	// the `--` separator makes it work.
	if _, code := run(t, "add", "--", "-a real title"); code != 0 {
		t.Errorf("`add -- \"-title\"` should succeed, got %d", code)
	}
}

// TestCLIChecklistModes pins t-abj3 (c-extra): `add --check` seeds items, and
// `check --reword`/`--rm` edit them by index.
func TestCLIChecklistModes(t *testing.T) {
	initStore(t)
	id := addTask(t, "steps", "--check", "one", "--check", "two")

	out, _ := run(t, "--json", "show", id)
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Fatalf("add --check should seed items:\n%s", out)
	}

	if _, code := run(t, "check", id, "0", "--reword", "ONE"); code != 0 {
		t.Fatalf("check --reword exit %d", code)
	}
	if _, code := run(t, "check", id, "1", "--rm"); code != 0 {
		t.Fatalf("check --rm exit %d", code)
	}
	out, _ = run(t, "--json", "show", id)
	var task struct {
		Checklist []struct {
			Text string `json:"text"`
		} `json:"checklist"`
	}
	if err := json.Unmarshal(showOne(t, out), &task); err != nil {
		t.Fatalf("parse show --json: %v\n%s", err, out)
	}
	if len(task.Checklist) != 1 || task.Checklist[0].Text != "ONE" {
		t.Errorf("after reword+rm want [ONE], got %+v", task.Checklist)
	}
	// mode flags are mutually exclusive.
	if _, code := run(t, "check", id, "0", "--rm", "--reword", "x"); code != int(core.CodeValidation) {
		t.Errorf("--rm + --reword should be exit 2, got %d", code)
	}
}

// TestCLICheckAddRepeatable pins that `check --add A --add B` appends BOTH items
// (was: cobra StringVar kept only the last), and that a comma inside an item is
// preserved verbatim — i.e. the flag is StringArrayVar, not StringSliceVar which
// would wrongly split free-text on commas. Regression for t-hgxw leg (c).
func TestCLICheckAddRepeatable(t *testing.T) {
	initStore(t)
	id := addTask(t, "task")
	out, code := run(t, "--json", "check", id, "--add", "buy milk, eggs", "--add", "second")
	if code != 0 {
		t.Fatalf("check --add exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "buy milk, eggs") || !strings.Contains(out, "second") {
		t.Errorf("both --add items should be appended verbatim (comma not split):\n%s", out)
	}
	// An empty --add is a validation error (exit 2), never a silent mode switch —
	// see TestCLICheckAddEmptyIsExit2NotToggle for the with-index regression.
	if _, code := run(t, "check", id, "--add", ""); code != int(core.CodeValidation) {
		t.Errorf(`check --add "" should exit 2, got %d`, code)
	}
}

// TestCLICheckAddEmptyIsExit2NotToggle is the t-fr3e regression: `check <id>
// <idx> --add ""` used to DROP the blank add, fall through to the toggle path,
// and mark item <idx> DONE at exit 0 — an empty value silently switching the
// command's mode. It must exit 2 and leave the item untouched.
func TestCLICheckAddEmptyIsExit2NotToggle(t *testing.T) {
	initStore(t)
	id := addTask(t, "task")
	if _, code := run(t, "check", id, "--add", "step one"); code != 0 {
		t.Fatalf("seed --add exit = %d", code)
	}
	if _, code := run(t, "check", id, "0", "--add", ""); code != int(core.CodeValidation) {
		t.Fatalf(`check <id> 0 --add "" must exit 2, got %d`, code)
	}
	out, code := run(t, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	var task struct {
		Checklist []struct {
			Text string `json:"text"`
			Done bool   `json:"done"`
		} `json:"checklist"`
	}
	if err := json.Unmarshal(showOne(t, out), &task); err != nil {
		t.Fatalf("parse show: %v\n%s", err, out)
	}
	if len(task.Checklist) != 1 {
		t.Fatalf("want 1 checklist item, got %d:\n%s", len(task.Checklist), out)
	}
	if task.Checklist[0].Done {
		t.Error(`item 0 was toggled done by an empty --add "" (t-fr3e mode-switch regression)`)
	}
	// --reword "" is the same class: an explicit empty value is exit 2.
	if _, code := run(t, "check", id, "0", "--reword", ""); code != int(core.CodeValidation) {
		t.Errorf(`check <id> 0 --reword "" should exit 2, got %d`, code)
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

	out, code := run(t, "--json", "label", id, "--add", "sill", "--rm", "chord")
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

	// no --add/--rm is a bad-usage error (exit 2).
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

func TestCLIRetitle(t *testing.T) {
	initStore(t)
	id := addTask(t, "old title", "-s", "ready") // body seeded "# old title\n"

	// A multi-word title need not be quoted — the trailing args are joined.
	out, code := run(t, "retitle", id, "a", "brand", "new", "title")
	if code != 0 {
		t.Fatalf("retitle exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "retitled "+id) || !strings.Contains(out, "a brand new title") {
		t.Errorf("unexpected retitle output:\n%s", out)
	}

	// The shard title moved...
	out, _ = run(t, "--json", "show", id)
	if !strings.Contains(out, `"title": "a brand new title"`) {
		t.Errorf("show does not reflect the new title:\n%s", out)
	}
	// ...and the body heading was synced on disk (real fsstore, not memstore).
	body, err := os.ReadFile(filepath.Join(os.Getenv(app.EnvDir), "bodies", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# a brand new title\n" {
		t.Errorf("body heading not synced on disk, got %q", string(body))
	}

	// A bare id (no title) is a usage error.
	if _, code := run(t, "retitle", id); code != int(core.CodeValidation) {
		t.Errorf("retitle with no title should exit 2, got %d", code)
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

// `furrow edit <id>` with no TTY prints the body file path instead of launching
// $EDITOR — the contract an agent depends on to read and edit the body
// directly. It had no test at any layer, so nothing caught a regression in the
// one shape a non-interactive caller sees.
//
// bite-exempt: characterization. This pins shipped, documented behaviour that
// nothing tested; there is no accompanying production change to fail without.
func TestCLIEditNoTTYPrintsBodyPath(t *testing.T) {
	initStore(t)
	id := addTask(t, "editable")

	out, code := run(t, "edit", id)
	if code != int(core.CodeOK) {
		t.Fatalf("edit should exit 0 with no TTY, got %d:\n%s", code, out)
	}
	path := strings.TrimSpace(out)
	want := filepath.Join(os.Getenv(app.EnvDir), "bodies", id+".md")
	if path != want {
		t.Errorf("edit printed %q, want the body file %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the printed path must exist: %v", err)
	}

	// --json emits the same path as an object, since --json is honored wherever
	// furrow emits JSON — not just on the read commands.
	out, code = run(t, "--json", "edit", id)
	if code != int(core.CodeOK) {
		t.Fatalf("edit --json exit = %d:\n%s", code, out)
	}
	var got struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("edit --json should be an object: %v\n%s", err, out)
	}
	if got.Path != want {
		t.Errorf("edit --json path = %q, want %q", got.Path, want)
	}

	// A body deleted by hand is recreated rather than handing back a path to
	// nothing — an editor opening a missing file is how an edit lands nowhere.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, "edit", id); code != int(core.CodeOK) {
		t.Fatalf("edit should recreate a missing body, exit = %d", code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("edit must recreate the missing body: %v", err)
	}

	// An unknown id is exit 1 (a specifically requested id), never a path.
	if out, code := run(t, "edit", "t-nope1"); code != int(core.CodeNotFound) {
		t.Errorf("edit on an unknown id should exit 1, got %d:\n%s", code, out)
	}
}
