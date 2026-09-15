package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// newRepeatApp pins the clock and the board calendar, the way a real board that
// declares [due].timezone runs: a UTC clock and a +09:00 calendar, which is the
// only shape that catches a due bound off the process zone.
func newRepeatApp(now time.Time) *App {
	cfg := config.Default()
	cfg.DueTimezone = jst
	st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
	return NewWithStore(st, cfg, &fixedClock{t: now})
}

func mustAddRepeating(t *testing.T, a *App, title, due, rule string, o AddOpts) *core.Task {
	t.Helper()
	o.Due, o.Repeat = due, rule
	task, err := a.Add(title, o)
	if err != nil {
		t.Fatalf("Add(%q, %q): %v", due, rule, err)
	}
	return task
}

func other(t *testing.T, a *App, closed string) *core.Task {
	t.Helper()
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if tasks[i].ID != closed {
			return &tasks[i]
		}
	}
	t.Fatalf("no task other than %s on the board", closed)
	return nil
}

func TestCloseGeneratesTheNextOccurrence(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)) // 12:00 JST on 2 March
	v, e := 4, 2
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{
		Labels: []string{"chore"}, Refs: []string{"docs/x.md:1"},
		Value: &v, Effort: &e, Checklist: []string{"tap on", "tap off"},
	})
	if _, err := a.Check(pred.ID, 1, true); err != nil {
		t.Fatalf("tick a box: %v", err)
	}

	closed, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Created == nil {
		t.Fatalf("no successor reported: %+v", rep)
	}
	succ := other(t, a, closed.ID)
	if succ.ID != *rep.Created {
		t.Fatalf("report names %s, board has %s", *rep.Created, succ.ID)
	}

	t.Run("the closed occurrence keeps nothing of the rule", func(t *testing.T) {
		if closed.Repeat != "" || closed.RepeatAnchor != nil {
			t.Errorf("predecessor still carries the rule: %q / %v", closed.Repeat, closed.RepeatAnchor)
		}
		if closed.Status != a.Cfg.DoneLane || closed.Closed == nil {
			t.Errorf("predecessor = %s, closed=%v; want the done lane, stamped", closed.Status, closed.Closed)
		}
		if closed.Due == nil || !closed.Due.Equal(time.Date(2026, 3, 1, 14, 59, 59, 0, time.UTC)) {
			t.Errorf("predecessor due = %v; the close must not re-date the occurrence it settled", closed.Due)
		}
	})

	t.Run("the successor is promised for the next occurrence", func(t *testing.T) {
		want := time.Date(2026, 4, 1, 14, 59, 59, 0, time.UTC) // 1 April 23:59:59 +09:00
		if succ.Due == nil || !succ.Due.Equal(want) {
			t.Errorf("successor due = %v, want %s", succ.Due, want)
		}
		if succ.Repeat != "FREQ=MONTHLY" {
			t.Errorf("successor rule = %q, want the predecessor's verbatim", succ.Repeat)
		}
		if succ.RepeatAnchor == nil || !succ.RepeatAnchor.Equal(time.Date(2026, 3, 1, 14, 59, 59, 0, time.UTC)) {
			t.Errorf("successor anchor = %v, want the SERIES start (unchanged)", succ.RepeatAnchor)
		}
		if rep.Skipped != 0 {
			t.Errorf("skipped = %d, want 0 — closed inside its own cycle", rep.Skipped)
		}
	})

	t.Run("it inherits what describes the chore", func(t *testing.T) {
		if succ.Title != pred.Title ||
			strings.Join(succ.Labels, ",") != "chore" || strings.Join(succ.Refs, ",") != "docs/x.md:1" {
			t.Errorf("successor = %+v, want the predecessor's descriptive fields", succ)
		}
		if succ.Value == nil || *succ.Value != v || succ.Effort == nil || *succ.Effort != e {
			t.Errorf("estimates = %v/%v, want %d/%d", succ.Value, succ.Effort, v, e)
		}
	})

	t.Run("it is born where furrow puts a task it creates", func(t *testing.T) {
		if succ.Status != a.Cfg.DefaultLane {
			t.Errorf("successor lane = %q, want the default lane %q — `next` does not read due, so a"+
				" ready-born chore would sit in next for the whole cycle", succ.Status, a.Cfg.DefaultLane)
		}
		if succ.Closed != nil || succ.Reviewed != nil || len(succ.Deps) != 0 {
			t.Errorf("successor carries settled state: closed=%v reviewed=%v deps=%v",
				succ.Closed, succ.Reviewed, succ.Deps)
		}
	})

	t.Run("the checklist is the chore's steps, not this run's ticks", func(t *testing.T) {
		if len(succ.Checklist) != 2 {
			t.Fatalf("checklist = %+v, want both items copied", succ.Checklist)
		}
		for _, it := range succ.Checklist {
			if it.Done {
				t.Errorf("item %q came over ticked", it.Text)
			}
		}
	})

	t.Run("the body carries over under a back-link", func(t *testing.T) {
		body, err := a.Store.LoadBody(succ.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(body, "previous: [["+pred.ID+"]]\n") {
			t.Errorf("successor body does not open with the back-link:\n%s", body)
		}
		if !strings.Contains(body, "水やり") {
			t.Errorf("successor body lost the predecessor's prose:\n%s", body)
		}
	})
}

