package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// runSplit drives the real root command with stdout and stderr captured
// SEPARATELY — the only way to assert that a hint went to stderr while stdout
// stayed a parseable array.
func runSplit(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&so)
	root.SetErr(&se)
	err := root.Execute()
	if err == nil {
		return so.String(), se.String(), 0
	}
	fe := core.AsError(err)
	if fe == nil {
		return so.String(), se.String(), int(core.CodeValidation)
	}
	return so.String(), se.String(), int(fe.Code)
}

// yesterday/today/tomorrow as `--due` spellings, in the LOCAL zone the CLI reads
// them in. Dates, not instants, so a run at 23:59:59 does not flip a case.
func dayOffset(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }

func TestCLIDueRoundTrip(t *testing.T) {
	initStore(t)
	id := addTask(t, "check the nightly run", "-s", "waiting", "-r", "o/r", "--due", "2126-08-04T10:30")

	out, code := run(t, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	var task struct {
		Due string `json:"due"`
	}
	if err := json.Unmarshal(showOne(t, out), &task); err != nil {
		t.Fatalf("parse show --json: %v\n%s", err, out)
	}
	// The wire form is the same UTC RFC3339 every other timestamp uses.
	got, err := time.Parse(time.RFC3339, task.Due)
	if err != nil {
		t.Fatalf("due %q is not RFC3339: %v", task.Due, err)
	}
	want := time.Date(2126, 8, 4, 10, 30, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("due = %s, want %s (the local wall clock typed on the flag)", got, want)
	}

	// The human detail block renders it local + offset, like created/updated.
	out, code = run(t, "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "due:") || !strings.Contains(out, want.Format("2006-01-02 15:04")) {
		t.Errorf("human show should carry the due line:\n%s", out)
	}

	// A task with no date carries no key and no line — never a 0001-01-01.
	plain := addTask(t, "undated", "-r", "o/r")
	out, _ = run(t, "--json", "show", plain)
	if strings.Contains(out, "\"due\"") {
		t.Errorf("an undated task must carry no due key:\n%s", out)
	}
	out, _ = run(t, "show", plain)
	if strings.Contains(out, "due:") || strings.Contains(out, "0001-01-01") {
		t.Errorf("an undated task must render no due line:\n%s", out)
	}
}

// A bare date is the WHOLE day: a task promised for today is not overdue at
// 00:01 — the rule the whole feature turns on.
func TestCLIDueDateOnlyIsEndOfDay(t *testing.T) {
	initStore(t)
	addTask(t, "promised today", "-s", "waiting", "-r", "o/r", "--due", dayOffset(0))

	out, code := run(t, "lint")
	if code != 0 {
		t.Fatalf("lint on a task due TODAY should be exit 0 (warn only), got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "due-today") {
		t.Errorf("lint should warn due-today:\n%s", out)
	}
	if strings.Contains(out, "due-overdue") {
		t.Errorf("a task due today must not be overdue:\n%s", out)
	}

	// Yesterday, on the other hand, is an ERROR — lint exits 2.
	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(-1))
	out, code = run(t, "lint")
	if code == 0 {
		t.Fatalf("lint with an overdue task should be non-zero:\n%s", out)
	}
	if !strings.Contains(out, "due-overdue") || !strings.Contains(out, late) {
		t.Errorf("lint should ERROR due-overdue on %s:\n%s", late, out)
	}
}

// The set path: re-date, snooze, and clear — each naming `due` in the envelope's
// changed list, or a headless caller reads a successful write as a no-op.
func TestCLISetDueEnvelope(t *testing.T) {
	initStore(t)
	id := addTask(t, "dated", "-s", "ready", "-r", "o/r")

	var env struct {
		After   map[string]any `json:"after"`
		Changed []string       `json:"changed"`
	}
	mustEnv := func(args ...string) {
		t.Helper()
		out, code := run(t, append([]string{"--json", "set", id}, args...)...)
		if code != 0 {
			t.Fatalf("set %v exit = %d:\n%s", args, code, out)
		}
		env = struct {
			After   map[string]any `json:"after"`
			Changed []string       `json:"changed"`
		}{}
		if err := json.Unmarshal(showOne(t, out), &env); err != nil {
			t.Fatalf("parse set --json: %v\n%s", err, out)
		}
	}

	mustEnv("--due", "2126-08-04")
	if !contains(env.Changed, "due") {
		t.Errorf("changed = %v, want it to name due", env.Changed)
	}
	first, _ := env.After["due"].(string)
	if first == "" {
		t.Fatalf("after.due is empty: %v", env.After)
	}

	mustEnv("--due", "+1d") // the snooze
	if !contains(env.Changed, "due") {
		t.Errorf("a snooze must report due as changed: %v", env.Changed)
	}
	if second, _ := env.After["due"].(string); second == first {
		t.Errorf("snooze left due at %s", second)
	}

	mustEnv("--clear-due")
	if !contains(env.Changed, "due") {
		t.Errorf("--clear-due must report due as changed: %v", env.Changed)
	}
	if _, ok := env.After["due"]; ok {
		t.Errorf("after.due should be absent once cleared: %v", env.After)
	}
}

// An empty --due is exit 2, never a silent clear: a caller passing "$WHEN" with
// an unset variable has a bug, and --clear-due already spells the clear.
func TestCLIDueRejectsBadSpellings(t *testing.T) {
	initStore(t)
	id := addTask(t, "x", "-s", "ready", "-r", "o/r")

	for _, args := range [][]string{
		{"add", "bad date", "--due", "someday"},
		{"add", "bad date", "--due", "2026-13-45"},
		{"set", id, "--due", "someday"},
		{"set", id, "--due", ""},
	} {
		fe, out := runErr(t, args...)
		if fe == nil {
			t.Errorf("%v should have failed:\n%s", args, out)
			continue
		}
		if fe.Code != 2 {
			t.Errorf("%v exit = %d, want 2 (validation)", args, fe.Code)
		}
	}
	// …and nothing was created by the rejected adds.
	out, _ := run(t, "--json", "ls", "-r", "o/r")
	if strings.Contains(out, "bad date") {
		t.Errorf("a rejected --due created a task:\n%s", out)
	}
}

// brief LEADS with what has come due, and its JSON key is absent when nothing
// has — never null, which a reader would trip over.
func TestCLIBriefDueSection(t *testing.T) {
	initStore(t)

	out, code := run(t, "--json", "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	if strings.Contains(out, "\"due\"") {
		t.Errorf("a board with no dates must carry no due key:\n%s", out)
	}
	if strings.Contains(out, "null") {
		t.Errorf("brief JSON must never contain null:\n%s", out)
	}

	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(-1))
	// An icebox task is PARKED: [due].ignore_lanes silences it here exactly as it
	// does in lint, which is why this one must not show up below.
	addTask(t, "promised today", "-s", "icebox", "-r", "o/r", "--due", dayOffset(0))

	out, code = run(t, "--json", "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	var b struct {
		Due *struct {
			Overdue []struct {
				ID string `json:"id"`
			} `json:"overdue"`
			Today []struct {
				ID string `json:"id"`
			} `json:"today"`
		} `json:"due"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	if b.Due == nil || len(b.Due.Overdue) != 1 || b.Due.Overdue[0].ID != late {
		t.Fatalf("brief due.overdue = %+v, want [%s]", b.Due, late)
	}
	if len(b.Due.Today) != 0 {
		t.Errorf("an icebox task must not appear in brief's due section: %+v", b.Due.Today)
	}

	// Human mode: the section leads, above the next block.
	out, code = run(t, "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	di, ni := strings.Index(out, "due ("), strings.Index(out, "next (")
	if di < 0 || ni < 0 || di > ni {
		t.Errorf("the due section must lead the dashboard:\n%s", out)
	}
	if !strings.Contains(out, late) || !strings.Contains(out, "overdue") {
		t.Errorf("human brief should name the overdue task:\n%s", out)
	}
}

// `next` notes the arrived dates on STDERR: the promised work usually sits in a
// lane next excludes, so it must be visible without polluting the array stdout.
func TestCLINextDueHintIsStderrOnly(t *testing.T) {
	initStore(t)
	addTask(t, "actionable", "-s", "ready", "-r", "o/r")
	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(-1))

	stdout, stderr, code := runSplit(t, "--json", "next")
	if code != 0 {
		t.Fatalf("next exit = %d:\n%s\n%s", code, stdout, stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("next --json stdout must stay a clean array: %v\n%s", err, stdout)
	}
	for _, r := range rows {
		if r["id"] == late {
			t.Errorf("next must not hand out the waiting task %s", late)
		}
	}
	if !strings.Contains(stderr, "OVERDUE") {
		t.Errorf("next should note the overdue promise on stderr, got:\n%s", stderr)
	}
}

// `ls` marks a dated row in the title cell, and marks a passed one differently —
// the at-a-glance half of the same signal.
func TestCLILsShowsDue(t *testing.T) {
	initStore(t)
	addTask(t, "promised yesterday", "-s", "ready", "-r", "o/r", "--due", dayOffset(-1))
	addTask(t, "promised later", "-s", "ready", "-r", "o/r", "--due", dayOffset(30))
	addTask(t, "undated", "-s", "ready", "-r", "o/r")

	out, code := run(t, "ls", "-r", "o/r")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "promised yesterday"):
			if !strings.Contains(line, "overdue ") {
				t.Errorf("the passed row should read overdue:\n%s", line)
			}
		case strings.Contains(line, "promised later"):
			if !strings.Contains(line, "due ") || strings.Contains(line, "overdue") {
				t.Errorf("the future row should read due, not overdue:\n%s", line)
			}
		case strings.Contains(line, "undated"):
			if strings.Contains(line, "due") {
				t.Errorf("an undated row must carry no due tag:\n%s", line)
			}
		}
	}
}

// The lint codes are real registry members, so `--code`/`--exclude-code` accept
// them — the contract that makes an ignore rule spellable at all.
func TestCLILintDueCodeFiltering(t *testing.T) {
	initStore(t)
	addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(-1))

	out, code := run(t, "lint", "--code", "due-overdue")
	if code == 0 || !strings.Contains(out, "due-overdue") {
		t.Errorf("lint --code due-overdue should report the finding (exit %d):\n%s", code, out)
	}
	out, code = run(t, "lint", "--exclude-code", "due-overdue")
	if code != 0 {
		t.Errorf("excluding the only error should exit 0, got %d:\n%s", code, out)
	}
	if fe, _ := runErr(t, "lint", "--code", "due-overdu"); fe == nil || fe.Code != 2 {
		t.Error("a typo'd code must still be exit 2 with candidates")
	}
}

// `add --due ""` is exit 2, exactly like `set --due ""`: silently creating a
// DATELESS task would drop the promise where nothing could report it again.
func TestCLIAddRejectsEmptyDue(t *testing.T) {
	initStore(t)
	fe, out := runErr(t, "add", "no date", "--due", "")
	if fe == nil || fe.Code != 2 {
		t.Fatalf("add --due '' should be exit 2, got %v:\n%s", fe, out)
	}
	if listing, _ := run(t, "ls", "--json"); strings.Contains(listing, "no date") {
		t.Errorf("the rejected add still created a task:\n%s", listing)
	}
}

// What `ls`/`show` may CALL overdue must match what lint and brief report: a
// task in an exempt lane still shows its date, but never an alarm. The
// finished-early case is the sharp one — the promise was kept.
func TestCLIExemptLanesAreNeverRenderedOverdue(t *testing.T) {
	initStore(t)
	open := addTask(t, "still open", "-s", "ready", "-r", "o/r", "--due", dayOffset(-1))
	parked := addTask(t, "parked", "-s", "icebox", "-r", "o/r", "--due", dayOffset(-1))
	shipped := addTask(t, "shipped early", "-s", "ready", "-r", "o/r", "--due", dayOffset(-1))
	if out, code := run(t, "done", shipped); code != 0 {
		t.Fatalf("done exit = %d:\n%s", code, out)
	}

	out, code := run(t, "ls", "-r", "o/r", "-s", "ready,icebox,done")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, open):
			if !strings.Contains(line, "overdue ") {
				t.Errorf("the open overdue row lost its marker:\n%s", line)
			}
		case strings.Contains(line, parked), strings.Contains(line, shipped):
			if strings.Contains(line, "overdue") {
				t.Errorf("an exempt lane must not read as overdue (lint is silent about it):\n%s", line)
			}
			if !strings.Contains(line, "due ") {
				t.Errorf("an exempt row should still show its date:\n%s", line)
			}
		}
	}
	// `show` renders the same judgement.
	if detail, _ := run(t, "show", shipped); strings.Contains(detail, "(overdue)") {
		t.Errorf("show marked a task finished before its date overdue:\n%s", detail)
	}
	// …and lint agrees, which is the point of routing both through one policy.
	if lint, code := run(t, "lint"); code == 0 || !strings.Contains(lint, open) || strings.Contains(lint, parked) || strings.Contains(lint, shipped) {
		t.Errorf("lint should name only the open one (exit %d):\n%s", code, lint)
	}
}

// `--tree` is the same matched rows regrouped, so it must not be the one view
// where a promise disappears.
func TestCLITreeShowsDue(t *testing.T) {
	initStore(t)
	epic, code := run(t, "--json", "epic", "add", "ops", "-r", "o/r")
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, epic)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(epic), &e); err != nil {
		t.Fatalf("parse epic add: %v\n%s", err, epic)
	}
	id := addTask(t, "promised", "-s", "waiting", "-r", "o/r", "-e", e.ID, "--due", dayOffset(-1))

	out, code := run(t, "ls", "-r", "o/r", "--tree")
	if code != 0 {
		t.Fatalf("ls --tree exit = %d:\n%s", code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) && !strings.Contains(line, "overdue ") {
			t.Errorf("the tree row dropped the due tag the flat row carries:\n%s", line)
		}
	}
}
