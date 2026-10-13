package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A bare-date series promises DAYS, so a close settles the whole local day of
// the later of now and the due. Two defects of the instant rule this replaces,
// both measured on 753c41f: the snooze (`set --due +1d`, the remedy furrow
// prints for due-overdue) landed the due off-lattice and a close later that
// day was handed back that very day's 23:59:59 point; and one afternoon-late
// close of a daily chore made every following afternoon close late as well,
// with a due-overdue error each day. A timed series promises instants and is
// settled as written. Reviewed adversarially on 2026-09-13: settling the day
// costs a just-past-midnight close of yesterday's chore today's occurrence,
// which is deliberate and documented; the variant that also settled a timed
// slot later that day, and the one that counted the settled day as skipped,
// were refuted.
func TestACloseSettlesTheWholeDayOfABareDateSeries(t *testing.T) {
	at := func(y int, mo time.Month, d, h, m int) time.Time { return time.Date(y, mo, d, h, m, 0, 0, jst) }
	eod := func(y int, mo time.Month, d int) time.Time { return time.Date(y, mo, d, 23, 59, 59, 0, jst) }
	cases := []struct {
		name        string
		bindAt      time.Time
		due, rule   string
		snooze      string // a Set --due before the close; "" = none
		closeAt     time.Time
		want        time.Time
		wantSkipped int
		completed   bool
	}{
		{"snoozed to earlier today, closed this afternoon", at(2026, 9, 11, 17, 20),
			"2026-09-10", "daily", "2026-09-11T08:00", at(2026, 9, 11, 17, 20), eod(2026, 9, 12), 0, false},
		{"the printed remedy: +1d, closed the next day", at(2026, 9, 11, 17, 20),
			"2026-09-10", "daily", "+1d", at(2026, 9, 12, 18, 0), eod(2026, 9, 13), 0, false},
		{"+3d, closed early the same day: the snoozed-to day is the one settled", at(2026, 9, 11, 17, 19),
			"2026-09-11", "daily", "+3d", at(2026, 9, 11, 17, 30), eod(2026, 9, 15), 0, false},
		{"snoozed, then late again: the snoozed day is settled, not skipped", at(2026, 9, 11, 17, 20),
			"2026-09-10", "daily", "2026-09-12T17:20", at(2026, 9, 13, 10, 0), eod(2026, 9, 14), 0, false},
		{"one afternoon late: tomorrow's, so the next afternoon is on time", at(2026, 9, 10, 12, 0),
			"2026-09-10", "daily", "", at(2026, 9, 11, 16, 0), eod(2026, 9, 12), 0, false},
		{"just past midnight settles the new day (deliberate, documented)", at(2026, 9, 10, 12, 0),
			"2026-09-10", "daily", "", at(2026, 9, 11, 0, 10), eod(2026, 9, 12), 0, false},
		{"weekly closed three Fridays late: next Friday, two lapsed", at(2026, 8, 21, 12, 0),
			"2026-08-21", "weekly on fri", "", at(2026, 9, 11, 17, 25), eod(2026, 9, 18), 2, false},
		{"yearly closed on the anniversary settles this year's", at(2025, 9, 13, 12, 0),
			"2025-09-13", "yearly", "", at(2026, 9, 13, 16, 2), eod(2027, 9, 13), 0, false},
		{"a timed series is settled as written: tonight's slot stands", at(2026, 9, 11, 7, 0),
			"2026-09-10T21:00", "daily", "2026-09-11T08:00", at(2026, 9, 11, 9, 0), at(2026, 9, 11, 21, 0), 0, false},
		{"a timed series closed late counts the slots that passed", at(2026, 9, 10, 8, 0),
			"2026-09-10T09:00", "daily", "", at(2026, 9, 11, 16, 0), at(2026, 9, 12, 9, 0), 1, false},
		{"completion reports what lapsed", at(2026, 9, 11, 17, 20),
			"2026-09-11", "daily until 2026-09-13", "", at(2026, 9, 15, 17, 0), time.Time{}, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newRepeatApp(c.bindAt.UTC())
			task := mustAddRepeating(t, a, "水やり", c.due, c.rule, AddOpts{})
			if c.snooze != "" {
				snooze := c.snooze
				if _, _, _, err := a.Set(task.ID, SetOpts{Due: &snooze}); err != nil {
					t.Fatalf("snooze: %v", err)
				}
			}
			a.Clock = &fixedClock{t: c.closeAt.UTC()}
			_, rep, err := a.moveOne(task.ID, a.Cfg.DoneLane)
			if err != nil {
				t.Fatal(err)
			}
			if rep == nil {
				t.Fatal("no series report")
			}
			if rep.Completed != c.completed {
				t.Fatalf("completed = %v, want %v (%+v)", rep.Completed, c.completed, rep)
			}
			if !c.completed && (rep.Due == nil || !rep.Due.Equal(c.want)) {
				t.Errorf("next due = %v, want %s", rep.Due, c.want.Format(time.RFC3339))
			}
			if rep.Skipped != c.wantSkipped {
				t.Errorf("skipped = %d, want %d", rep.Skipped, c.wantSkipped)
			}
		})
	}
}

