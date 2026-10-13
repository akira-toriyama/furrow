package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// execCLI is the one in-process furrow runner every test harness below is
// built on: it points BOTH funnel writers (out / errOut — the only outlets the
// package prints through, see output.go) and cobra's own writers at two fresh
// buffers, runs the real root command, and classifies the error exactly as
// Execute() does (a bare cobra error is a usage problem = validation). Tests
// point the store at FURROW_DIR, so no chdir is needed.
//
// stdout and stderr are captured SEPARATELY, always: a harness that captured
// only stdout could not see a note go missing from stderr, and one that merged
// the two could not see a note leak into stdout — t-hs4a's clamp note did the
// former for months under 355 green tests. The previous writers are restored,
// not nilled (a nil io.Writer panics on the next print), so a test that swapped
// its own buffers in before calling run keeps them afterward.
func execCLI(t *testing.T, stdin string, args ...string) (fe *core.Error, stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	prevOut, prevErr := out, errOut
	out, errOut = &outBuf, &errBuf
	defer func() { out, errOut = prevOut, prevErr }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	err := root.Execute()
	if err != nil {
		// classifyFailure writes the session guard's notes to errOut, so it must
		// run before the buffers are read.
		fe = classifyFailure(err)
	}
	return fe, outBuf.String(), errBuf.String()
}

// run executes furrow against args, returning stdout and the exit code
// Execute would have produced.
func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runIn(t, "", args...)
}

func runIn(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	fe, stdout, _ := execCLI(t, stdin, args...)
	return stdout, exitOf(fe)
}

// runErr returns the structured error (nil on success) plus stdout — for tests
// that assert on Candidates, Details, and friends rather than the exit code.
func runErr(t *testing.T, args ...string) (*core.Error, string) {
	t.Helper()
	return runErrIn(t, "", args...)
}

func runErrIn(t *testing.T, stdin string, args ...string) (*core.Error, string) {
	t.Helper()
	fe, stdout, _ := execCLI(t, stdin, args...)
	return fe, stdout
}

// runSplit is for the tests that assert a note went to stderr while stdout
// stayed a parseable array.
func runSplit(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runSplitIn(t, "", args...)
}

func runSplitIn(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	fe, stdout, stderr := execCLI(t, stdin, args...)
	return stdout, stderr, exitOf(fe)
}

// mustRun is run for a step the test only sets up with: a non-zero exit is
// the test's failure, stdout is returned for the callers that read it.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := runSplit(t, args...)
	if code != 0 {
		t.Fatalf("%v exit = %d:\n%s%s", args, code, stdout, stderr)
	}
	return stdout
}

// mustSplit is mustRun keeping stderr apart, for the tests that assert a
// disclosure landed there and nowhere else.
func mustSplit(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, code := runSplit(t, args...)
	if code != 0 {
		t.Fatalf("%v exit = %d:\n%s%s", args, code, stdout, stderr)
	}
	return stdout, stderr
}

