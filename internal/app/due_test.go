package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// jst is the operator zone these tests read wall-clock dates in — FIXED, never
// time.Local, so a CI runner in UTC asserts the same instants.
var jst = time.FixedZone("JST", 9*60*60)

// newDueApp is newApp with the clock and the operator zone both pinned: the two
// are deliberately different (UTC clock, +09:00 zone), which is the shape every
// real invocation has (core.SystemClock is UTC) and the only shape that catches
// a day boundary read off the clock instead of off the zone.
func newDueApp(now time.Time) *App {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
	a := NewWithStore(st, cfg, &fixedClock{t: now})
	a.Loc = jst
	return a
}

func TestParseDue(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC) // 2026-08-03 12:00 JST
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"a bare date is the END of that day in the operator's zone",
			"2026-08-04", time.Date(2026, 8, 4, 23, 59, 59, 0, jst)},
		{"a wall-clock minute is read in the operator's zone",
			"2026-08-04T10:30", time.Date(2026, 8, 4, 10, 30, 0, 0, jst)},
		{"the space spelling is the same instant",
			"2026-08-04 10:30", time.Date(2026, 8, 4, 10, 30, 0, 0, jst)},
		{"seconds are accepted",
			"2026-08-04T10:30:15", time.Date(2026, 8, 4, 10, 30, 15, 0, jst)},
		{"RFC3339 carries its own offset",
			"2026-08-04T10:30:00+09:00", time.Date(2026, 8, 4, 10, 30, 0, 0, jst)},
		{"an RFC3339 instant in another zone is that instant",
			"2026-08-04T01:30:00Z", time.Date(2026, 8, 4, 10, 30, 0, 0, jst)},
		{"a positive offset is measured from now", "+1d", now.Add(24 * time.Hour)},
		{"hours work too", "+2h", now.Add(2 * time.Hour)},
		{"a negative offset is legal (dating something in the past)", "-1d", now.Add(-24 * time.Hour)},
		{"whitespace is trimmed", "  2026-08-04  ", time.Date(2026, 8, 4, 23, 59, 59, 0, jst)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseDue(c.in, now, jst)
			if err != nil {
				t.Fatalf("ParseDue(%q): %v", c.in, err)
			}
			if !got.Equal(c.want) {
				t.Errorf("ParseDue(%q) = %s, want %s", c.in, got.In(jst), c.want.In(jst))
			}
		})
	}
}

// Every rejected spelling must be exit 2 (validation), never a stored zero time:
// a due date silently set to year 1 would be overdue forever.
func TestParseDueRejects(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	for _, in := range []string{
		"",                       // an empty flag is never a silent clear
		"   ",                    // nor a blank one
		"tomorrow",               // no word spellings
		"2h",                     // an offset needs its sign
		"2026-13-45",             // not a date
		"2026-08-04T25:00",       // not a time
		"+99999999999999999999d", // overflows a Duration; must not wrap negative
		"08/04/2026",             // not a supported spelling
	} {
		_, err := ParseDue(in, now, jst)
		if err == nil {
			t.Errorf("ParseDue(%q) should have failed", in)
			continue
		}
		if ce := core.AsError(err); ce == nil || ce.Code != core.CodeValidation {
			t.Errorf("ParseDue(%q) error = %v, want a validation error", in, err)
		}
	}
}

