package app

import (
	"testing"
	"time"
)

// A respace rewrites the neighbours' shards too: one of them carrying a repo
// another session holds refuses the whole write, on both relative paths
// (t-s895: the target alone was guarded, and ReorderRelative rewrote a busy
// repo's task without a refusal).
func TestRespaceIsGuardedForTheNeighboursItRewrites(t *testing.T) {
	a, _ := guardedApp(t, time.Minute, false)
	// Seed through a second App on the same store: the guard reads the
	// registry once per App, so the busy occupant must not be consulted
	// while the fixture is built.
	seed := NewWithStore(a.Store, a.Cfg, a.Clock)
	p100, p101 := 100, 101
	anchor, err := seed.Add("anchor", AddOpts{Status: "ready", Repos: []string{"o/other"}, Priority: &p100})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := seed.Add("held by the occupant", AddOpts{Status: "ready", Repos: []string{"o/glyph"}, Priority: &p101})
	if err != nil {
		t.Fatal(err)
	}
	mover, err := seed.Add("mover", AddOpts{Status: "ready", Repos: []string{"o/other"}})
	if err != nil {
		t.Fatal(err)
	}
	// The gap 100..101 is exhausted, so slotting after `anchor` respaces the
	// lane — including the occupant's task.
	_, _, err = a.ReorderRelative(mover.ID, anchor.ID, false)
	wantSessionBusy(t, err, busy.ID)
	held, _, _ := a.Get(busy.ID)
	if held.Priority != p101 {
		t.Fatalf("the occupant's task was respaced to %d by a refused write", held.Priority)
	}
	_, _, _, err = a.Set(mover.ID, SetOpts{After: anchor.ID})
	wantSessionBusy(t, err, busy.ID)
	held, _, _ = a.Get(busy.ID)
	if held.Priority != p101 {
		t.Fatalf("set --after respaced the occupant's task to %d by a refused write", held.Priority)
	}
	// Control: with no occupant the same respace passes.
	if _, _, err := seed.ReorderRelative(mover.ID, anchor.ID, false); err != nil {
		t.Fatalf("respace without a busy neighbour must pass: %v", err)
	}
}
