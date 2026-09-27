package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// yesterday/today/tomorrow as `--due` spellings, in the LOCAL zone the CLI reads
// them in. Dates, not instants, so a run at 23:59:59 does not flip a case.
// frozenNow is the instant the date-sensitive tests run at: a local noon, so
// the calendar day dayOffset writes into a --due and the day furrow reads
// today/overdue against (the machine zone, on a board that declares none)
// agree, and a test cannot straddle midnight between the two. Freeze the
// clock (freezeClock) before asking for a day near today; +30 is safe unfrozen.
var frozenNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.Local)

type frozenClock struct{}

func (frozenClock) Now() time.Time { return frozenNow }

func freezeClock(t *testing.T) {
	t.Helper()
	testClock = frozenClock{}
	t.Cleanup(func() { testClock = nil })
}

// dayOffset is the bare date n days from now, in the operator's zone: the
// frozen instant once freezeClock ran, the wall clock otherwise. A day within a
// week of today without a frozen clock is refused — that is the midnight flake.
func dayOffset(t *testing.T, n int) string {
	t.Helper()
	now := time.Now()
	if testClock != nil {
		now = testClock.Now()
	} else if n > -7 && n < 7 {
		t.Fatalf("dayOffset(%d) needs freezeClock(t): the date is evaluated again at read time", n)
	}
	return now.AddDate(0, 0, n).Format("2006-01-02")
}

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
	freezeClock(t)
	initStore(t)
	addTask(t, "promised today", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, 0))

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
	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, -1))
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
	if !slices.Contains(env.Changed, "due") {
		t.Errorf("changed = %v, want it to name due", env.Changed)
	}
	first, _ := env.After["due"].(string)
	if first == "" {
		t.Fatalf("after.due is empty: %v", env.After)
	}

	mustEnv("--due", "+1d") // the snooze
	if !slices.Contains(env.Changed, "due") {
		t.Errorf("a snooze must report due as changed: %v", env.Changed)
	}
	if second, _ := env.After["due"].(string); second == first {
		t.Errorf("snooze left due at %s", second)
	}

	mustEnv("--clear-due")
	if !slices.Contains(env.Changed, "due") {
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
	out, _ := run(t, "--json", "ls", "-r", "o/r")
	if strings.Contains(out, "bad date") {
		t.Errorf("a rejected --due created a task:\n%s", out)
	}
}

// brief LEADS with what has come due, and its JSON key is absent when nothing
// has — never null, which a reader would trip over.
func TestCLIBriefDueSection(t *testing.T) {
	freezeClock(t)
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

	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, -1))
	// An icebox task is PARKED: [due].ignore_lanes silences it here exactly as it
	// does in lint, which is why this one must not show up below.
	addTask(t, "promised today", "-s", "icebox", "-r", "o/r", "--due", dayOffset(t, 0))

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

