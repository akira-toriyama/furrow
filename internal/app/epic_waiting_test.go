package app

import (
	"testing"
	"time"
)

// strP is a *string literal for SetOpts.
func strP(s string) *string { return &s }

// park moves a member into the waiting lane, optionally binding a due.
func park(t *testing.T, a *App, id, due string) {
	t.Helper()
	o := SetOpts{Status: strP("waiting")}
	if due != "" {
		o.Due = strP(due)
	}
	if _, _, err := a.Set(id, o); err != nil {
		t.Fatalf("park %s: %v", id, err)
	}
}

func allDone(t *testing.T, a *App) []string {
	t.Helper()
	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return sum.EpicAllDone
}

func rowOf(t *testing.T, a *App, id string) EpicItem {
	t.Helper()
	items, err := a.EpicList(EpicQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Epic.ID == id {
			return it
		}
	}
	t.Fatalf("epic %s not listed", id)
	return EpicItem{}
}

// A box whose open work is done but whose remaining member is parked with a
// due still ahead is WAITING, not all-done: revisit stays quiet, and epic
// ls/show say until when and on whose account.
func TestWaitingBoxSilencesAllDoneAndSurfacesUntil(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "feather sku", EpicAddOpts{Repos: []string{"o/r"}})
	work := mustAddReady(t, a, "ship it", box)
	watch := mustAddReady(t, a, "confirm zero incidents", box)
	later := mustAddReady(t, a, "the later check", box)
	if _, err := a.Done(work); err != nil {
		t.Fatal(err)
	}
	park(t, a, watch, "2026-10-01")
	park(t, a, later, "2026-12-01")

	if got := allDone(t, a); len(got) != 0 {
		t.Errorf("a waiting box must not be nagged to close: %v", got)
	}
	row := rowOf(t, a, box)
	if row.Waiting == nil || row.Waiting.Task != watch {
		t.Fatalf("epic ls must name the EARLIEST parked due's carrier, got %+v", row.Waiting)
	}
	// A bare day binds the end of that day in the app's zone (ParseDue).
	if want := time.Date(2026, 10, 1, 23, 59, 59, 0, a.loc()); !row.Waiting.Until.Equal(want) {
		t.Errorf("until = %s, want %s", row.Waiting.Until, want)
	}
	d, err := a.EpicShow(box)
	if err != nil {
		t.Fatal(err)
	}
	if d.Waiting == nil || d.Waiting.Task != watch || !d.Waiting.Until.Equal(row.Waiting.Until) {
		t.Errorf("epic show must carry the same state, got %+v", d.Waiting)
	}

	// Once the date has passed the box is back to all-done: the date's own
	// surfaces (due-overdue) own it now, and "consider closing" is true again.
	a.Clock = &fixedClock{t: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	if got := allDone(t, a); len(got) != 1 || got[0] != box {
		t.Errorf("an arrived due ends the wait: want all_done on %s, got %v", box, got)
	}
	if row := rowOf(t, a, box); row.Waiting != nil {
		t.Errorf("no future due, no waiting state: %+v", row.Waiting)
	}
}

// The states that must NOT count as waiting: a parked member with no due
// (nobody can say what it waits for), a due in a [due].ignore_lanes lane, and a
// box that still has open work (then it is stuck or in progress, not waiting).
func TestWaitingBoxNeedsAFutureDueOnATrackedLaneAndNoOpenWork(t *testing.T) {
	a := newApp()

	dateless := mustEpic(t, a, "dateless", EpicAddOpts{Repos: []string{"o/r"}})
	park(t, a, mustAddReady(t, a, "waits for nothing named", dateless), "")

	iced := mustEpic(t, a, "iced", EpicAddOpts{Repos: []string{"o/r"}})
	ice := mustAddReady(t, a, "someday", iced)
	if _, _, err := a.Set(ice, SetOpts{Status: strP("icebox"), Due: strP("2026-10-01")}); err != nil {
		t.Fatal(err)
	}

	busy := mustEpic(t, a, "busy", EpicAddOpts{Repos: []string{"o/r"}})
	park(t, a, mustAddReady(t, a, "the watch", busy), "2026-10-01")
	mustAdd(t, a, "untriaged gate", AddOpts{Epic: busy}) // inbox: open, not actionable

	got := allDone(t, a)
	want := map[string]bool{dateless: true, iced: true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Errorf("all_done must still fire for the dateless and the iced box, got %v (want %s, %s)", got, dateless, iced)
	}
	for _, id := range []string{dateless, iced, busy} {
		if row := rowOf(t, a, id); row.Waiting != nil {
			t.Errorf("%s must not read as waiting: %+v", row.Epic.Title, row.Waiting)
		}
	}
	if row := rowOf(t, a, busy); !row.Stuck {
		t.Errorf("open work beside a parked date is the stuck case, not waiting: %+v", row)
	}
}

// standing stays exempt on its own terms: a waiting standing box is silent
// for both reasons, and reports its wait like any other box.
func TestWaitingStandingBoxStaysQuiet(t *testing.T) {
	a := newApp()
	inbox := mustEpic(t, a, "mandate", EpicAddOpts{Repos: []string{"o/r"}})
	setFlag(t, a, inbox, boolPtr(true), nil)
	park(t, a, mustAddReady(t, a, "check back", inbox), "2026-10-01")
	if got := allDone(t, a); len(got) != 0 {
		t.Errorf("standing must stay exempt: %v", got)
	}
	if row := rowOf(t, a, inbox); row.Waiting == nil {
		t.Errorf("the wait is still information worth a row: %+v", row)
	}
}