// The write paths: `add --due` binds at creation, `set --due` re-dates, and
// `--clear-due` removes. The stored stamp is UTC whole seconds (the marshaller's
// contract), whatever the spelling.
func TestAddAndSetDue(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	a := newDueApp(now)

	tk, err := a.Add("check the nightly run", AddOpts{Status: "waiting", Due: "2026-08-04"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Due == nil {
		t.Fatal("add --due stored no date")
	}
	if want := time.Date(2026, 8, 4, 23, 59, 59, 0, jst); !tk.Due.Equal(want) {
		t.Errorf("stored due = %s, want %s", tk.Due.In(jst), want)
	}
	if tk.Due.Location() != time.UTC {
		t.Errorf("stored due is in %v, want UTC (the on-disk contract)", tk.Due.Location())
	}

	// Snooze: the offset is measured from NOW, so it lands in the future even
	// though the existing date is in the past relative to it.
	spell := "+2d"
	moved, _, err := a.Set(tk.ID, SetOpts{Due: &spell})
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(48 * time.Hour); !moved.Due.Equal(want) {
		t.Errorf("snoozed due = %s, want %s", moved.Due, want)
	}

	cleared, _, err := a.Set(tk.ID, SetOpts{ClearDue: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Due != nil {
		t.Errorf("--clear-due left %v", cleared.Due)
	}
}

// A malformed spelling must be refused BEFORE anything is written — on both the
// single and the bulk path, since a bulk write is all-or-nothing.
func TestDueRejectedBeforeWrite(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC))
	if _, err := a.Add("bad", AddOpts{Due: "someday"}); err == nil {
		t.Error("add --due someday should have failed")
	}
	if _, err := a.AddMany([]AddSpec{
		{Title: "ok", AddOpts: AddOpts{Due: "2026-08-04"}},
		{Title: "bad", AddOpts: AddOpts{Due: "someday"}},
	}); err == nil {
		t.Error("a bulk add with one bad due should have failed")
	}
	all, _ := a.List(QueryOpts{})
	if len(all) != 0 {
		t.Errorf("a rejected write created %d task(s); it must create none", len(all))
	}

	tk, err := a.Add("real", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	bad := "someday"
	if _, _, err := a.Set(tk.ID, SetOpts{Due: &bad}); err == nil {
		t.Error("set --due someday should have failed")
	}
	again, _, _ := a.Get(tk.ID)
	if again.Due != nil {
		t.Errorf("a rejected set stored %v", again.Due)
	}
}

// `set` with no other flag must count --due/--clear-due as a change; otherwise
// the one-flag snooze is exit 2 "nothing to do".
func TestSetDueCountsAsAChange(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC))
	tk, _ := a.Add("x", AddOpts{})
	spell := "+1d"
	if _, _, err := a.Set(tk.ID, SetOpts{Due: &spell}); err != nil {
		t.Errorf("set --due alone: %v", err)
	}
	if _, _, err := a.Set(tk.ID, SetOpts{ClearDue: true}); err != nil {
		t.Errorf("set --clear-due alone: %v", err)
	}
}

// The brief section: overdue first (longest overdue leading), then today; a
// future date and the skipped lanes say nothing. This is the read `furrow brief`
// leads with, and it must not be scoped by the active epic.
func TestDueSummary(t *testing.T) {
	now := time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC) // 2026-08-04 12:00 JST
	a := newDueApp(now)

	mk := func(title, lane, due string) *core.Task {
		tk, err := a.Add(title, AddOpts{Status: lane, Due: due})
		if err != nil {
			t.Fatalf("add %q: %v", title, err)
		}
		return tk
	}
	veryLate := mk("very late", "waiting", "2026-08-01")
	late := mk("late", "ready", "2026-08-03")
	today := mk("today", "waiting", "2026-08-04")
	mk("later", "ready", "2026-09-01")
	mk("parked", "icebox", "2026-08-01")
	mk("shipped", "done", "2026-08-01")

	sum, err := a.Due(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Overdue) != 2 || len(sum.Today) != 1 {
		t.Fatalf("summary = %d overdue / %d today, want 2 / 1", len(sum.Overdue), len(sum.Today))
	}
	if sum.Overdue[0].Task.ID != veryLate.ID || sum.Overdue[1].Task.ID != late.ID {
		t.Errorf("overdue order = %s, %s; want the longest overdue first (%s, %s)",
			sum.Overdue[0].Task.ID, sum.Overdue[1].Task.ID, veryLate.ID, late.ID)
	}
	if sum.Today[0].Task.ID != today.ID {
		t.Errorf("today = %s, want %s", sum.Today[0].Task.ID, today.ID)
	}
	if sum.Total() != 3 || sum.Empty() {
		t.Errorf("Total/Empty = %d/%v, want 3/false", sum.Total(), sum.Empty())
	}
}

// brief composes Due, and the section must survive brief's own lane filtering:
// a promised task usually sits in a lane `next` excludes.
func TestBriefCarriesDue(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	if _, err := a.Add("check the run", AddOpts{Status: "waiting", Due: "2026-08-01"}); err != nil {
		t.Fatal(err)
	}
	b, err := a.Brief(QueryOpts{}, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Due.Overdue) != 1 {
		t.Fatalf("brief.Due.Overdue = %d, want 1", len(b.Due.Overdue))
	}
	if len(b.Next) != 0 {
		t.Errorf("the waiting task must not appear in next: %+v", b.Next)
	}
}

// `-q is:overdue` and the due date qualifiers read the same field the lint rule
// does, so an audit query cannot disagree with the error.
func TestQueryDue(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	late, _ := a.Add("late", AddOpts{Status: "ready", Due: "2026-08-01"})
	soon, _ := a.Add("soon", AddOpts{Status: "ready", Due: "2026-09-01"})
	none, _ := a.Add("undated", AddOpts{Status: "ready"})

	ids := func(q string) []string {
		got, err := a.List(QueryOpts{Query: q})
		if err != nil {
			t.Fatalf("-q %q: %v", q, err)
		}
		out := make([]string, 0, len(got))
		for _, tk := range got {
			out = append(out, tk.ID)
		}
		return out
	}
	if got := ids("is:overdue"); len(got) != 1 || got[0] != late.ID {
		t.Errorf("is:overdue = %v, want [%s]", got, late.ID)
	}
	if got := ids("has:due"); len(got) != 2 {
		t.Errorf("has:due = %v, want 2 tasks", got)
	}
	if got := ids("no:due"); len(got) != 1 || got[0] != none.ID {
		t.Errorf("no:due = %v, want [%s]", got, none.ID)
	}
	if got := ids("due:>2026-08-15"); len(got) != 1 || got[0] != soon.ID {
		t.Errorf("due:>2026-08-15 = %v, want [%s]", got, soon.ID)
	}
}

// A bare date must land at 23:59:59 WALL CLOCK, which is not midnight + 24h - 1s
// on a DST day: US/Pacific springs forward on 2026-03-08 (a 23-hour day, where
// the arithmetic form overshoots into the 9th) and falls back on 2026-11-01 (a
// 25-hour day, where it stops an hour short).
func TestParseDueEndOfDayAcrossDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata for America/Los_Angeles")
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, day := range []string{"2026-03-08", "2026-11-01", "2026-06-15"} {
		got, err := ParseDue(day, now, la)
		if err != nil {
			t.Fatalf("ParseDue(%q): %v", day, err)
		}
		local := got.In(la)
		if local.Format("2006-01-02 15:04:05") != day+" 23:59:59" {
			t.Errorf("ParseDue(%q) = %s, want %s 23:59:59 local", day, local, day)
		}
	}
}