// The board's repo scope must not hide a promise from the session-start read.
// Driven through real cobra from a scoped checkout: brief's due band and next's
// stderr hint both name a dated task belonging to ANOTHER repo, while `ls` — an
// ordinary scoped read — still hides it. That asymmetry IS the contract: the
// scope is derived from the cwd, nobody typed it, and `lint` (which has no repo
// filter at all) errors on the task regardless, so a scope that filtered the due
// band made brief print a section of 1 above a lint ride-along counting 2.
func TestCLIBriefDueIgnoresTheBoardRepoScope(t *testing.T) {
	freezeClock(t)
	checkout, _ := localBoardLayout(t, "default_repo = \"me/demo\"\n")
	if err := os.Chdir(checkout); err != nil {
		t.Fatal(err)
	}

	mine := addTask(t, "promised here", "-s", "waiting", "-r", "me/demo", "--due", dayOffset(t, -1))
	theirs := addTask(t, "promised elsewhere", "-s", "waiting", "-r", "me/other", "--due", dayOffset(t, -2))
	// `add` unions the board scope into repos, so strip it: this task must
	// belong to me/other ALONE, which is the case under test.
	if out, code := run(t, "repo", theirs, "--rm", "me/demo"); code != 0 {
		t.Fatalf("repo --rm exit %d:\n%s", code, out)
	}

	out, code := run(t, "--json", "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	var b struct {
		Due *struct {
			Overdue []struct {
				ID string `json:"id"`
			} `json:"overdue"`
		} `json:"due"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	if b.Due == nil || len(b.Due.Overdue) != 2 {
		t.Fatalf("brief due.overdue = %+v, want both %s (scoped) and %s (other repo)", b.Due, mine, theirs)
	}
	// Longest-overdue leads, across the repo boundary.
	if b.Due.Overdue[0].ID != theirs {
		t.Errorf("due band leader = %s, want the longest-overdue %s", b.Due.Overdue[0].ID, theirs)
	}

	// `next`'s hint counts the same set (its doc promises the two agree).
	_, stderr, code := runSplit(t, "--json", "next")
	if code != 0 {
		t.Fatalf("next exit = %d", code)
	}
	if !strings.Contains(stderr, "2 due (2 OVERDUE)") {
		t.Errorf("next's hint must count both, got: %q", stderr)
	}

	// An ordinary scoped read is UNCHANGED — only the due surfaces widen.
	out, code = run(t, "--json", "ls")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	if strings.Contains(out, theirs) {
		t.Errorf("ls must still obey the board scope:\n%s", out)
	}
	// …and an EXPLICITLY typed -r still narrows the due band: the reader chose
	// that one, unlike the scope.
	out, code = run(t, "--json", "brief", "-r", "me/demo")
	if code != 0 {
		t.Fatalf("brief -r exit = %d:\n%s", code, out)
	}
	if strings.Contains(out, theirs) {
		t.Errorf("an explicit -r must still narrow the due band:\n%s", out)
	}
}

// `next` notes the arrived dates on STDERR: the promised work usually sits in a
// lane next excludes, so it must be visible without polluting the array stdout.
func TestCLINextDueHintIsStderrOnly(t *testing.T) {
	freezeClock(t)
	initStore(t)
	addTask(t, "actionable", "-s", "ready", "-r", "o/r")
	late := addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, -1))

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
	freezeClock(t)
	initStore(t)
	addTask(t, "promised yesterday", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, -1))
	addTask(t, "promised later", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30))
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
	freezeClock(t)
	initStore(t)
	addTask(t, "promised yesterday", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, -1))

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
	freezeClock(t)
	initStore(t)
	open := addTask(t, "still open", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, -1))
	parked := addTask(t, "parked", "-s", "icebox", "-r", "o/r", "--due", dayOffset(t, -1))
	shipped := addTask(t, "shipped early", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, -1))
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
	freezeClock(t)
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
	id := addTask(t, "promised", "-s", "waiting", "-r", "o/r", "-e", e.ID, "--due", dayOffset(t, -1))

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

// --due-shift moves the due each task already carries; the human line echoes
// the instant it bound, and the JSON envelope's after.due is the before plus
// the offset — never an instant measured from now (that is --due's snooze).
func TestCLISetDueShift(t *testing.T) {
	initStore(t)
	id := addTask(t, "venue booking", "-s", "ready", "-r", "o/r", "--due", "2126-11-21T21:30")

	stdout := mustRun(t, "set", id, "--due-shift", "+7d")
	want := time.Date(2126, 11, 28, 21, 30, 0, 0, time.Local)
	if !strings.Contains(stdout, "due "+want.Format("2006-01-02 15:04")) {
		t.Errorf("the set line should echo the shifted due %s:\n%s", want.Format("2006-01-02 15:04"), stdout)
	}

	out, code := run(t, "--json", "set", id, "--due-shift", "-1w")
	if code != 0 {
		t.Fatalf("set --due-shift -1w exit = %d:\n%s", code, out)
	}
	var env struct {
		Before  map[string]any `json:"before"`
		After   map[string]any `json:"after"`
		Changed []string       `json:"changed"`
	}
	if err := json.Unmarshal(showOne(t, out), &env); err != nil {
		t.Fatalf("parse set --json: %v\n%s", err, out)
	}
	if !slices.Contains(env.Changed, "due") {
		t.Errorf("changed = %v, want it to name due", env.Changed)
	}
	before, _ := time.Parse(time.RFC3339, env.Before["due"].(string))
	after, _ := time.Parse(time.RFC3339, env.After["due"].(string))
	if !after.Equal(before.AddDate(0, 0, -7)) {
		t.Errorf("after.due = %s, want before (%s) minus a week", after, before)
	}
	if !after.Equal(time.Date(2126, 11, 21, 21, 30, 0, 0, time.Local)) {
		t.Errorf("the wall clock must survive the round trip: after.due = %s", after.In(time.Local))
	}

	// A plain --due still echoes what it bound: the bare date is the END of the day.
	stdout = mustRun(t, "set", id, "--due", "2126-12-01")
	if !strings.Contains(stdout, "due 2126-12-01 23:59") {
		t.Errorf("the set line should echo the bound due:\n%s", stdout)
	}
}

// The refusals: no due to shift (the id named, in details.undated), a shift
// beside --due/--clear-due, an empty or zero or absolute spelling — every one
// exit 2 with nothing written.
func TestCLISetDueShiftRefusals(t *testing.T) {
	initStore(t)
	dated := addTask(t, "dated", "-s", "ready", "-r", "o/r", "--due", "2126-11-21")
	undated := addTask(t, "undated", "-s", "ready", "-r", "o/r")
	stamp := func(id string) string {
		var task struct {
			Updated string `json:"updated"`
			Due     string `json:"due"`
		}
		out := mustRun(t, "--json", "show", id)
		if err := json.Unmarshal(showOne(t, out), &task); err != nil {
			t.Fatalf("parse show: %v\n%s", err, out)
		}
		return task.Updated + " " + task.Due
	}
	wasDated, wasUndated := stamp(dated), stamp(undated)

	fe, out := runErr(t, "set", undated, "--due-shift", "+1d")
	if fe == nil || fe.Code != 2 {
		t.Fatalf("shifting an undated task should be exit 2, got %v:\n%s", fe, out)
	}
	if fe.Subject != undated {
		t.Errorf("subject = %q, want the undated task", fe.Subject)
	}
	details, _ := fe.Details.(map[string]any)
	if got, _ := details["undated"].([]string); !slices.Equal(got, []string{undated}) {
		t.Errorf("details.undated = %v, want [%s]", details["undated"], undated)
	}

	// The batch is all-or-nothing: the dated task is not shifted when the
	// undated one refuses, and the refusal names every undated id.
	fe, out = runErr(t, "set", dated, undated, "--due-shift", "+1d")
	if fe == nil || fe.Code != 2 {
		t.Fatalf("a batch with an undated task should be exit 2, got %v:\n%s", fe, out)
	}
	if fe.Subject != "" {
		t.Errorf("a batch refusal has no single subject, got %q", fe.Subject)
	}

	for _, args := range [][]string{
		{"set", dated, "--due-shift", "+1d", "--due", "2126-01-01"},
		{"set", dated, "--due-shift", "+1d", "--clear-due"},
		{"set", dated, "--due-shift", ""},
		{"set", dated, "--due-shift", "+0d"},
		{"set", dated, "--due-shift", "2126-01-01"},
		{"set", dated, "--due-shift", "7d"},
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
	if got := stamp(dated); got != wasDated {
		t.Errorf("a refused shift wrote the dated task: %s -> %s", wasDated, got)
	}
	if got := stamp(undated); got != wasUndated {
		t.Errorf("a refused shift wrote the undated task: %s -> %s", wasUndated, got)
	}
}

// A -q selection previews the shift as old → new per row and names the rows
// the apply would refuse; `-q 'id:…'` is how an explicit id list gets that
// preview. --yes over dated rows applies each from its own stamp.
func TestCLISetDueShiftPreview(t *testing.T) {
	initStore(t)
	a := addTask(t, "venue booking", "-s", "ready", "-r", "o/r", "--due", "2126-11-21T21:30")
	b := addTask(t, "menu tasting", "-s", "ready", "-r", "o/r", "--due", "2126-11-20")
	c := addTask(t, "undated", "-s", "ready", "-r", "o/r")

	// With an undated row in the selection the preview must not promise the
	// write: the count line says 0 will be written, the row is marked, and the
	// closing line is the remedy rather than the re-run.
	sel := "id:" + a + "," + b + "," + c
	stdout, stderr := mustSplit(t, "set", "-q", sel, "--due-shift", "+7d")
	for _, want := range []string{
		"would set 0 of 3 task(s): 1 cannot be shifted, and the write is all-or-nothing",
		"due 2126-11-21 21:30  → 2126-11-28 21:30",
		"due 2126-11-20 23:59  → 2126-11-27 23:59",
		c + "  [ready] undated  no due — refused",
		"narrow the selection to the rows that can be shifted (-q has:due) — --yes would exit 2",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview lacks %q:\n%s%s", want, stdout, stderr)
		}
	}
	if strings.Contains(stdout, "re-run with --yes") {
		t.Errorf("a preview the apply would refuse must not invite the re-run:\n%s", stdout)
	}
	out := mustRun(t, "--json", "show", a)
	var shown struct {
		Due string `json:"due"`
	}
	if err := json.Unmarshal(showOne(t, out), &shown); err != nil {
		t.Fatalf("parse show --json: %v\n%s", err, out)
	}
	if got, _ := time.Parse(time.RFC3339, shown.Due); !got.Equal(time.Date(2126, 11, 21, 21, 30, 0, 0, time.Local)) {
		t.Errorf("a preview must not write: due = %s", shown.Due)
	}

	// JSON keeps the documented object shape and names the refused ids.
	out = mustRun(t, "--json", "set", "-q", sel, "--due-shift", "+7d")
	var preview struct {
		DryRun  bool             `json:"dry_run"`
		Tasks   []map[string]any `json:"tasks"`
		Refused []string         `json:"refused"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil || !preview.DryRun || len(preview.Tasks) != 3 {
		t.Errorf("JSON preview = %s (err %v), want {dry_run: true, tasks: [3], refused}", out, err)
	}
	if !slices.Equal(preview.Refused, []string{c}) {
		t.Errorf("JSON preview refused = %v, want [%s]", preview.Refused, c)
	}

	// A landing past year 9999 is the other refusal, and the preview says so
	// too instead of drawing an arrow to a date no due can hold.
	// A morning clock, so the stored UTC instant stays inside year 9999 in
	// every machine zone the suite may run in; the shift still lands past it.
	far := addTask(t, "far", "-s", "ready", "-r", "o/r", "--due", "9999-12-31T09:00")
	stdout = mustRun(t, "set", "-q", "id:"+far, "--due-shift", "+1d")
	if !strings.Contains(stdout, "lands outside years 1..9999 — refused") || strings.Contains(stdout, "re-run with --yes") {
		t.Errorf("an out-of-range landing should be marked refused:\n%s", stdout)
	}
	if fe, _ := runErr(t, "set", far, "--due-shift", "+1d"); fe == nil || fe.Code != 2 {
		t.Errorf("an out-of-range landing should be exit 2 validation, got %v", fe)
	}

	// Narrowed to dated rows the preview is the ordinary one: a count, the
	// re-run hint, no undated key.
	stdout = mustRun(t, "set", "-q", sel+" has:due", "--due-shift", "+7d")
	if !strings.Contains(stdout, "would set 2 task(s)") || !strings.Contains(stdout, "re-run with --yes") {
		t.Errorf("a dated-only preview should count and invite the re-run:\n%s", stdout)
	}
	out = mustRun(t, "--json", "set", "-q", sel+" has:due", "--due-shift", "+7d")
	if strings.Contains(out, `"refused"`) {
		t.Errorf("a dated-only JSON preview must not carry refused:\n%s", out)
	}

	// The apply refuses the whole selection while an undated row is in it…
	fe, _ := runErr(t, "set", "-q", sel, "--due-shift", "+7d", "--yes")
	if fe == nil || fe.Code != 2 {
		t.Fatalf("--yes over an undated row should be exit 2, got %v", fe)
	}
	// …and narrowed to dated rows, shifts each from its own stamp.
	stdout = mustRun(t, "set", "-q", sel+" has:due", "--due-shift", "+7d", "--yes")
	if !strings.Contains(stdout, "due 2126-11-28 21:30") || !strings.Contains(stdout, "due 2126-11-27 23:59") {
		t.Errorf("the applied lines should echo each shifted due:\n%s", stdout)
	}
	out = mustRun(t, "--json", "show", c)
	if strings.Contains(out, `"due"`) {
		t.Errorf("the undated task must stay undated:\n%s", out)
	}
}
