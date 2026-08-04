package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// newClockedApp is newApp with the clock handed back, so a test can advance it
// between activations (recordSwitch stamps are minute-precision).
func newClockedApp() (*App, *fixedClock) {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk), clk
}

// The suggestion is the open, currently-inactive box with the newest
// activation record — computed from the body log, no stored state.
func TestPreviousActiveSuggest(t *testing.T) {
	a, clk := newClockedApp()
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
// and a closed box cannot be one (it cannot be activated).
func TestPreviousActiveSuggestSkipsActiveAndClosed(t *testing.T) {
	a, clk := newClockedApp()
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	ec, _ := a.EpicAdd("box c", EpicAddOpts{Repos: []string{"me/r2"}})
	ed, _ := a.EpicAdd("box d", EpicAddOpts{Repos: []string{"me/r3"}})

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
	if _, _, _, err := a.EpicActivate(ec.ID, ""); err != nil { // newest, still ACTIVE
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(eb.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Closing b: c is active (skip), d is closed (skip) -> a wins despite the
	// oldest stamp.
	prev := a.PreviousActiveSuggest(eb.ID)
	if prev == nil || prev.ID != ea.ID {
		t.Fatalf("previous = %+v, want %s (active/closed boxes are not candidates)", prev, ea.ID)
	}
}

// No activation record anywhere -> nil (unknown). Records only exist since v6,
// so this is the common case on older boards — the caller must say "unknown",
// not guess.
func TestPreviousActiveSuggestUnknown(t *testing.T) {
	a, _ := newClockedApp()
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	_ = ea
	if prev := a.PreviousActiveSuggest(eb.ID); prev != nil {
		t.Fatalf("previous = %+v, want nil (no records)", prev)
	}
}