// The invariant the whole design rests on: a rule is held by exactly one task,
// so closing twice — or reopening and closing again — cannot mint a second
// occurrence. furrow's no-op detection compares one task's own shard bytes and
// structurally cannot see a side effect in another file.
func TestClosingAgainMintsNothing(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})

	if _, err := a.Done(pred.ID); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		tasks, err := a.List(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return len(tasks)
	}
	if n := count(); n != 2 {
		t.Fatalf("after one close: %d tasks, want 2", n)
	}

	if _, err := a.Done(pred.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("closing an already-closed occurrence minted one: %d tasks", n)
	}

	if _, err := a.Move(pred.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(pred.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("reopen then close minted one: %d tasks, want 2", n)
	}
}

// Closing late skips the cycles that went by rather than minting one task per
// lapsed month, and says how many.
func TestALateCloseSkipsAndSaysSo(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 5, 15, 3, 0, 0, 0, time.UTC)) // 15 May
	pred := mustAddRepeating(t, a, "月次レビュー", "2026-03-01", "monthly", AddOpts{})

	closed, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Created == nil {
		t.Fatalf("no successor: %+v", rep)
	}
	want := time.Date(2026, 6, 1, 14, 59, 59, 0, time.UTC)
	if rep.Due == nil || !rep.Due.Equal(want) {
		t.Errorf("next due = %v, want %s (the first occurrence strictly after now)", rep.Due, want)
	}
	if rep.Skipped != 2 {
		t.Errorf("skipped = %d, want 2 (1 April and 1 May)", rep.Skipped)
	}
	if succ := other(t, a, closed.ID); succ.Due == nil || !succ.Due.Equal(want) {
		t.Errorf("board disagrees with the report: %v", succ.Due)
	}
}

func TestSeriesEndReportsCompletionAndMintsNothing(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)) // 12:00 JST on 1 March: on time
	// `for 2 times` binds (there IS one more occurrence) and is spent by the
	// SECOND close — when both are on time. A close a day LATE settles that
	// day's slot too, so it can spend a bounded series a close early; that case
	// is pinned below. `for 1 times` is refused at bind time — a rule that would
	// end on the very next close is a due date, not a recurrence.
	pred := mustAddRepeating(t, a, "2 回だけ", "2026-03-01", "daily for 2 times", AddOpts{})
	first, rep0, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep0 == nil || rep0.Created == nil {
		t.Fatalf("the first close ended the series early: %+v", rep0)
	}
	live, _, err := a.Get(*rep0.Created)
	if err != nil {
		t.Fatal(err)
	}
	_ = first

	closed, rep, err := a.moveOne(live.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || !rep.Completed {
		t.Fatalf("report = %+v, want completed", rep)
	}
	if rep.Created != nil || rep.Due != nil {
		t.Errorf("a completed series reported a successor: %+v", rep)
	}
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("%d tasks, want the two closed occurrences and no third", len(tasks))
	}
	if closed.Repeat != "" {
		t.Errorf("a spent rule stayed on the task: %q", closed.Repeat)
	}

	t.Run("a day-late close settles that day's slot too, and spends the series", func(t *testing.T) {
		a := newRepeatApp(time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC))
		pred := mustAddRepeating(t, a, "2 回だけ", "2026-03-01", "daily for 2 times", AddOpts{})
		a.Clock = &fixedClock{t: time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)} // 12:00 JST on 2 March
		_, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		if rep == nil || !rep.Completed || rep.Skipped != 0 {
			t.Fatalf("report = %+v, want completed with nothing lapsed: the 2 March slot is the day of the close, settled rather than skipped", rep)
		}
	})
}

