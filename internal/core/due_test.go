package core

import (
	"strings"
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
		{ID: "t-rdy00", Status: "ready", Due: &past},      // open lane, overdue -> the work itself
		{ID: "t-rdy01", Status: "ready", Due: &todayLate}, // due today -> warn
		{ID: "t-rdy02", Status: "ready", Due: &later},     // not yet -> nothing
		{ID: "t-rdy03", Status: "ready"},                  // no date -> nothing
		{ID: "t-done1", Status: "done", Due: &past},       // settled -> skipped
		{ID: "t-ice01", Status: "icebox", Due: &past},     // parked -> skipped
	}}
	skip := map[string]bool{"done": true, "icebox": true}
	terminal := map[string]bool{"done": true, "icebox": true, "waiting": true}

	ps := DueProblems(idx, now, jst, skip, terminal)
	if len(ps) != 3 {
		t.Fatalf("got %d problems, want 3: %+v", len(ps), ps)
	}
	// sortProblems' order — severity first, so the overdue ERRORs lead the
	// due-today warn whatever their ids (the rule's own sort once dropped the
	// severity key and interleaved the two by id; t-2xqp).
	if ps[0].ID != "t-rdy00" || ps[0].Code != "due-overdue" || ps[0].Severity != SevError {
		t.Errorf("first problem = %+v, want a due-overdue ERROR on t-rdy00", ps[0])
	}
	if ps[1].ID != "t-wait1" || ps[1].Code != "due-overdue" || ps[1].Severity != SevError {
		t.Errorf("second problem = %+v, want a due-overdue ERROR on t-wait1", ps[1])
	}
	if ps[2].ID != "t-rdy01" || ps[2].Code != "due-today" || ps[2].Severity != SevWarn {
		t.Errorf("third problem = %+v, want a due-today warn on t-rdy01", ps[2])
	}
	// The remedy follows the lane: an open lane is offered the work, a terminal
	// lane the chase — "do it" is wrong for a task nobody is working — and both
	// name the close, because a finished wait is closed from where it sits.
	if open := ps[0].Msg; !strings.Contains(open, "do it, close it (`furrow done t-rdy00`)") || strings.Contains(open, "parked") {
		t.Errorf("open-lane remedy = %q, want the work, the close and the date, no chase", open)
	}
	if parked := ps[1].Msg; !strings.Contains(parked, `parked in "waiting"`) || !strings.Contains(parked, "chase or escalate, close it (`furrow done t-wait1`)") || strings.Contains(parked, "do it") {
		t.Errorf("terminal-lane remedy = %q, want the chase and the close, never \"do it\"", parked)
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
	if ps := DueProblems(idx, now, jst, nil, nil); len(ps) != 1 {
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

// due-inversion: a dated task waiting on a dep promised LATER than itself. One
// finding per task naming every such dep; the skip set silences both ends of
// the edge (a done or parked date is not a live promise) and an undated dep
// says nothing. The seed of the furrow-test board carried one such edge until
// a rebuild found it by hand (t-fejrz → t-ehf4f) — the case this check is for.
func TestDueInversionProblems(t *testing.T) {
	mar10 := time.Date(2026, 3, 10, 12, 0, 0, 0, jst).UTC()
	mar15 := time.Date(2026, 3, 15, 12, 0, 0, 0, jst).UTC()
	mar20 := time.Date(2026, 3, 20, 12, 0, 0, 0, jst).UTC()
	idx := &Index{Tasks: []Task{
		{ID: "t-inv01", Status: "ready", Due: &mar10, Deps: []string{"t-late1", "t-late2", "t-early", "t-nodue", "t-parkd", "t-done1", "t-gone"}},
		{ID: "t-late1", Status: "backlog", Due: &mar15},                           // later -> named
		{ID: "t-late2", Status: "ready", Due: &mar20},                             // later -> named
		{ID: "t-early", Status: "ready", Due: &mar10},                             // same instant -> fine
		{ID: "t-nodue", Status: "ready"},                                          // undated -> silent
		{ID: "t-parkd", Status: "icebox", Due: &mar20},                            // parked -> skipped
		{ID: "t-done1", Status: "done", Due: &mar20},                              // settled -> skipped
		{ID: "t-ok001", Status: "ready", Due: &mar20, Deps: []string{"t-late1"}},  // dep earlier -> fine
		{ID: "t-park2", Status: "icebox", Due: &mar10, Deps: []string{"t-late2"}}, // the task itself parked -> skipped
		{ID: "t-undat", Status: "ready", Deps: []string{"t-late2"}},               // no due of its own -> silent
	}}
	skip := map[string]bool{"done": true, "icebox": true}

	ps := DueInversionProblems(idx, jst, skip)
	if len(ps) != 1 {
		t.Fatalf("got %d problems, want 1: %+v", len(ps), ps)
	}
	p := ps[0]
	if p.ID != "t-inv01" || p.Code != "due-inversion" || p.Severity != SevWarn {
		t.Errorf("problem = %+v, want a due-inversion warn on t-inv01", p)
	}
	for _, want := range []string{"due 2026-03-10 12:00 +09:00", "t-late1 (due 2026-03-15 12:00 +09:00)", "t-late2 (due 2026-03-20 12:00 +09:00)", "`furrow set t-inv01 --due <date>`", "`furrow dep t-inv01 <dep> --rm`"} {
		if !strings.Contains(p.Msg, want) {
			t.Errorf("message %q lacks %q", p.Msg, want)
		}
	}
	for _, silent := range []string{"t-early", "t-nodue", "t-parkd", "t-done1", "t-gone"} {
		if strings.Contains(p.Msg, silent) {
			t.Errorf("message names %s, which is not an inverted live promise: %q", silent, p.Msg)
		}
	}
	if !IsLintCode(p.Code) {
		t.Errorf("code %q is not in the lint-code registry", p.Code)
	}
}