// The regression no single-close test can see: under the instant rule, one
// afternoon-late close of a bare-date daily minted a successor due that same
// night, which the next afternoon's close was late for again — late forever,
// with a due-overdue error each day. Settling the day heals the chain after
// the one late close.
func TestAChainOfAfternoonClosesIsOnTimeAfterOneLateDay(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 9, d, h, 0, 0, 0, jst) }
	a := newRepeatApp(at(10, 12).UTC())
	task := mustAddRepeating(t, a, "水やり", "2026-09-10", "daily", AddOpts{})
	live := func() *core.Task {
		t.Helper()
		tasks, err := a.List(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range tasks {
			if tasks[i].Repeat != "" {
				return &tasks[i]
			}
		}
		t.Fatal("no live occurrence")
		return nil
	}
	for day := 11; day <= 14; day++ {
		now := at(day, 16)
		a.Clock = &fixedClock{t: now.UTC()}
		cur := live()
		late := cur.Due.Before(now)
		if day > 11 && late {
			t.Fatalf("day %d: the close is late (due %v) — the chain did not heal", day, cur.Due.In(jst))
		}
		if day == 11 && !late {
			t.Fatalf("day 11 should be the one late close (due %v)", cur.Due.In(jst))
		}
		_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 9, day+1, 23, 59, 59, 0, jst)
		if rep == nil || rep.Due == nil || !rep.Due.Equal(want) || rep.Skipped != 0 {
			t.Fatalf("day %d: report %+v, want next due %s and nothing skipped", day, rep, want.Format(time.RFC3339))
		}
	}
	_ = task
}

// TestSuccessorInheritsTheEpicEvenWhenItIsClosed pins the membership half of the
// inheritance, the half v10 shipped with no coverage at all, and the loop it
// creates. The successor stays in the box whatever state the box is in —
// dropping it for a CLOSED one would trade lint's epic-closed WARN for an
// epic-required ERROR — so `epic done`'s disclosure (EpicOpenMembers) is what
// makes the loop visible, and re-filing ONCE is what ends it: every later
// occurrence inherits the new box.
func TestSuccessorInheritsTheEpicEvenWhenItIsClosed(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	eid := mustEpic(t, a, "box", EpicAddOpts{})
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "daily", AddOpts{Epic: eid})
	if _, _, err := a.EpicDone(eid); err != nil {
		t.Fatalf("epic done: %v", err)
	}

	closed, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Created == nil {
		t.Fatalf("no successor reported: %+v", rep)
	}
	succ := other(t, a, closed.ID)
	if succ.Epic != eid {
		t.Fatalf("successor epic = %q, want %q — the box's state is not a filing decision", succ.Epic, eid)
	}
	// The warn does not accumulate, it MOVES: the closed occurrence is terminal
	// and drops out, the fresh one takes its place under the same closed box.
	left := a.EpicOpenMembers(eid)
	if len(left) != 1 || left[0].ID != succ.ID || left[0].Repeat == "" {
		t.Fatalf("open members after the close = %+v, want only the live successor %s carrying the rule", left, succ.ID)
	}

	// Re-filing once carries the whole series, which is why the disclosure spells
	// that remedy out rather than refusing the close.
	open := mustEpic(t, a, "open box", EpicAddOpts{})
	if _, _, _, err := a.Set(succ.ID, SetOpts{Epic: &open}); err != nil {
		t.Fatalf("re-file: %v", err)
	}
	_, rep2, err := a.moveOne(succ.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep2 == nil || rep2.Created == nil {
		t.Fatalf("no successor after the second close: %+v", rep2)
	}
	next, _, err := a.Get(*rep2.Created)
	if err != nil {
		t.Fatal(err)
	}
	if next.Epic != open {
		t.Errorf("next occurrence epic = %q, want %q — re-filing once must carry the series", next.Epic, open)
	}
	if left := a.EpicOpenMembers(eid); len(left) != 0 {
		t.Errorf("closed box still holds %+v — the series left it for good", left)
	}
}