// `set -s done` is a close like any other: the triage shortcut must not be a
// hole in the series.
func TestSetToDoneAlsoAdvancesTheSeries(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})

	done := a.Cfg.DoneLane
	if _, _, err := a.Set(pred.ID, SetOpts{Status: &done}); err != nil {
		t.Fatal(err)
	}
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("%d tasks, want 2 — `set -s done` skipped the series", len(tasks))
	}
}

// done --note appends to the body BEFORE the close. The note belongs to the
// occurrence that earned it, so the successor must not inherit it.
func TestTheSuccessorDoesNotInheritThisClosesNote(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})

	closed, err := a.DoneNote(pred.ID, "tap was stuck this time")
	if err != nil {
		t.Fatal(err)
	}
	predBody, err := a.Store.LoadBody(closed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(predBody, "tap was stuck") {
		t.Fatalf("the note did not land on the closed occurrence:\n%s", predBody)
	}
	succBody, err := a.Store.LoadBody(other(t, a, closed.ID).ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(succBody, "tap was stuck") {
		t.Errorf("this close's note rode into the next occurrence:\n%s", succBody)
	}
}

// A body that has recurred many times still opens with exactly one back-link.
func TestBackLinksDoNotStack(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	cur := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})

	// Follow the series by the id each close reports, so every past occurrence
	// can stay on the board — which is the state that would stack the links.
	for i := 0; i < 3; i++ {
		_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		if rep == nil || rep.Created == nil {
			t.Fatalf("cycle %d produced no successor", i+1)
		}
		next, _, gerr := a.Get(*rep.Created)
		if gerr != nil {
			t.Fatalf("cycle %d: %s is not on the board: %v", i+1, *rep.Created, gerr)
		}
		cur = next
	}
	body, err := a.Store.LoadBody(cur.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(body, "[["); n != 1 {
		t.Errorf("%d back-links after 3 cycles, want 1:\n%s", n, body)
	}
}

func TestRepeatRefusesWhatItCannotAnchor(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))

	t.Run("--repeat with no --due", func(t *testing.T) {
		if _, err := a.Add("水やり", AddOpts{Repeat: "monthly"}); err == nil {
			t.Error("a rule with no first occurrence was accepted")
		}
	})

	t.Run("--clear-due on a repeating task", func(t *testing.T) {
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
		if _, _, err := a.Set(task.ID, SetOpts{ClearDue: true}); err == nil {
			t.Error("dropping the anchor of a live series was accepted")
		}
	})

	t.Run("--clear-repeat then --clear-due is the way out", func(t *testing.T) {
		task := mustAddRepeating(t, a, "水やり2", "2026-03-01", "monthly", AddOpts{})
		if _, _, err := a.Set(task.ID, SetOpts{ClearRepeat: true, ClearDue: true}); err != nil {
			t.Errorf("clearing both at once was refused: %v", err)
		}
	})
}

// The snooze moves THIS occurrence. The anchor is what the rule counts from, so
// it must not move with it — otherwise furrow's own remedy for an overdue task
// would silently re-lattice every occurrence after it.
func TestSnoozeDoesNotMoveTheAnchor(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
	anchor := *task.RepeatAnchor

	plus := "+3d"
	snoozed, _, err := a.Set(task.ID, SetOpts{Due: &plus})
	if err != nil {
		t.Fatal(err)
	}
	if snoozed.RepeatAnchor == nil || !snoozed.RepeatAnchor.Equal(anchor) {
		t.Fatalf("anchor moved to %v, want %v", snoozed.RepeatAnchor, anchor)
	}
	if snoozed.Due == nil || snoozed.Due.Equal(anchor) {
		t.Fatalf("due did not move: %v", snoozed.Due)
	}

	// The next occurrence still comes off the original lattice: the 1st.
	_, rep, err := a.moveOne(task.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 1, 14, 59, 59, 0, time.UTC)
	if rep == nil || rep.Due == nil || !rep.Due.Equal(want) {
		t.Errorf("next due = %v, want %s — the snooze re-latticed the series", rep, want)
	}
}

