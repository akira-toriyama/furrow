package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// `set -s done` runs the repeat edits of the SAME write first, so the operator's
// explicit intent wins over the rule the task happened to arrive with.
func TestSetDoneHonorsTheRepeatEditInTheSameWrite(t *testing.T) {
	done := "done"

	t.Run("--clear-repeat ends the series instead of handing it on", func(t *testing.T) {
		a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "monthly", AddOpts{})
		if _, _, _, err := a.Set(task.ID, SetOpts{Status: &done, ClearRepeat: true}); err != nil {
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
		closed, _, _, err := a.Set(task.ID, SetOpts{Status: &done, Repeat: &weekly})
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
		closed, _, _, err := a.Set(task.ID, SetOpts{Status: &done, AddLabels: []string{"chore"}})
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
	closed, reps, err := a.MoveMany(ids, a.Cfg.DoneLane, nil)
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
	_, reps, err := a.SetMany(ids, SetOpts{Status: &done})
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
	if _, _, _, err := a.Set(task.ID, SetOpts{Repeat: &rule}); err == nil {
		t.Error("a live rule was armed on a closed task")
	}
	// The same edit WITH a reopen is the supported way round.
	lane := a.Cfg.DefaultLane
	if _, _, _, err := a.Set(task.ID, SetOpts{Status: &lane, Repeat: &rule}); err != nil {
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
	if _, _, _, err := a.Set(task.ID, SetOpts{Repeat: &rule}); err != nil {
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
		if _, _, _, err := a.Set(task.ID, SetOpts{Status: &done, Repeat: &rule}); err == nil {
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