// Both bind-time notes are independent facts about the same rule, and the dates
// they name are read in the BOARD's calendar — a note that resolved them in UTC
// would print the wrong day on every board whose offset crosses one.
func TestRepeatWarningsSayTheAnchorIsOffTheLattice(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC))

	// 2026-09-18 is a Friday; the rule lands on Mondays.
	off := mustAddRepeating(t, a, "金曜起点", "2026-09-18", "weekly on mon", AddOpts{})
	ws := a.RepeatWarnings(off)
	if len(ws) != 1 {
		t.Fatalf("RepeatWarnings = %q, want exactly the off-lattice note", ws)
	}
	if !strings.Contains(ws[0], "not a date this rule lands on") {
		t.Errorf("note says nothing about the anchor: %q", ws[0])
	}
	if !strings.Contains(ws[0], "2026-09-21 23:59 +09:00") {
		t.Errorf("note does not name the rule's own first date in the board's calendar: %q", ws[0])
	}

	on := mustAddRepeating(t, a, "月曜起点", "2026-09-21", "weekly on mon", AddOpts{})
	if ws := a.RepeatWarnings(on); len(ws) != 0 {
		t.Errorf("an anchor ON the lattice was warned about: %q", ws)
	}

	// `monthly on 31` anchored on the 15th is both at once: it skips the short
	// months AND does not land on its own anchor.
	both := mustAddRepeating(t, a, "両方", "2026-03-15", "monthly on 31", AddOpts{})
	if ws := a.RepeatWarnings(both); len(ws) != 2 {
		t.Errorf("RepeatWarnings = %q, want the skip note and the off-lattice note", ws)
	}
}

// The anchor is a live occurrence OUTSIDE the series, so COUNT — which bounds
// the rule's own lattice slots — hands out n MORE after it: `for 3 times`
// anchored off the lattice is FOUR tasks, where the same rule anchored on it is
// three (TestABoundedSeriesRunsOut).
//
// bite-exempt: characterization. It pins shipped behaviour this change
// DOCUMENTS rather than changes; the fix that ships with it is the bind-time
// note, which TestRepeatWarningsSayTheAnchorIsOffTheLattice bites.
func TestAnOffLatticeAnchorSpendsOneOccurrenceMoreThanTheCount(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC))
	// 2026-09-18 is a Friday; the rule lands on Mondays.
	cur := mustAddRepeating(t, a, "3 回だけ", "2026-09-18", "weekly on mon for 3 times", AddOpts{})

	want := []string{"2026-09-21", "2026-09-28", "2026-10-05"}
	tasks := 1
	for i := 0; i <= len(want); i++ {
		_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		if rep == nil {
			t.Fatalf("close %d reported nothing about the series", i+1)
		}
		if i == len(want) {
			if !rep.Completed {
				t.Errorf("close %d = %+v, want the series spent", i+1, rep)
			}
			break
		}
		if rep.Created == nil {
			t.Fatalf("close %d ended the series early: %+v", i+1, rep)
		}
		if got := rep.Due.In(jst).Format("2006-01-02"); got != want[i] {
			t.Errorf("successor %d due %s, want %s", i+1, got, want[i])
		}
		next, _, gerr := a.Get(*rep.Created)
		if gerr != nil {
			t.Fatal(gerr)
		}
		cur, tasks = next, tasks+1
	}
	if tasks != 4 {
		t.Errorf("`for 3 times` anchored off the lattice produced %d tasks, want 4 (the anchor plus the rule's 3)", tasks)
	}
}