// The defect the review caught: `recur.Next` was asked for the first occurrence
// after NOW, but a bare `--due` binds 23:59:59, so a close during the working
// day was handed back the occurrence it had just completed. The series advanced
// only when the operator was late — and a daily chore could never be cleared.
func TestAnOnTimeOrEarlyCloseAdvancesExactlyOneStep(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		due  string
		rule string
		want time.Time
	}{
		{"closed during the day it is due", time.Date(2026, 3, 1, 5, 32, 0, 0, time.UTC),
			"2026-03-01", "daily", time.Date(2026, 3, 2, 14, 59, 59, 0, time.UTC)},
		// Anchored on the 31st, so April is SKIPPED (RFC 5545) — the next
		// occurrence after the one settled is 31 May, not 30 April.
		{"closed weeks early", time.Date(2026, 3, 1, 5, 0, 0, 0, time.UTC),
			"2026-03-31", "monthly", time.Date(2026, 5, 31, 14, 59, 59, 0, time.UTC)},
		// Late: now (3 March 14:00 JST) is past the settled due (1 March), so the
		// close settles the day it lands on and the answer is TOMORROW's — one
		// late afternoon must not make every following afternoon late as well.
		{"closed after the due instant settles the day of the close", time.Date(2026, 3, 3, 5, 0, 0, 0, time.UTC),
			"2026-03-01", "daily", time.Date(2026, 3, 4, 14, 59, 59, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newRepeatApp(c.now)
			task := mustAddRepeating(t, a, "水やり", c.due, c.rule, AddOpts{})
			_, rep, err := a.moveOne(task.ID, a.Cfg.DoneLane)
			if err != nil {
				t.Fatal(err)
			}
			if rep == nil || rep.Due == nil {
				t.Fatalf("no successor: %+v", rep)
			}
			if !rep.Due.Equal(c.want) {
				t.Errorf("next due = %s, want %s", rep.Due.Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
			if rep.Due.Equal(*task.Due) {
				t.Error("the successor repeats the occurrence that was just closed")
			}
		})
	}
}

// The corollary: a bounded series must actually run out. It could not before,
// because every on-time close re-issued the same occurrence.
func TestABoundedSeriesRunsOut(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 1, 5, 0, 0, 0, time.UTC))
	cur := mustAddRepeating(t, a, "3 回だけ", "2026-03-01", "daily for 3 times", AddOpts{})

	for i := 1; i <= 2; i++ {
		_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		if rep == nil || rep.Created == nil {
			t.Fatalf("close %d ended the series early: %+v", i, rep)
		}
		next, _, gerr := a.Get(*rep.Created)
		if gerr != nil {
			t.Fatal(gerr)
		}
		cur = next
	}
	_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || !rep.Completed {
		t.Errorf("the third close = %+v, want completed — COUNT never spent", rep)
	}
}

