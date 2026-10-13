package app

import (
	"strings"
	"testing"
)

// A lane change carries the task's priority along, but the destination lane
// respaces on its own, so the value may already be held there (the real
// board's t-8jpv/t-rgky): the mover lands at the lane's tail and says so.
func TestMoveRelocatesPriorityHeldInDestinationLane(t *testing.T) {
	a := newApp()
	anchor, err := a.Add("A", AddOpts{Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	p := anchor.Priority
	mover, err := a.Add("C", AddOpts{Status: "ready", Priority: &p})
	if err != nil {
		t.Fatal(err)
	}
	a.TakeNotes()
	moved, _, err := a.MoveMany([]string{mover.ID}, "backlog", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := moved[0].Priority; got == p || got != p+a.Cfg.PriorityStep {
		t.Fatalf("priority after move = %d, want the lane tail %d (not the tie %d)", got, p+a.Cfg.PriorityStep, p)
	}
	notes := a.TakeNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "placed at the lane's tail") {
		t.Fatalf("relocation note = %q, want one note naming the tail placement", notes)
	}
}

func TestMoveKeepsPriorityWhenDestinationIsFree(t *testing.T) {
	a := newApp()
	if _, err := a.Add("A", AddOpts{Status: "backlog"}); err != nil {
		t.Fatal(err)
	}
	p := 300
	mover, err := a.Add("C", AddOpts{Status: "ready", Priority: &p})
	if err != nil {
		t.Fatal(err)
	}
	a.TakeNotes()
	moved, _, err := a.MoveMany([]string{mover.ID}, "backlog", nil)
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].Priority != 300 {
		t.Fatalf("priority after move = %d, want 300 kept (no tie)", moved[0].Priority)
	}
	if notes := a.TakeNotes(); len(notes) != 0 {
		t.Fatalf("unexpected notes: %q", notes)
	}
}

func TestSetStatusRelocatesPriorityHeldInDestinationLane(t *testing.T) {
	a := newApp()
	anchor, err := a.Add("A", AddOpts{Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	p := anchor.Priority
	mover, err := a.Add("C", AddOpts{Status: "ready", Priority: &p})
	if err != nil {
		t.Fatal(err)
	}
	lane := "backlog"
	after, _, _, err := a.Set(mover.ID, SetOpts{Status: &lane})
	if err != nil {
		t.Fatal(err)
	}
	if after.Priority == p {
		t.Fatalf("set -s left the tie at %d in %q", p, lane)
	}
}

// An explicit priority is honoured as given — never rounded — but a tie it
// creates is disclosed, on every path that takes one.
func TestExplicitPriorityTieIsDisclosed(t *testing.T) {
	a := newApp()
	anchor, err := a.Add("A", AddOpts{Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	p := anchor.Priority
	a.TakeNotes()
	added, err := a.Add("Y", AddOpts{Status: "backlog", Priority: &p})
	if err != nil {
		t.Fatal(err)
	}
	if added.Priority != p {
		t.Fatalf("add -p %d was rounded to %d", p, added.Priority)
	}
	if notes := a.TakeNotes(); len(notes) != 1 || !strings.Contains(notes[0], "shared with "+anchor.ID) {
		t.Fatalf("add -p tie note = %q", notes)
	}
	q := p + 50
	if _, err := a.Reorder(added.ID, q); err != nil {
		t.Fatal(err)
	}
	if notes := a.TakeNotes(); len(notes) != 0 {
		t.Fatalf("reorder onto a free priority noted %q", notes)
	}
	if _, err := a.Reorder(added.ID, p); err != nil {
		t.Fatal(err)
	}
	if notes := a.TakeNotes(); len(notes) != 1 || !strings.Contains(notes[0], "shared with "+anchor.ID) {
		t.Fatalf("reorder tie note = %q", notes)
	}
	if _, _, _, err := a.Set(anchor.ID, SetOpts{Priority: &q}); err != nil {
		t.Fatal(err)
	}
	if notes := a.TakeNotes(); len(notes) != 0 {
		t.Fatalf("set -p onto a free priority noted %q", notes)
	}
	if _, _, _, err := a.Set(anchor.ID, SetOpts{Priority: &p}); err != nil {
		t.Fatal(err)
	}
	if notes := a.TakeNotes(); len(notes) != 1 || !strings.Contains(notes[0], "shared with "+added.ID) {
		t.Fatalf("set -p tie note = %q", notes)
	}
}