// santiago is the board calendar these two tests need: one day a year it has no
// local MIDNIGHT (it springs forward AT 00:00), which is the day the expansion's
// day grid used to slide off by one. The board-level twin of recur's
// TestOccurrencesLandOnADayWithNoLocalMidnight.
func santiago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tzdata for America/Santiago: %v", err)
	}
	return loc
}

// A `weekly on sun` chore was promised for a SATURDAY: the successor's day came
// off a grid that had shifted at the transition, and a weekday rule produces no
// duplicate to notice it by — the board simply said the wrong day.
func TestSuccessorKeepsItsWeekdayAcrossADayWithNoLocalMidnight(t *testing.T) {
	loc := santiago(t)
	// 2027-09-05 is the Sunday Santiago has no midnight on; this close is on
	// time, on the Sunday before it.
	a, _ := newAppWith(at(time.Date(2027, 8, 29, 15, 0, 0, 0, time.UTC)), zone(loc))
	pred := mustAddRepeating(t, a, "riego", "2027-08-29", "weekly on sun", AddOpts{})

	_, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Due == nil {
		t.Fatalf("no successor reported: %+v", rep)
	}
	got := rep.Due.In(loc)
	if got.Format("2006-01-02 15:04:05") != "2027-09-05 23:59:59" {
		t.Errorf("successor due = %s (%s), want 2027-09-05 23:59:59 (Sunday)", got.Format(time.RFC3339), got.Weekday())
	}
}

// The lapse count crosses the same day, so a late close under-reported it by
// one: four occurrences went by, three were named. The due sits on the day the
// grid used to emit TWICE — from any earlier day the duplicate silently made the
// count add up again, which is why this defect could sit on a board unnoticed.
func TestLateCloseCountsTheDayWithNoLocalMidnight(t *testing.T) {
	loc := santiago(t)
	// Promised for 4 September 2027, cleared at noon on the 9th: 5 September —
	// the day this zone has no midnight — is one of the four that lapsed.
	a, _ := newAppWith(at(time.Date(2027, 9, 9, 15, 0, 0, 0, time.UTC)), zone(loc))
	pred := mustAddRepeating(t, a, "riego", "2027-09-04", "daily", AddOpts{})

	_, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Due == nil {
		t.Fatalf("no successor reported: %+v", rep)
	}
	if rep.Skipped != 4 {
		t.Errorf("skipped = %d, want 4 (5..8 September, the 5th included)", rep.Skipped)
	}
	if got := rep.Due.In(loc).Format("2006-01-02 15:04:05"); got != "2027-09-10 23:59:59" {
		t.Errorf("successor due = %s, want 2027-09-10 23:59:59", got)
	}
}

// A rule refused at bind time on `add` names NO task: the id was minted before
// the rule was checked and no shard or body will ever carry it, so a tool that
// trusted the envelope's subject grabbed a task that does not exist (t-qps2).
// On `set` the task exists and the refusal names it.
func TestRepeatRefusalSubjectNamesOnlyAnExistingTask(t *testing.T) {
	a := newApp()
	due := "2026-10-01"
	_, err := a.Add("rep", AddOpts{Due: due, Repeat: "every 0 weeks"})
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("add with a bad rule = %v, want exit 2", err)
	}
	if fe.Subject != "" {
		t.Errorf("add's refusal names a task that was never created: subject %q", fe.Subject)
	}
	tk, err := a.Add("plain", AddOpts{Due: due})
	if err != nil {
		t.Fatal(err)
	}
	bad := "every 0 weeks"
	_, _, _, err = a.Set(tk.ID, SetOpts{Repeat: &bad})
	if fe := core.AsError(err); fe == nil || fe.Subject != tk.ID {
		t.Errorf("set's refusal must name the existing task: %+v", fe)
	}
	_ = time.Now
}