// `set -s done` runs the repeat edits of the SAME write first, so the operator's
// explicit intent wins over the rule the task happened to arrive with.
func TestSetDoneHonorsTheRepeatEditInTheSameWrite(t *testing.T) {
	done := "done"

	t.Run("--clear-repeat ends the series instead of handing it on", func(t *testing.T) {
		a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
		if _, _, err := a.Set(task.ID, SetOpts{Status: &done, ClearRepeat: true}); err != nil {
			t.Fatal(err)
		}
		tasks, err := a.List(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 1 {
			t.Errorf("%d tasks, want 1 — --clear-repeat still minted a successor", len(tasks))
		}
	})

	t.Run("--repeat rebinds before the close, and only one task holds a rule", func(t *testing.T) {
		a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
		weekly := "weekly"
		closed, _, err := a.Set(task.ID, SetOpts{Status: &done, Repeat: &weekly})
		if err != nil {
			t.Fatal(err)
		}
		if closed.Repeat != "" {
			t.Errorf("the closed task kept a rule (%q) — the series forked", closed.Repeat)
		}
		succ := other(t, a, closed.ID)
		if succ.Repeat != "FREQ=WEEKLY" {
			t.Errorf("successor rule = %q, want the rule this write asked for", succ.Repeat)
		}
	})

	t.Run("a successor born beside other edits inherits the edited values", func(t *testing.T) {
		a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
		closed, _, err := a.Set(task.ID, SetOpts{Status: &done, AddLabels: []string{"chore"}})
		if err != nil {
			t.Fatal(err)
		}
		succ := other(t, a, closed.ID)
		if len(succ.Labels) != 1 || succ.Labels[0] != "chore" {
			t.Errorf("successor labels = %v, want the label this write added", succ.Labels)
		}
	})
}

// A refusal partway through a batch must leave NOTHING behind — neither the
// note on the bodies it already reached, nor a body file for a successor that
// was never inserted.
func TestARefusedBatchLeavesNothingBehind(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	ok1 := mustAddRepeating(t, a, "first", "2026-03-01", "monthly", AddOpts{})
	bad, err := a.Add("broken", AddOpts{Due: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	// A shard furrow would never write: a rule with no anchor. planRepeat
	// refuses it, and the refusal must undo nothing because nothing happened.
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	bt, _ := idx.Find(bad.ID)
	bt.Repeat = "FREQ=MONTHLY"
	bt.RepeatAnchor = nil
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}

	before, err := a.Store.LoadBody(ok1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DoneManyNote([]string{ok1.ID, bad.ID}, "closing note"); err == nil {
		t.Fatal("the batch was accepted despite an unusable rule")
	}
	after, err := a.Store.LoadBody(ok1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("a refused batch left the note on an earlier task's body:\n%s", after)
	}
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("%d tasks after a refused batch, want the original 2", len(tasks))
	}
	ids, err := a.Store.ListBodyIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("%d body files, want 2 — a refused write left an orphan for a task that never existed", len(ids))
	}
}

// A task created in the done lane is closed at birth, so a rule on it could
// never fire: a series with no live occurrence, and nothing to say so.
func TestAddRefusesARuleOnATaskClosedAtBirth(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	if _, err := a.Add("x", AddOpts{Status: a.Cfg.DoneLane, Due: "2026-03-01", Repeat: "monthly"}); err == nil {
		t.Error("a rule was parked on a task that can never fire it")
	}
}

// The batch plans every successor BEFORE any is inserted, so the index cannot
// answer for the batch's own ids. Two successors drawing the same id would be
// refused by both stores at save time — a hard failure on a routine close.
func TestABatchOfClosesReservesItsOwnIDs(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, mustAddRepeating(t, a, "chore", "2026-03-01", "monthly", AddOpts{}).ID)
	}
	closed, reps, err := a.MoveManySeries(ids, a.Cfg.DoneLane, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 5 {
		t.Fatalf("closed %d, want 5", len(closed))
	}
	seen := map[string]bool{}
	for i, r := range reps {
		if r == nil || r.Created == nil {
			t.Fatalf("task %d produced no successor", i)
		}
		if seen[*r.Created] {
			t.Errorf("two successors share the id %s", *r.Created)
		}
		seen[*r.Created] = true
	}
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 10 {
		t.Errorf("%d tasks, want 10 (5 closed + 5 successors)", len(tasks))
	}
}

// A close is a close whatever the arity: the batch form owes the same receipt.
func TestBatchSetToDoneReportsEverySeries(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	var ids []string
	for i := 0; i < 2; i++ {
		ids = append(ids, mustAddRepeating(t, a, "chore", "2026-03-01", "monthly", AddOpts{}).ID)
	}
	done := a.Cfg.DoneLane
	_, reps, err := a.SetManySeries(ids, SetOpts{Status: &done})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("%d reports, want 2", len(reps))
	}
	for i, r := range reps {
		if r == nil || r.Created == nil {
			t.Errorf("task %d advanced its series with no receipt: %+v", i, r)
		}
	}
}

