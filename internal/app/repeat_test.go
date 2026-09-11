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
		if succ.Title != pred.Title || succ.Priority != pred.Priority ||
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
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	// `for 2 times` binds (there IS one more occurrence) and is spent by the
	// SECOND close. `for 1 times` is refused at bind time — a rule that would end
	// on the very next close is a due date, not a recurrence.
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
		// Late: now (3 March 14:00 JST) wins over the settled due (1 March), and
		// the answer is the first occurrence after NOW — today's, still ahead.
		{"closed after the due instant still jumps forward", time.Date(2026, 3, 3, 5, 0, 0, 0, time.UTC),
			"2026-03-01", "daily", time.Date(2026, 3, 3, 14, 59, 59, 0, time.UTC)},
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
