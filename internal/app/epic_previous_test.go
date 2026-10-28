package app

import (
	"testing"
	"time"
)

// The suggestion is the open, currently-inactive box sharing a repo with the
// box being left that holds the newest activation record — computed from the
// body log, no stored state.
func TestPreviousActiveSuggest(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, err := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	if err != nil {
		t.Fatal(err)
	}
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})

	if _, _, _, err := a.EpicActivate(ea.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicDeactivate(ea.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(eb.ID, "switching"); err != nil {
		t.Fatal(err)
	}

	// Closing b: the only candidate with a record is a.
	prev := a.PreviousActiveSuggest(eb.ID)
	if prev == nil || prev.ID != ea.ID {
		t.Fatalf("previous = %+v, want %s", prev, ea.ID)
	}
	if prev.Title != "box a" || prev.At != "2026-06-25 10:00" {
		t.Errorf("previous = %+v, want title 'box a' at '2026-06-25 10:00'", prev)
	}
}

// A currently-active box is not a return candidate (it needs no returning to),
// and a closed box cannot be one (it cannot be activated). Every box shares
// the left box's repo, so only that rule can exclude them: closing parked b
// while c holds the repo must still answer a.
func TestPreviousActiveSuggestSkipsActiveAndClosed(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	ec, _ := a.EpicAdd("box c", EpicAddOpts{Repos: []string{"me/r1"}})
	ed, _ := a.EpicAdd("box d", EpicAddOpts{Repos: []string{"me/r1"}})

	if _, _, _, err := a.EpicActivate(ea.ID, ""); err != nil { // oldest record
		t.Fatal(err)
	}
	if _, _, err := a.EpicDeactivate(ea.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(ed.ID, ""); err != nil { // newer, then CLOSED
		t.Fatal(err)
	}
	if _, _, err := a.EpicDone(ed.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(eb.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicDeactivate(eb.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(ec.ID, ""); err != nil { // newest, still ACTIVE
		t.Fatal(err)
	}

	prev := a.PreviousActiveSuggest(eb.ID)
	if prev == nil || prev.ID != ea.ID {
		t.Fatalf("previous = %+v, want %s (active/closed boxes are not candidates)", prev, ea.ID)
	}
}

// Only boxes sharing a repo with the box being left are candidates: the
// newest record board-wide is another repo's focus, not where this repo's
// session came from (t-b5q5). Sharing one repo is enough.
func TestPreviousActiveSuggestSameRepoOnly(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	ex, _ := a.EpicAdd("box x", EpicAddOpts{Repos: []string{"me/r2"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	em, _ := a.EpicAdd("box m", EpicAddOpts{Repos: []string{"me/r3", "me/r2"}})

	for _, id := range []string{ea.ID, ex.ID} { // a, then x: x holds the newer record
		if _, _, _, err := a.EpicActivate(id, ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.EpicDeactivate(id); err != nil {
			t.Fatal(err)
		}
		clk.t = clk.t.Add(5 * time.Minute)
	}
	if _, _, _, err := a.EpicActivate(eb.ID, ""); err != nil {
		t.Fatal(err)
	}

	if prev := a.PreviousActiveSuggest(eb.ID); prev == nil || prev.ID != ea.ID {
		t.Fatalf("leaving the me/r1 box: previous = %+v, want %s (x's newer record is me/r2's)", prev, ea.ID)
	}
	if prev := a.PreviousActiveSuggest(em.ID); prev == nil || prev.ID != ex.ID {
		t.Fatalf("leaving the me/r3+me/r2 box: previous = %+v, want %s (one shared repo is enough)", prev, ex.ID)
	}
}

// A box with no repo shares none, so leaving it answers unknown even though
// another box holds a record.
func TestPreviousActiveSuggestNoRepoIsUnknown(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	en, err := a.EpicAdd("box n", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(en.Repos) != 0 {
		t.Fatalf("fixture: box n repos = %v, want none", en.Repos)
	}
	if _, _, _, err := a.EpicActivate(ea.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicDeactivate(ea.ID); err != nil {
		t.Fatal(err)
	}

	if prev := a.PreviousActiveSuggest(en.ID); prev != nil {
		t.Fatalf("previous = %+v, want nil (a repo-less box shares no repo)", prev)
	}
}

// No activation record anywhere -> nil (unknown). Records only exist since v6,
// so this is the common case on older boards — the caller must say "unknown",
// not guess.
func TestPreviousActiveSuggestUnknown(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	_ = ea
	if prev := a.PreviousActiveSuggest(eb.ID); prev != nil {
		t.Fatalf("previous = %+v, want nil (no records)", prev)
	}
}