// A closed task can never be closed again, so a rule on it could never fire —
// the state `add -s done --repeat` refuses, reached the other way round.
func TestSetRefusesARuleOnAnAlreadyClosedTask(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task, err := a.Add("x", AddOpts{Due: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(task.ID); err != nil {
		t.Fatal(err)
	}
	rule := "monthly"
	if _, _, err := a.Set(task.ID, SetOpts{Repeat: &rule}); err == nil {
		t.Error("a live rule was armed on a closed task")
	}
	// The same edit WITH a reopen is the supported way round.
	lane := a.Cfg.DefaultLane
	if _, _, err := a.Set(task.ID, SetOpts{Status: &lane, Repeat: &rule}); err != nil {
		t.Errorf("reopening and binding in one write was refused: %v", err)
	}
}

// A rule with no due has no occurrence to advance from; without this guard the
// search falls back to the wall clock and re-mints what was just closed.
func TestARuleWithNoDueIsRefusedAndLinted(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task := mustAddRepeating(t, a, "x", "2026-03-01", "monthly", AddOpts{})

	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	bt, _ := idx.Find(task.ID)
	bt.Due = nil // a shard furrow would not write
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Done(task.ID); err == nil {
		t.Error("a close advanced a series with no occurrence to advance from")
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Code == "repeat-invalid" && p.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Error("lint did not report the unusable rule")
	}
}

// Nothing is duplicated per cycle: the store holds ONE copy of the blob however
// many times the chore has come round. The copy-per-cycle version of this grew
// the filename by an id every close and wedged the series at ~40.
func TestACarriedAttachmentIsNotDuplicatedPerCycle(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	cur := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
	name, err := a.Store.SaveAsset(cur.ID, "note.txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddNote(cur.ID, "![n](assets/"+name+")"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		_, rep, err := a.moveOne(cur.ID, a.Cfg.DoneLane)
		if err != nil {
			t.Fatalf("cycle %d: %v", i+1, err)
		}
		next, _, err := a.Get(*rep.Created)
		if err != nil {
			t.Fatal(err)
		}
		cur = next
		body, err := a.Store.LoadBody(cur.ID)
		if err != nil {
			t.Fatal(err)
		}
		if refs := core.ExtractAssetRefs(body); len(refs) != 1 || refs[0] != name {
			t.Fatalf("cycle %d: refs = %v, want the one original attachment %q", i+1, refs, name)
		}
	}
	assets, err := a.Store.ListAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Errorf("%d assets after 4 cycles, want 1 — the blob is being duplicated", len(assets))
	}
}

// The back-link carries a `previous: ` marker, and ONLY that shape is replaced.
// A bare leading `[[t-…]]` is something an operator may well have written.
func TestAnOperatorsOwnLeadingLinkSurvives(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task, err := a.Add("水やり", AddOpts{Due: "2026-03-01", Body: "[[t-other]]\n\nsee the task above.\n"})
	if err != nil {
		t.Fatal(err)
	}
	rule := "monthly"
	if _, _, err := a.Set(task.ID, SetOpts{Repeat: &rule}); err != nil {
		t.Fatal(err)
	}
	closed, err := a.Done(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := a.Store.LoadBody(other(t, a, closed.ID).ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "[[t-other]]") {
		t.Errorf("the operator's own link was overwritten:\n%s", body)
	}
	if !strings.HasPrefix(body, "previous: [["+closed.ID+"]]") {
		t.Errorf("the back-link is missing or unmarked:\n%s", body)
	}
}

// Only a TRANSITION into the done lane advances a series. `set <closed-id> -s
// done --repeat X` would otherwise arm a rule and spend it in the same write,
// minting a fresh occurrence on every invocation.
func TestReArmingAClosedTaskDoesNotMint(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task, err := a.Add("x", AddOpts{Due: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(task.ID); err != nil {
		t.Fatal(err)
	}
	done, rule := a.Cfg.DoneLane, "monthly"
	for i := 0; i < 3; i++ {
		if _, _, err := a.Set(task.ID, SetOpts{Status: &done, Repeat: &rule}); err == nil {
			t.Fatalf("attempt %d: arming a closed task was accepted", i+1)
		}
	}
	tasks, err := a.List(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("%d tasks, want 1 — re-closing minted occurrences", len(tasks))
	}
}

// A rule that would end on the very next close is a due date, not a recurrence.
func TestARuleDeadOnArrivalIsRefused(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	for _, spec := range []string{"daily for 1 times", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30"} {
		if _, err := a.Add("x", AddOpts{Due: "2026-03-01", Repeat: spec}); err == nil {
			t.Errorf("%q was bound, and the first close would end the series", spec)
		}
	}
}

// A board whose default lane IS its done lane would bear every occurrence
// closed at birth, holding a rule nothing will ever fire.
func TestRecurrenceRefusesABoardThatBearsClosedTasks(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	a.Cfg.DefaultLane = a.Cfg.DoneLane
	if _, err := a.Add("x", AddOpts{Due: "2026-03-01", Repeat: "monthly"}); err == nil {
		t.Error("a rule was bound on a board whose default lane is its done lane")
	}
}

// A successor is born in a DIFFERENT lane from the occurrence it follows, and
// priority is relative to a lane — so copying the predecessor's number tied an
// existing task in the default lane and sorted ahead of it. It is appended
// exactly as `add` appends, and a batch of closes appends each in turn.
func TestASuccessorIsAppendedToTheDefaultLane(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	for _, title := range []string{"P1", "P2"} {
		if _, err := a.Add(title, AddOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	pred := mustAddRepeating(t, a, "水やり", "2026-03-01", "daily", AddOpts{Status: "ready"})
	if pred.Priority != a.Cfg.PriorityDefault {
		t.Fatalf("predecessor priority = %d, want the head of its own lane (%d)", pred.Priority, a.Cfg.PriorityDefault)
	}

	closed, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	succ, _, err := a.Get(*rep.Created)
	if err != nil {
		t.Fatal(err)
	}
	wantPrio := a.Cfg.PriorityDefault + 2*a.Cfg.PriorityStep // after P1 and P2
	if succ.Status != a.Cfg.DefaultLane || succ.Priority != wantPrio {
		t.Errorf("successor = %s/%d, want appended to %s at %d (not the predecessor's %d)",
			succ.Status, succ.Priority, a.Cfg.DefaultLane, wantPrio, closed.Priority)
	}

	t.Run("a batch appends each successor in turn", func(t *testing.T) {
		x := mustAddRepeating(t, a, "x", "2026-03-01", "daily", AddOpts{Status: "ready"})
		y := mustAddRepeating(t, a, "y", "2026-03-01", "daily", AddOpts{Status: "ready"})
		_, reps, err := a.moveMany([]string{x.ID, y.ID}, a.Cfg.DoneLane, "")
		if err != nil {
			t.Fatal(err)
		}
		sx, _, _ := a.Get(*reps[0].Created)
		sy, _, _ := a.Get(*reps[1].Created)
		if sx.Priority == sy.Priority {
			t.Fatalf("both successors got priority %d — the batch tied them", sx.Priority)
		}
		if sx.Priority != wantPrio+a.Cfg.PriorityStep || sy.Priority != wantPrio+2*a.Cfg.PriorityStep {
			t.Errorf("successors = %d/%d, want %d/%d (appended in close order)",
				sx.Priority, sy.Priority, wantPrio+a.Cfg.PriorityStep, wantPrio+2*a.Cfg.PriorityStep)
		}
	})
}

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
				if _, _, err := a.Set(task.ID, SetOpts{Due: &snooze}); err != nil {
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
	if _, _, err := a.Set(succ.ID, SetOpts{Epic: &open}); err != nil {
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

// newRepeatAppIn is newRepeatApp on a board whose calendar is a REAL IANA zone,
// for the DST shapes a fixed offset cannot express.
func newRepeatAppIn(loc *time.Location, now time.Time) *App {
	cfg := config.Default()
	cfg.DueTimezone = loc
	st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
	return NewWithStore(st, cfg, &fixedClock{t: now})
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
	a := newRepeatAppIn(loc, time.Date(2027, 8, 29, 15, 0, 0, 0, time.UTC))
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
	a := newRepeatAppIn(loc, time.Date(2027, 9, 9, 15, 0, 0, 0, time.UTC))
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
	_, _, err = a.Set(tk.ID, SetOpts{Repeat: &bad})
	if fe := core.AsError(err); fe == nil || fe.Subject != tk.ID {
		t.Errorf("set's refusal must name the existing task: %+v", fe)
	}
	_ = time.Now
}
