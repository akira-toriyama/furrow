package app

import (
	"fmt"
	"reflect"
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

// A zone whose local MIDNIGHT does not exist is the case the DST test above
// cannot see: Santiago springs forward AT 00:00 (2026-09-06), so
// ParseInLocation of that date normalizes BACKWARD to 23:00 on the 5th. Deriving
// the calendar day from that result promised the task for the day BEFORE — i.e.
// it was overdue for every hour of the day it was promised for, the exact
// failure the end-of-day rule exists to prevent.
func TestParseDueMidnightGapZones(t *testing.T) {
	cases := []struct{ zone, day string }{
		{"America/Santiago", "2026-09-06"},
		{"America/Havana", "2026-03-08"},
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range cases {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Skipf("no tzdata for %s", c.zone)
		}
		got, err := ParseDue(c.day, now, loc)
		if err != nil {
			t.Fatalf("ParseDue(%q) in %s: %v", c.day, c.zone, err)
		}
		if day := got.In(loc).Format("2006-01-02"); day != c.day {
			t.Errorf("%s --due %s landed on %s (%s) — a day early", c.zone, c.day, day, got.In(loc))
		}
	}
}

// A dated DRAFT must reach the due read on a repo-scoped board. It is the
// "note it on the board, attach it later" shape, and lint (board-wide) errors on
// it — so if the scope hid it here, one `furrow brief` would print a due section
// of 1 above a lint ride-along counting 2.
func TestDueIncludesDraftsUnderARepoScope(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	scoped, err := a.Add("scoped", AddOpts{Status: "waiting", Repos: []string{"me/app"}, Due: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.Add("drafted", AddOpts{Status: "waiting", Draft: true, Due: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := a.Due(QueryOpts{ScopeRepo: "me/app"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range sum.Overdue {
		got[it.Task.ID] = true
	}
	if !got[scoped.ID] || !got[draft.ID] || len(sum.Overdue) != 2 {
		t.Errorf("scoped due = %v, want both %s (scoped) and %s (draft)", got, scoped.ID, draft.ID)
	}
	// An EXPLICIT -r still applies to a task that HAS repos: the reader typed
	// that one. The BOARD scope no longer does — see TestDueIgnoresTheBoardRepoScope.
	other, err := a.Add("elsewhere", AddOpts{Status: "waiting", Repos: []string{"me/other"}, Due: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	sum, _ = a.Due(QueryOpts{Repo: "me/app"})
	for _, it := range sum.Overdue {
		if it.Task.ID == other.ID {
			t.Errorf("a task in another repo leaked past an explicit -r: %s", other.ID)
		}
	}
}

// The board's repo scope must NOT hide a date. It is derived from the cwd —
// nobody typed it — and `lint` has no repo filter at all, so a scope that
// filtered here made `furrow brief` print a due section of 1 above a lint
// ride-along counting 2 (measured 2026-08-12: a task promised for 15:00 reached
// the session-start read only because it happened to name the scoped repo).
// The rule is about WHO CHOSE the filter: an automatic narrowing never hides a
// promise, an explicitly typed one still may.
func TestDueIgnoresTheBoardRepoScope(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	here, err := a.Add("in the scoped repo", AddOpts{Status: "waiting", Repos: []string{"me/app"}, Due: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := a.Add("in another repo entirely", AddOpts{Status: "waiting", Repos: []string{"me/other"}, Due: "2026-07-30"})
	if err != nil {
		t.Fatal(err)
	}

	sum, err := a.Due(QueryOpts{ScopeRepo: "me/app"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range sum.Overdue {
		got[it.Task.ID] = true
	}
	if !got[here.ID] || !got[elsewhere.ID] {
		t.Errorf("board-scoped due = %v, want BOTH %s (scoped) and %s (other repo)", got, here.ID, elsewhere.ID)
	}
	// Longest-overdue-first survives the widening.
	if len(sum.Overdue) > 0 && sum.Overdue[0].Task.ID != elsewhere.ID {
		t.Errorf("ordering broke: leader = %s, want the longest-overdue %s", sum.Overdue[0].Task.ID, elsewhere.ID)
	}
	// The agreement that is the whole point: lint is board-wide, so the count
	// the due read quotes must equal the number of due-overdue problems.
	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	overdue := 0
	for _, p := range probs {
		if p.Code == "due-overdue" {
			overdue++
		}
	}
	if overdue != len(sum.Overdue) {
		t.Errorf("lint reports %d due-overdue but the board-scoped due read found %d — the two surfaces disagree", overdue, len(sum.Overdue))
	}
}

// What a RENDERER may say must match what lint and brief say: a task whose lane
// is exempt never reads as overdue, or `ls` would call a task finished a week
// EARLY late while lint stayed silent about it.
func TestDueDisplayStateNeverContradictsTheLintPolicy(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	late, _ := a.Add("late", AddOpts{Status: "ready", Due: "2026-08-01"})
	parked, _ := a.Add("parked", AddOpts{Status: "icebox", Due: "2026-08-01"})
	shipped, _ := a.Add("shipped early", AddOpts{Status: "done", Due: "2026-08-01"})

	if got := a.DueDisplayState(late); got != core.DueOverdue {
		t.Errorf("an open overdue task = %q, want %q", got, core.DueOverdue)
	}
	for _, tk := range []*core.Task{parked, shipped} {
		if got := a.DueDisplayState(tk); got == core.DueOverdue || got == core.DueToday {
			t.Errorf("%s (lane %q) = %q; an exempt lane must not read as an alarm", tk.ID, tk.Status, got)
		}
	}
	if got := a.DueDisplayState(&core.Task{ID: "t-nodue", Status: "ready"}); got != core.DueNone {
		t.Errorf("a dateless task = %q, want %q", got, core.DueNone)
	}
}

// `-q due:<bare day>` must denote the same day `--due <bare day>` wrote, or the
// tool cannot find what it just stored. (The machine stamps stay UTC days.)
func TestQueryDueBareDayIsTheOperatorsDay(t *testing.T) {
	// 2026-08-04 12:00 JST; the task is promised for the 4th, stored 14:59:59Z.
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	tk, err := a.Add("promised", AddOpts{Status: "ready", Due: "2026-08-04"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.List(QueryOpts{Query: "due:2026-08-04"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != tk.ID {
		t.Errorf("-q due:2026-08-04 = %v, want the task written by --due 2026-08-04", got)
	}
	if out, _ := a.List(QueryOpts{Query: "due:<=2026-08-04"}); len(out) != 1 {
		t.Errorf("-q due:<=2026-08-04 = %d rows, want 1", len(out))
	}
}

// The due band's rows tie constantly: ParseDue pins a bare-day due to that day's
// 23:59:59, so every task promised for the same day carries a byte-identical
// instant. Feeding sortByDue the REVERSE of the canonical order is what makes
// this an assertion about the TIEBREAK rather than about sort.SliceStable — a
// stable sort with no tiebreak hands a tied input straight back, so only the
// canonical comparator can turn the reversed input into the canonical order.
// Nothing pinned this before, which is how swapping the one sort call for an
// unstable slices.SortFunc scrambled brief's overdue band from its first row
// with all 11 packages still green.
func TestSortByDueBreaksTiesByCanonicalOrderNotInputOrder(t *testing.T) {
	lanes := config.Default().Lanes
	sameDay := time.Date(2026, 8, 4, 14, 59, 59, 0, time.UTC) // 2026-08-04 23:59:59 JST
	earlier := sameDay.Add(-24 * time.Hour)

	mk := func(id, lane string, prio int, due time.Time) ListItem {
		d := due
		return ListItem{Task: core.Task{ID: id, Status: lane, Priority: prio, Due: &d}}
	}
	// Canonical order is lane rank, then priority, then id — so within sameDay:
	// ready(t-b, t-c by priority) then waiting(t-a, t-z by id at equal priority).
	want := []string{"t-old", "t-b", "t-c", "t-a", "t-z"}
	items := []ListItem{
		mk("t-z", "waiting", 20, sameDay),
		mk("t-a", "waiting", 20, sameDay),
		mk("t-c", "ready", 30, sameDay),
		mk("t-b", "ready", 10, sameDay),
		mk("t-old", "waiting", 99, earlier),
	}
	sortByDue(items, core.TaskOrder(lanes))

	got := make([]string, len(items))
	for i, it := range items {
		got[i] = it.Task.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("band = %v, want %v (earliest instant first, then lane -> priority -> id)", got, want)
	}
}

// The end-to-end shape the defect was measured in: 15 tasks over 3 overdue days,
// interleaved so no run of equal keys arrives already grouped — an input whose
// ties are contiguous short-circuits pdqsort's sorted-run detection and hides
// the scramble, which is why the first attempt to reproduce it saw nothing. One
// lane and ascending add order make the expected within-day order the board
// order a reader would see in `furrow ls`.
func TestDueBandOrdersInterleavedDaysThenBoardOrder(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)) // 2026-08-28 12:00 JST
	days := []string{"2026-08-20", "2026-08-21", "2026-08-22"}
	perDay := map[string][]string{}
	for n := 1; n <= 15; n++ {
		day := days[n%len(days)]
		tk, err := a.Add(fmt.Sprintf("probe %d", n), AddOpts{Status: "waiting", Due: day})
		if err != nil {
			t.Fatalf("add probe %d: %v", n, err)
		}
		perDay[day] = append(perDay[day], tk.ID)
	}
	var want []string
	for _, d := range days {
		want = append(want, perDay[d]...)
	}

	sum, err := a.Due(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(sum.Overdue))
	for i, it := range sum.Overdue {
		got[i] = it.Task.ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overdue band = %v,\nwant %v (oldest day first, board order within a day)", got, want)
	}
}
