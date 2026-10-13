package app

import (
	"testing"
	"time"
)

// A board whose calendar observes DST: a --due-shift that crosses the change
// keeps the wall clock, and so does an anchor move. Both read a.loc(); under
// time.UTC (the fault this guards) the UTC instant is kept instead and the
// due reads an hour off on the board's own calendar — a JST board, with no
// DST, cannot tell the two apart.
func TestDueShiftAndAnchorMoveKeepTheWallClockAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newAppWith(at(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)), zone(ny))

	// DST ends in New York on 2026-11-01: 13:20 EDT is 17:20Z, 13:20 EST is 18:20Z.
	tk := mustAdd(t, a, "shifted", AddOpts{Due: "2026-10-25T13:20"})
	if got, want := dueOf(t, a, tk.ID), time.Date(2026, 10, 25, 17, 20, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("premise: the due parses on the board's calendar, got %s want %s", got, want)
	}
	shift := "+14d"
	moved, _, _, err := a.Set(tk.ID, SetOpts{DueShift: &shift})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 8, 13, 20, 0, 0, ny); !moved.Due.Equal(want) {
		t.Errorf("--due-shift +14d across the DST change: due = %s, want %s (the wall clock, not the UTC instant, is kept)", moved.Due.In(ny), want)
	}

	epic := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}, Anchor: "2026-10-25"})
	follower := mustAdd(t, a, "follower", AddOpts{Epic: epic, Due: "2026-10-25T13:20", Anchor: epic})
	_, _, plan, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-11-08")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Days != 14 || len(plan.Moves) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if got, want := dueOf(t, a, follower.ID), time.Date(2026, 11, 8, 13, 20, 0, 0, ny); !got.Equal(want) {
		t.Errorf("anchor move across the DST change: follower due = %s, want %s", got.In(ny), want)
	}
}
