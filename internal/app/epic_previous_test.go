package app

import (
	"testing"
	"time"
)

// activateAndLeave activates id at the clock's current minute and deactivates
// it again, so the box holds an activation record but no slot.
func activateAndLeave(t *testing.T, a *App, id string) {
	t.Helper()
	if _, _, _, err := a.EpicActivate(id, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicDeactivate(id); err != nil {
		t.Fatal(err)
	}
}

// The suggestion is the open box sharing a free repo with the box being left
// that holds the newest activation record — computed from the body log, no
// stored state.
func TestPreviousActiveSuggest(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, err := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	if err != nil {
		t.Fatal(err)
	}
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})

	activateAndLeave(t, a, ea.ID)
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(eb.ID, "switching"); err != nil {
		t.Fatal(err)
	}

	// Closing b: the only candidate with a record is a.
	prev, held := a.PreviousActiveSuggest(eb.ID)
	if prev == nil || prev.ID != ea.ID || held != nil {
		t.Fatalf("previous = %+v, held = %v; want %s and nothing held", prev, held, ea.ID)
	}
	if prev.Title != "box a" || prev.At != "2026-06-25 10:00" {
		t.Errorf("previous = %+v, want title 'box a' at '2026-06-25 10:00'", prev)
	}
}

// Within the repo the newest record wins, and a newer record in another repo
// does not count: board-wide recency is another repo's focus, not where this
// repo's session came from (t-b5q5). The newer same-repo record sits on the
// larger id, so a first-match scan in id order fails too.
func TestPreviousActiveSuggestNewestInRepoWins(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	p, _ := a.EpicAdd("box p", EpicAddOpts{Repos: []string{"me/r1"}})
	q, _ := a.EpicAdd("box q", EpicAddOpts{Repos: []string{"me/r1"}})
	older, newer := p, q
	if older.ID > newer.ID {
		older, newer = newer, older
	}
	ex, _ := a.EpicAdd("box x", EpicAddOpts{Repos: []string{"me/r2"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})

	for _, id := range []string{older.ID, newer.ID, ex.ID} { // x holds the newest record
		activateAndLeave(t, a, id)
		clk.t = clk.t.Add(5 * time.Minute)
	}
	if _, _, _, err := a.EpicActivate(eb.ID, ""); err != nil {
		t.Fatal(err)
	}

	if prev, _ := a.PreviousActiveSuggest(eb.ID); prev == nil || prev.ID != newer.ID {
		t.Fatalf("previous = %+v, want %s (newest in me/r1: not the older %s, not me/r2's %s)", prev, newer.ID, older.ID, ex.ID)
	}
}

// Sharing one repo is enough, on either side: the left box's shared repo sorts
// second (repos are stored sorted, so checking only the first one fails), and
// the candidate carries a repo the left box lacks.
func TestPreviousActiveSuggestOneSharedRepoIsEnough(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ey, _ := a.EpicAdd("box y", EpicAddOpts{Repos: []string{"me/z9", "me/r2"}})
	em, _ := a.EpicAdd("box m", EpicAddOpts{Repos: []string{"me/r2", "me/a0"}})
	if len(em.Repos) != 2 || em.Repos[1] != "me/r2" {
		t.Fatalf("fixture: box m repos = %v, want me/r2 second", em.Repos)
	}
	activateAndLeave(t, a, ey.ID)

	if prev, _ := a.PreviousActiveSuggest(em.ID); prev == nil || prev.ID != ey.ID {
		t.Fatalf("previous = %+v, want %s (one shared repo is enough)", prev, ey.ID)
	}
}

// Repos match folded, as the slot map compares them: a box spelled Me/R1 is in
// me/r1 both when matching a candidate and when its slot is held.
func TestPreviousActiveSuggestFoldsRepoCase(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"Me/R1"}})
	if len(eb.Repos) != 1 || eb.Repos[0] != "Me/R1" {
		t.Fatalf("fixture: box b repos = %v, want [Me/R1] as typed", eb.Repos)
	}
	activateAndLeave(t, a, ea.ID)

	if prev, _ := a.PreviousActiveSuggest(eb.ID); prev == nil || prev.ID != ea.ID {
		t.Fatalf("previous = %+v, want %s (Me/R1 is me/r1)", prev, ea.ID)
	}
	if _, _, _, err := a.EpicActivate(ea.ID, ""); err != nil {
		t.Fatal(err)
	}
	if prev, held := a.PreviousActiveSuggest(eb.ID); prev != nil || held["Me/R1"] != ea.ID {
		t.Fatalf("previous = %+v, held = %v; want none, Me/R1 held by %s", prev, held, ea.ID)
	}
}

// Neither a closed box (it cannot be activated) nor the box being left (its own
// record is the newest) is a candidate.
func TestPreviousActiveSuggestSkipsClosedAndSelf(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	ed, _ := a.EpicAdd("box d", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})

	activateAndLeave(t, a, ea.ID) // oldest record
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(ed.ID, ""); err != nil { // newer, then CLOSED
		t.Fatal(err)
	}
	if _, _, err := a.EpicDone(ed.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(eb.ID, ""); err != nil { // newest, the box being left
		t.Fatal(err)
	}

	if prev, _ := a.PreviousActiveSuggest(eb.ID); prev == nil || prev.ID != ea.ID {
		t.Fatalf("previous = %+v, want %s (neither closed d nor b itself)", prev, ea.ID)
	}
}

// Closing a parked box while another box holds its repo leaves that repo's
// focus in place: nothing to return to, and the holder is named. A box
// suggested there would be one `epic activate` refuses. A repo of the left box
// that is still free is answered as usual.
func TestPreviousActiveSuggestHeldRepo(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)))
	ex, _ := a.EpicAdd("box x", EpicAddOpts{Repos: []string{"me/r2"}})
	ea, _ := a.EpicAdd("box a", EpicAddOpts{Repos: []string{"me/r1"}})
	ec, _ := a.EpicAdd("box c", EpicAddOpts{Repos: []string{"me/r1"}})
	eb, _ := a.EpicAdd("box b", EpicAddOpts{Repos: []string{"me/r1"}})
	ew, _ := a.EpicAdd("box w", EpicAddOpts{Repos: []string{"me/r1", "me/r2"}})

	activateAndLeave(t, a, ex.ID) // older record, me/r2
	clk.t = clk.t.Add(5 * time.Minute)
	activateAndLeave(t, a, ea.ID) // newer record, me/r1
	clk.t = clk.t.Add(5 * time.Minute)
	if _, _, _, err := a.EpicActivate(ec.ID, ""); err != nil { // c holds me/r1
		t.Fatal(err)
	}

	prev, held := a.PreviousActiveSuggest(eb.ID)
	if prev != nil || len(held) != 1 || held["me/r1"] != ec.ID {
		t.Fatalf("leaving parked b: previous = %+v, held = %v; want none, me/r1 held by %s", prev, held, ec.ID)
	}
	prev, held = a.PreviousActiveSuggest(ew.ID)
	if prev == nil || prev.ID != ex.ID || held != nil {
		t.Fatalf("leaving parked w: previous = %+v, held = %v; want %s from the free me/r2", prev, held, ex.ID)
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
	activateAndLeave(t, a, ea.ID)

	if prev, held := a.PreviousActiveSuggest(en.ID); prev != nil || held != nil {
		t.Fatalf("previous = %+v, held = %v; want unknown (a repo-less box shares no repo)", prev, held)
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
	if prev, held := a.PreviousActiveSuggest(eb.ID); prev != nil || held != nil {
		t.Fatalf("previous = %+v, held = %v; want unknown (no records)", prev, held)
	}
}
