package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCLIArchiveRepoScope(t *testing.T) {
	// Seed two aged done tasks in different repos directly through the store
	// (old Closed makes them archivable under the default age guard), then drive
	// the CLI with -r to fold only one repo's done.
	dir := t.TempDir()
	ia, err := app.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-aaa1", Title: "a done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/a"}, Body: core.BodyPath("t-aaa1")})
	idx.Add(core.Task{ID: "t-bbb1", Title: "b done", Status: "done", Priority: 110,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/b"}, Body: core.BodyPath("t-bbb1")})
	ia.Store.Save(idx)
	ia.Store.SaveBody("t-aaa1", "# a\n")
	ia.Store.SaveBody("t-bbb1", "# b\n")
	t.Setenv(app.EnvDir, filepath.Join(dir, app.DirName))

	// -r owner/a --yes moves only owner/a; JSON records the repo scope.
	out, code := run(t, "--json", "archive", "-r", "owner/a", "--yes")
	if code != 0 {
		t.Fatalf("archive -r exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "t-aaa1") {
		t.Errorf("owner/a task should be archived:\n%s", out)
	}
	if strings.Contains(out, "t-bbb1") {
		t.Errorf("owner/b task must NOT be archived (out of scope):\n%s", out)
	}
	if !strings.Contains(out, `"repos": [`) || !strings.Contains(out, "owner/a") {
		t.Errorf("archive --json should record the repo scope in a repos array:\n%s", out)
	}

	// An unresolvable short repo name is a validation error (exit 2), same
	// contract as the read commands — never a silent whole-board sweep.
	if _, code := run(t, "archive", "-r", "ghost", "--yes"); code != int(core.CodeValidation) {
		t.Errorf("unresolvable -r should exit 2, got %d", code)
	}
}

// TestCLIListMultiValueOR pins the -s/-l comma = OR plumbing end-to-end through
// the real cobra flags. The OR semantics rely on the comma reaching the app layer
// intact: both -s and -l are StringArrayVar (which, unlike StringSliceVar, does
// NOT split on commas — joinOrFilter re-joins the repeats and the single
// app-layer split stays the one parser). If a maintainer ever switched either to
// StringSliceVarP or added a CLI-layer split, cobra would consume the comma and
// this test would fail where the app-level TestListMultiValueOR could not see it.
func TestCLIListMultiValueOR(t *testing.T) {
	initStore(t)
	addTask(t, "i-bug", "-s", "inbox", "-l", "bug")
	addTask(t, "b-urgent", "-s", "backlog", "-l", "urgent")
	addTask(t, "r-bug", "-s", "ready", "-l", "bug")

	// -s comma = OR within the field: inbox,backlog matches the first two.
	out, code := run(t, "--ndjson", "ls", "-s", "inbox,backlog")
	if code != 0 {
		t.Fatalf("ls -s exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "i-bug") || !strings.Contains(out, "b-urgent") || strings.Contains(out, "r-bug") {
		t.Errorf("`ls -s inbox,backlog` should OR to i-bug + b-urgent only:\n%s", out)
	}

	// -l comma = OR: bug,urgent matches all three.
	out, _ = run(t, "--ndjson", "ls", "-l", "bug,urgent")
	for _, want := range []string{"i-bug", "b-urgent", "r-bug"} {
		if !strings.Contains(out, want) {
			t.Errorf("`ls -l bug,urgent` should include %q:\n%s", want, out)
		}
	}

	// flags still AND across fields: (inbox|backlog) AND label bug -> i-bug only.
	out, _ = run(t, "--ndjson", "ls", "-s", "inbox,backlog", "-l", "bug")
	if !strings.Contains(out, "i-bug") || strings.Contains(out, "b-urgent") || strings.Contains(out, "r-bug") {
		t.Errorf("`ls -s inbox,backlog -l bug` should AND to i-bug only:\n%s", out)
	}
}

// TestCLILsStatusRepeatUnion pins t-1bwc: a REPEATED -s (as opposed to a single
// comma-joined -s) unions its lanes instead of silently keeping only the last —
// the "silent last-wins" trap of the old StringVarP flag. It is order-independent,
// composes with a comma within one -s, and is shared by every -s command (ls +
// search proven here; stats reuses the same joinOrFilter helper).
func TestCLILsStatusRepeatUnion(t *testing.T) {
	initStore(t)
	addTask(t, "i-task", "-s", "inbox")
	addTask(t, "b-task", "-s", "backlog")
	addTask(t, "r-task", "-s", "ready")

	// repeated -s unions (the fix): -s inbox -s backlog matches the first two, not
	// just the last (which was the pre-fix behavior: backlog only).
	out, code := run(t, "--ndjson", "ls", "-s", "inbox", "-s", "backlog")
	if code != 0 {
		t.Fatalf("ls -s inbox -s backlog exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("repeated -s should OR to i-task + b-task only:\n%s", out)
	}

	// order-independent: the reversed spelling must union identically (the old bug
	// was order-sensitive — the LAST -s won).
	out, _ = run(t, "--ndjson", "ls", "-s", "backlog", "-s", "inbox")
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("reversed repeated -s should union identically:\n%s", out)
	}

	// comma and repeat compose: -s inbox,backlog -s ready spans all three.
	out, _ = run(t, "--ndjson", "ls", "-s", "inbox,backlog", "-s", "ready")
	for _, want := range []string{"i-task", "b-task", "r-task"} {
		if !strings.Contains(out, want) {
			t.Errorf("comma+repeat -s should include %q:\n%s", want, out)
		}
	}

	// an unknown token in ANY repeat still fails fast (exit 2 + candidates), the
	// same closed-vocabulary contract a single -s honors.
	fe, _ := runErr(t, "ls", "-s", "inbox", "-s", "ghost")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("a bad lane in a repeated -s should exit 2 with candidates, got %+v", fe)
	}

	// the fix is shared by every -s command via joinOrFilter: search unions too.
	out, _ = run(t, "--ndjson", "search", "task", "-s", "inbox", "-s", "backlog")
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("search should honor repeated -s the same way:\n%s", out)
	}
}

// TestCLILabelRepeatUnion pins t-k1sr: a REPEATED -l unions its tags instead of
// silently keeping only the last — the same "silent last-wins" trap #128 fixed
// for -s, now closed for -l. It is order-independent, composes with a comma
// within one -l, is shared by every -l command (ls/next/search proven here;
// revisit/stats reuse the same joinOrFilter), and does NOT regress the
// single-token DidYouMeanRepo guard (the -l-specific risk of comma-joining).
func TestCLILabelRepeatUnion(t *testing.T) {
	initStore(t)
	addTask(t, "wip bug", "-s", "ready", "-l", "bug")
	addTask(t, "wip urgent", "-s", "ready", "-l", "urgent")
	addTask(t, "wip chore", "-s", "ready", "-l", "chore")

	// repeated -l unions (the fix): -l bug -l urgent matches the first two, not
	// just the last (the pre-fix behavior was chore/urgent-only, order-dependent).
	out, code := run(t, "--ndjson", "ls", "-l", "bug", "-l", "urgent")
	if code != 0 {
		t.Fatalf("ls -l bug -l urgent exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("repeated -l should OR to bug + urgent only:\n%s", out)
	}

	// order-independent: the old bug kept whichever -l came LAST.
	out, _ = run(t, "--ndjson", "ls", "-l", "urgent", "-l", "bug")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("reversed repeated -l should union identically:\n%s", out)
	}

	// comma and repeat compose: -l bug,urgent -l chore spans all three.
	out, _ = run(t, "--ndjson", "ls", "-l", "bug,urgent", "-l", "chore")
	for _, want := range []string{"wip bug", "wip urgent", "wip chore"} {
		if !strings.Contains(out, want) {
			t.Errorf("comma+repeat -l should include %q:\n%s", want, out)
		}
	}

	// shared by every -l command via joinOrFilter: next (a different scopedQuery
	// consumer than ls) unions too — all three are in a next lane with no deps.
	out, _ = run(t, "--ndjson", "next", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("next should honor repeated -l the same way:\n%s", out)
	}
	// and search (a positional term + -l filter).
	out, _ = run(t, "--ndjson", "search", "wip", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("search should honor repeated -l the same way:\n%s", out)
	}
	// and revisit (all three are open with unset estimates, so all surface; the -l
	// repeat must narrow to bug+urgent, not last-wins to one).
	out, _ = run(t, "--ndjson", "revisit", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("revisit should honor repeated -l the same way:\n%s", out)
	}
	// and stats: the aggregate total counts the union (2), not a single -l (1).
	out, code = run(t, "--json", "stats", "-l", "bug", "-l", "urgent")
	if code != 0 {
		t.Fatalf("stats -l bug -l urgent exit = %d:\n%s", code, out)
	}
	var st struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("parse stats --json: %v\n%s", err, out)
	}
	if st.Total != 2 {
		t.Errorf("stats repeated -l should total the union (2), got %d:\n%s", st.Total, out)
	}

	// DidYouMeanRepo must NOT regress. Seed a repo-scoped task so a label that
	// happens to name that repo triggers the did-you-mean guard.
	addTask(t, "in webapp", "-s", "ready", "-r", "owner/webapp")
	// a SINGLE -l naming a repo (and matching no tag) still exits 2 pointing at -r.
	fe, _ := runErr(t, "ls", "-l", "webapp")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("single -l naming a repo should still exit 2 with candidates, got %+v", fe)
	}
	// a REPEATED -l that matches nothing must NOT misfire the guard: the joined
	// "webapp,ghost" resolves to no repo, so it is a clean empty result (exit 0).
	out, code = run(t, "--ndjson", "ls", "-l", "webapp", "-l", "ghost")
	if code != 0 {
		t.Errorf("repeated -l matching nothing should be a clean empty result, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("repeated -l webapp -l ghost should match nothing:\n%s", out)
	}
}

// TestCLILsUnknownLaneExit2 pins that a typo'd -s lane fails fast (exit 2)
// carrying the configured lanes in the error's candidates array — not a silent
// [] exit 0 — even when a comma filter mixes a known and an unknown token. This
// is the read-side symmetry with move/add (t-bec7 案B).
func TestCLILsUnknownLaneExit2(t *testing.T) {
	initStore(t)
	addTask(t, "i", "-s", "inbox")

	for _, bad := range []string{"in_progress", "inbox,ghost"} {
		fe, _ := runErr(t, "ls", "-s", bad)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("ls -s %q should exit 2, got %+v", bad, fe)
		}
		if len(fe.Candidates) == 0 {
			t.Errorf("ls -s %q error should carry lane candidates, got %+v", bad, fe)
		}
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
	if err := json.Unmarshal([]byte(out), &res); err != nil {
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
	// no change is a validation error.
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
	// remove both.
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

// TestCLIArchiveByID pins t-kx76 (e): `archive <id> --yes` retires exactly that
// task; a non-done id is exit 2; --older-than/-r can't combine with an id list.
func TestCLIArchiveByID(t *testing.T) {
	initStore(t)
	doneID := addTask(t, "finished", "-s", "ready")
	run(t, "done", doneID)
	openID := addTask(t, "in flight", "-s", "ready")

	// a non-done id is refused (exit 2) — you can't strand live work in archive/.
	if _, code := run(t, "archive", openID, "--yes"); code != int(core.CodeValidation) {
		t.Errorf("archiving a non-done id should exit 2, got %d", code)
	}
	// combining an id with the sweep knobs is refused.
	if _, code := run(t, "archive", doneID, "--older-than", "0", "--yes"); code != int(core.CodeValidation) {
		t.Errorf("archive <id> --older-than should exit 2, got %d", code)
	}
	// retire the done one by id.
	out, code := run(t, "--json", "archive", doneID, "--yes")
	if code != 0 {
		t.Fatalf("archive by id exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, doneID) {
		t.Errorf("archive --json should list the retired id:\n%s", out)
	}
	// it's gone from the hot store now.
	if _, code := run(t, "show", doneID); code != int(core.CodeNotFound) {
		t.Errorf("archived task should be not-found in the hot store, got %d", code)
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
	if err := json.Unmarshal([]byte(out), &task); err != nil {
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
	// The board config is user-owned — hand-append an [alias] table.
	cfgPath := filepath.Join(os.Getenv(app.EnvDir), "config.toml")
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n[alias]\ntriage = \"ls -s inbox,backlog\"\nls = \"ls -s ready\"\n")
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
	// An empty --add keeps its prior "flag unset" meaning (falls through to the
	// toggle path, which needs an index) rather than appending a blank bullet.
	if _, code := run(t, "check", id, "--add", ""); code != int(core.CodeValidation) {
		t.Errorf(`check --add "" with no index should require an index (exit 2), got %d`, code)
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