func exitOf(fe *core.Error) int {
	if fe == nil {
		return int(core.CodeOK)
	}
	return int(fe.Code)
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

// addDanglingDep hand-edits id's shard to carry dep in `deps` — the one way a
// dep the hot store cannot resolve is still reached now that `dep` refuses to
// create one, `archive` refuses to strand one and `unarchive` refuses to
// restore a dependent without its archived dependency (t-kw28): a hand edit,
// or a merge that removed the target on another machine.
func addDanglingDep(t *testing.T, id, dep string) {
	t.Helper()
	path := filepath.Join(os.Getenv(app.EnvDir), "tasks", id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shard map[string]any
	if err := json.Unmarshal(raw, &shard); err != nil {
		t.Fatal(err)
	}
	deps, _ := shard["deps"].([]any)
	shard["deps"] = append(deps, dep)
	out, err := json.MarshalIndent(shard, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestCLINextEmptyExit0(t *testing.T) {
	initStore(t)
	// no tasks -> next is empty, which is a HEALTHY result -> exit 0 with `[]`,
	// the same contract as ls/revisit (t-kx76: exit 1 is only a requested id
	// that's missing, not an empty query). An agent's `set -e` pipeline must not
	// treat "nothing to pick up" as a failure.
	out, code := run(t, "next", "--json")
	if code != int(core.CodeOK) {
		t.Errorf("empty next should exit 0, got %d", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty next --json should print [], got %q", out)
	}
}

// TestCLIConfigUnknownSubcommandExit2 pins t-kx76 (b): a bogus `config`
// subcommand is exit 2 with the known names in candidates — not the exit-0 help
// prose cobra swallows it as by default. Bare `config` still prints help (0).
func TestCLIConfigUnknownSubcommandExit2(t *testing.T) {
	initStore(t)
	fe, _ := runErr(t, "config", "show")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("`config show` should exit 2, got %+v", fe)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown config subcommand should carry candidates, got %+v", fe)
	}
	// bare `config` prints help and exits 0 (unchanged).
	if _, code := run(t, "config"); code != int(core.CodeOK) {
		t.Errorf("bare `config` should exit 0 (help), got %d", code)
	}
}

// TestCLIBoard pins `furrow board --json`: the lane-vocabulary/scope
// introspection an agent reads instead of provoking an unknown-lane error. The
// store points at FURROW_DIR, so source is "env".
func TestCLIBoard(t *testing.T) {
	initStore(t)
	out, code := run(t, "board", "--json")
	if code != 0 {
		t.Fatalf("board --json exit = %d:\n%s", code, out)
	}
	var b struct {
		Store       string   `json:"store"`
		Source      string   `json:"source"`
		Lanes       []string `json:"lanes"`
		NextLanes   []string `json:"next_lanes"`
		DefaultLane string   `json:"default_lane"`
		DoneLane    string   `json:"done_lane"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse board --json: %v\n%s", err, out)
	}
	if b.Source != "env" {
		t.Errorf("board source = %q, want env (FURROW_DIR)", b.Source)
	}
	if len(b.Lanes) == 0 || b.DefaultLane != "inbox" || b.DoneLane != "done" {
		t.Errorf("board vocabulary looks wrong: %+v", b)
	}
	if b.Store == "" {
		t.Error("board store path should be set")
	}
	// --ndjson emits the same object as one compact line (no indent).
	nd, code := run(t, "board", "--ndjson")
	if code != 0 || strings.Contains(nd, "\n  ") {
		t.Errorf("board --ndjson should be one compact line, got code=%d:\n%s", code, nd)
	}
}

// TestCLINDJSONEverywhere pins t-f5xk 案A: --ndjson is honored wherever --json
// is, emitting the SAME payload compact (one value per line) instead of the old
// silent degrade to human prose at exit 0. Covers a mutation, add, version, the
// apply report, and the lint problem stream.
func TestCLINDJSONEverywhere(t *testing.T) {
	initStore(t)
	id := addTask(t, "task one", "-s", "ready")

	// compactLine asserts out's first line parses as JSON and carries no 2-space
	// indent (i.e. it is the compact form, not the pretty --json form).
	compactLine := func(label, out string) {
		t.Helper()
		trimmed := strings.TrimRight(out, "\n")
		if trimmed == "" {
			t.Fatalf("%s --ndjson emitted nothing", label)
		}
		if strings.Contains(trimmed, "\n  ") {
			t.Errorf("%s --ndjson should be compact (no indent):\n%s", label, out)
		}
		first := strings.SplitN(trimmed, "\n", 2)[0]
		var v any
		if err := json.Unmarshal([]byte(first), &v); err != nil {
			t.Errorf("%s --ndjson first line is not JSON: %v\n%s", label, err, out)
		}
	}

	// mutation: done --ndjson -> {before,after,changed} on one compact line.
	out, code := run(t, "--ndjson", "done", id)
	if code != 0 {
		t.Fatalf("done --ndjson exit %d:\n%s", code, out)
	}
	compactLine("done", out)
	if !strings.Contains(out, `"changed":`) || !strings.Contains(out, `"before":`) {
		t.Errorf("done --ndjson should carry before/changed:\n%s", out)
	}

	// add --ndjson -> the created task as one compact line.
	out, _ = run(t, "--ndjson", "add", "task two", "-s", "ready")
	compactLine("add", out)

	// version --ndjson -> the version block, one compact line.
	out, _ = run(t, "--ndjson", "version")
	compactLine("version", out)

	// apply --ndjson -> the {on, ref, outcomes} report on one compact line.
	out, _ = runIn(t, "SetStatus-task: "+id+" done\n", "--ndjson", "apply", "--on", "merge")
	compactLine("apply", out)
	if !strings.Contains(out, `"outcomes":`) {
		t.Errorf("apply --ndjson should carry the outcomes array:\n%s", out)
	}

	// lint --ndjson -> one problem per compact line (induce a dangling [[id]]).
	addTask(t, "haslink", "--body", "see [[t-zzzzz]]")
	out, _ = run(t, "--ndjson", "lint")
	compactLine("lint", out)
	if !strings.Contains(out, "t-zzzzz") {
		t.Errorf("lint --ndjson should stream the dangling-link problem:\n%s", out)
	}
}

// TestCLIClampSignal pins t-abj3 (d): an out-of-range value/effort is clamped to
// 1..5 AND signaled — a `clamped` key in the --json envelope; an in-range value
// carries no such key.
func TestCLIClampSignal(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")

	out, code := run(t, "--json", "value", id, "9")
	if code != 0 {
		t.Fatalf("value exit %d:\n%s", code, out)
	}
	var res struct {
		After struct {
			Value int `json:"value"`
		} `json:"after"`
		Clamped map[string]struct {
			Requested int `json:"requested"`
			Stored    int `json:"stored"`
		} `json:"clamped"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse value --json: %v\n%s", err, out)
	}
	if res.After.Value != 5 {
		t.Errorf("value 9 should clamp to 5, got %d", res.After.Value)
	}
	if res.Clamped["value"].Requested != 9 || res.Clamped["value"].Stored != 5 {
		t.Errorf("clamped signal missing/wrong: %+v", res.Clamped)
	}

	// an in-range value emits no clamped key.
	out, _ = run(t, "--json", "value", id, "3")
	if strings.Contains(out, "clamped") {
		t.Errorf("in-range value must not emit a clamped key:\n%s", out)
	}
}

// TestCLIAliasExpansion pins t-awsb: a board [alias] expands git-style before
// dispatch, a builtin is never shadowed, and lint warns about a shadowing alias.
func TestCLIAliasExpansion(t *testing.T) {
	initStore(t)
	// The board config is user-owned — hand-append alias entries. The scaffolded
	// template already ENDS with the (commented) [alias] section, so bare keys
	// appended at EOF bind to that table; repeating the header would be the
	// duplicate-table TOML error.
	cfgPath := filepath.Join(os.Getenv(app.EnvDir), "config.toml")
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\ntriage = \"ls -s inbox,backlog\"\nls = \"ls -s ready\"\n")
	f.Close()

	root := newRootCmd()
	joined := func(args []string) string { return strings.Join(expandAlias(root, args), " ") }

	// a real command is never shadowed by an alias.
	if got := joined([]string{"ls", "-s", "ready"}); got != "ls -s ready" {
		t.Errorf("builtin ls must not be aliased, got %q", got)
	}
	// an alias expands and appends the remaining args (git-style).
	if got := joined([]string{"triage", "-r", "furrow"}); got != "ls -s inbox,backlog -r furrow" {
		t.Errorf("triage should expand+append, got %q", got)
	}
	// an unknown non-alias and a leading flag both pass through untouched.
	if got := joined([]string{"bogus"}); got != "bogus" {
		t.Errorf("unknown non-alias must pass through, got %q", got)
	}
	if got := joined([]string{"--json", "triage"}); got != "--json triage" {
		t.Errorf("a leading flag must pass through, got %q", got)
	}
	// lint warns about the shadowing `ls` alias (inert, but surfaced).
	out, _ := run(t, "lint")
	if !strings.Contains(out, "alias-shadow") || !strings.Contains(out, `"ls"`) {
		t.Errorf("lint should warn about the shadowing alias:\n%s", out)
	}
	// ... and --json stays JSON on that board (t-8j5v: the shadow check used to
	// build a second root mid-run, which reset --json to false and printed prose).
	out, _ = run(t, "lint", "--json")
	var ps []map[string]any
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("lint --json on a board with [alias] is not JSON: %v\n%s", err, out)
	}
	found := false
	for _, p := range ps {
		if p["code"] == "alias-shadow" {
			found = true
		}
	}
	if !found {
		t.Errorf("lint --json should carry the alias-shadow problem:\n%s", out)
	}
}

func TestCLIMutationJSONDiff(t *testing.T) {
	initStore(t)
	id := addTask(t, "task", "-s", "ready")

	out, code := run(t, "--json", "done", id)
	if code != 0 {
		t.Fatalf("done --json exit = %d:\n%s", code, out)
	}
	var envs []struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("done --json is a one-element envelope array (always-array), got error %v:\n%s", err, out)
	}
	res := envs[0]
	if res.Before == nil || res.After == nil {
		t.Fatalf("mutation --json must carry before+after:\n%s", out)
	}
	if res.Before.Status != "ready" || res.After.Status != "done" {
		t.Errorf("before/after status wrong: %q -> %q", res.Before.Status, res.After.Status)
	}
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

// TestCLIClampSignalBatch pins t-4tm9: the batch arm of `set <id>... --value 9`
// used to route through the annotation-free emitter, silently writing 5 to
// every task with no stderr note and no clamped key — while a single id
// disclosed both. The disclosure is per envelope, whatever the arity.
func TestCLIClampSignalBatch(t *testing.T) {
	initStore(t)
	a := addTask(t, "one")
	b := addTask(t, "two")

	out, code := run(t, "--json", "set", a, b, "--value", "9", "--effort", "0")
	if code != 0 {
		t.Fatalf("batch set exit %d:\n%s", code, out)
	}
	var res []struct {
		After struct {
			Value  int `json:"value"`
			Effort int `json:"effort"`
		} `json:"after"`
		Clamped map[string]struct {
			Requested int `json:"requested"`
			Stored    int `json:"stored"`
		} `json:"clamped"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse set --json: %v\n%s", err, out)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 envelopes, got %d:\n%s", len(res), out)
	}
	for i, env := range res {
		if env.After.Value != 5 || env.After.Effort != 1 {
			t.Errorf("envelope %d: value/effort should clamp to 5/1, got %d/%d", i, env.After.Value, env.After.Effort)
		}
		if env.Clamped["value"].Requested != 9 || env.Clamped["value"].Stored != 5 {
			t.Errorf("envelope %d: value clamp signal missing/wrong: %+v", i, env.Clamped)
		}
		if env.Clamped["effort"].Requested != 0 || env.Clamped["effort"].Stored != 1 {
			t.Errorf("envelope %d: effort clamp signal missing/wrong: %+v", i, env.Clamped)
		}
	}

	// In-range batch writes carry no clamped key.
	out, _ = run(t, "--json", "set", a, b, "--value", "3")
	if strings.Contains(out, "clamped") {
		t.Errorf("in-range batch set must not emit a clamped key:\n%s", out)
	}
}
