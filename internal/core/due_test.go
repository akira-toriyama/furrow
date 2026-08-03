package core

import (
	"testing"
	"time"
)

// jst is a fixed +09:00 zone: the operator zone every day-boundary case below is
// read in. A FIXED zone, never time.Local, so the assertions mean the same thing
// on a CI runner in UTC as on the machine this was written on.
var jst = time.FixedZone("JST", 9*60*60)

func due(t time.Time) *Task { return &Task{ID: "t-due01", Status: "waiting", Due: &t} }

// The state machine, read in the OPERATOR's zone. The clock is UTC (which is
// what core.SystemClock hands the app), so every row here is also a regression
// test for taking the day off the clock instead of off loc: at 2026-08-03
// 23:00Z it is already the 4th in JST, and a stamp late on the 4th JST is
// "today" — while its UTC day is the 4th too, the MORNING case (now = 2026-08-04
// 01:00 JST = 2026-08-03 16:00Z) is the one that separates the two readings.
func TestDueStateOf(t *testing.T) {
	// 2026-08-04 23:59:59 +09:00 — what `--due 2026-08-04` binds to in JST.
	endOf4th := time.Date(2026, 8, 4, 23, 59, 59, 0, jst).UTC()
	cases := []struct {
		name string
		now  time.Time
		due  time.Time
		want string
	}{
		{"morning of the promised day (JST) is TODAY, though UTC still says the 3rd",
			time.Date(2026, 8, 4, 1, 0, 0, 0, jst).UTC(), endOf4th, DueToday},
		{"the evening of the day before is not yet today",
			time.Date(2026, 8, 3, 22, 0, 0, 0, jst).UTC(), endOf4th, DueLater},
		{"a second past the promise is overdue",
			endOf4th.Add(time.Second), endOf4th, DueOverdue},
		{"exactly at the promise is not yet overdue",
			endOf4th, endOf4th, DueToday},
		{"a stamp earlier the same day that has passed is OVERDUE, not today",
			time.Date(2026, 8, 4, 14, 0, 0, 0, jst).UTC(),
			time.Date(2026, 8, 4, 10, 30, 0, 0, jst).UTC(), DueOverdue},
		{"days out is later",
			time.Date(2026, 8, 1, 12, 0, 0, 0, jst).UTC(), endOf4th, DueLater},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DueStateOf(due(c.due), c.now, jst); got != c.want {
				t.Errorf("DueStateOf = %q, want %q (now %s, due %s)",
					got, c.want, c.now.In(jst), c.due.In(jst))
			}
		})
	}

	if got := DueStateOf(&Task{ID: "t-none1"}, time.Now(), jst); got != DueNone {
		t.Errorf("a task with no due date = %q, want %q", got, DueNone)
	}
}

// The same instant is "today" or "later" depending only on the zone it is read
// in — which is why loc is a parameter and not the clock's own location.
func TestDueStateOfIsZoneDependent(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 0, 0, 0, time.UTC) // 2026-08-04 01:00 JST
	d := time.Date(2026, 8, 4, 23, 59, 59, 0, jst).UTC()
	if got := DueStateOf(due(d), now, jst); got != DueToday {
		t.Errorf("in JST = %q, want %q", got, DueToday)
	}
	if got := DueStateOf(due(d), now, time.UTC); got != DueLater {
		t.Errorf("in UTC = %q, want %q", got, DueLater)
	}
}

// The lint findings: severity, code, and — the load-bearing rule — WHICH lanes
// go quiet. `waiting` is terminal on the shipped config and must still report;
// only the done lane and the configured parked lanes are skipped.
func TestDueProblems(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, jst).UTC()
	past := time.Date(2026, 8, 2, 10, 0, 0, 0, jst).UTC()
	todayLate := time.Date(2026, 8, 4, 23, 59, 59, 0, jst).UTC()
	later := time.Date(2026, 9, 1, 10, 0, 0, 0, jst).UTC()

	idx := &Index{Tasks: []Task{
		{ID: "t-wait1", Status: "waiting", Due: &past},    // terminal, but NOT skipped
		{ID: "t-rdy01", Status: "ready", Due: &todayLate}, // due today -> warn
		{ID: "t-rdy02", Status: "ready", Due: &later},     // not yet -> nothing
		{ID: "t-rdy03", Status: "ready"},                  // no date -> nothing
		{ID: "t-done1", Status: "done", Due: &past},       // settled -> skipped
		{ID: "t-ice01", Status: "icebox", Due: &past},     // parked -> skipped
	}}
	skip := map[string]bool{"done": true, "icebox": true}

	ps := DueProblems(idx, now, jst, skip)
	if len(ps) != 2 {
		t.Fatalf("got %d problems, want 2: %+v", len(ps), ps)
	}
	// Sorted by id: t-rdy01 (today, warn) before t-wait1 (overdue, error).
	if ps[0].ID != "t-rdy01" || ps[0].Code != "due-today" || ps[0].Severity != SevWarn {
		t.Errorf("first problem = %+v, want a due-today warn on t-rdy01", ps[0])
	}
	if ps[1].ID != "t-wait1" || ps[1].Code != "due-overdue" || ps[1].Severity != SevError {
		t.Errorf("second problem = %+v, want a due-overdue ERROR on t-wait1", ps[1])
	}
	// Both codes must be registered, or `lint --code due-overdue` would reject
	// the very code lint emits.
	for _, p := range ps {
		if !IsLintCode(p.Code) {
			t.Errorf("code %q is not in the lint-code registry", p.Code)
		}
	}
}

// An empty skip set means "report everywhere" — the [due].ignore_lanes = []
// spelling. Nothing is exempt except by naming it.
func TestDueProblemsWithNoSkippedLanes(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, jst).UTC()
	past := now.Add(-48 * time.Hour)
	idx := &Index{Tasks: []Task{{ID: "t-done1", Status: "done", Due: &past}}}
	if ps := DueProblems(idx, now, jst, nil); len(ps) != 1 {
		t.Errorf("got %d problems, want 1 (nothing skipped): %+v", len(ps), ps)
	}
}

// FormatDue renders in loc and matches what `furrow show` prints, so the date in
// a lint message and the date in the detail view are the same string.
func TestFormatDue(t *testing.T) {
	d := time.Date(2026, 8, 4, 10, 30, 0, 0, jst).UTC()
	if got, want := FormatDue(due(d), jst), "2026-08-04 10:30 +09:00"; got != want {
		t.Errorf("FormatDue = %q, want %q", got, want)
	}
	if got := FormatDue(&Task{ID: "t-none1"}, jst); got != "" {
		t.Errorf("FormatDue with no date = %q, want empty", got)
	}
}
