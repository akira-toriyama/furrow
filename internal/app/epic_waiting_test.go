package app

import (
	"testing"
	"time"
)

// park moves a member into the waiting lane, optionally binding a due.
func park(t *testing.T, a *App, id, due string) {
	t.Helper()
	o := SetOpts{Status: ptr("waiting")}
	if due != "" {
		o.Due = ptr(due)
	}
	if _, _, _, err := a.Set(id, o); err != nil {
		t.Fatalf("park %s: %v", id, err)
	}
}

func boxSignals(t *testing.T, a *App) (allDone, stuck []string) {
	t.Helper()
	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return sum.EpicAllDone, sum.EpicStuck
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

// A box next reads whose open work is done but whose remaining member is
// parked with a due still ahead is WAITING, not stuck: revisit stays quiet, and
// epic ls/show say until when and on whose account.
func TestWaitingBoxSilencesStuckAndSurfacesUntil(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "feather sku", EpicAddOpts{Repos: []string{"o/r"}})
	work := mustAddReady(t, a, "ship it", box)
	watch := mustAddReady(t, a, "confirm zero incidents", box)
	later := mustAddReady(t, a, "the later check", box)
	mustActivate(t, a, box)
	if _, err := a.Done(work); err != nil {
		t.Fatal(err)
	}
	park(t, a, watch, "2026-10-01")
	park(t, a, later, "2026-12-01")

	if done, stuck := boxSignals(t, a); len(done)+len(stuck) != 0 {
		t.Errorf("a waiting box must raise neither signal: all_done=%v stuck=%v", done, stuck)
	}
	row := rowOf(t, a, box)
	if row.Stuck {
		t.Errorf("a waiting box must not carry the stuck flag: %+v", row)
	}
	if row.Waiting == nil || row.Waiting.Task != watch {
		t.Fatalf("epic ls must name the EARLIEST parked due's carrier, got %+v", row.Waiting)
	}
	// A bare day binds the end of that day in the app's zone (parseDueAt).
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

	// Once the date has passed the wait is over and the box is stuck: what is
	// left is parked, not done, so "consider closing" would still be false.
	a.Clock = &fixedClock{t: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	if done, stuck := boxSignals(t, a); len(done) != 0 || len(stuck) != 1 || stuck[0] != box {
		t.Errorf("an arrived due ends the wait: want stuck on %s only, got all_done=%v stuck=%v", box, done, stuck)
	}
	if row := rowOf(t, a, box); row.Waiting != nil {
		t.Errorf("no future due, no waiting state: %+v", row.Waiting)
	}
}

// The states that must NOT count as waiting, each in a box next reads: a
// parked member with no due (nobody can say what it waits for), a due in a
// [due].ignore_lanes lane, and a box that still has open work. All three are
// stuck.
func TestWaitingBoxNeedsAFutureDueOnATrackedLaneAndNoOpenWork(t *testing.T) {
	a := newApp()

	dateless := mustEpic(t, a, "dateless", EpicAddOpts{Repos: []string{"o/a"}})
	park(t, a, mustAddReady(t, a, "waits for nothing named", dateless), "")

	iced := mustEpic(t, a, "iced", EpicAddOpts{Repos: []string{"o/b"}})
	ice := mustAddReady(t, a, "someday", iced)
	if _, _, _, err := a.Set(ice, SetOpts{Status: ptr("icebox"), Due: ptr("2026-10-01")}); err != nil {
		t.Fatal(err)
	}

	busy := mustEpic(t, a, "busy", EpicAddOpts{Repos: []string{"o/c"}})
	park(t, a, mustAddReady(t, a, "the watch", busy), "2026-10-01")
	mustAdd(t, a, "untriaged gate", AddOpts{Epic: busy}) // inbox: open, not actionable

	for _, id := range []string{dateless, iced, busy} {
		mustActivate(t, a, id)
	}
	done, stuck := boxSignals(t, a)
	if len(done) != 0 {
		t.Errorf("members parked outside the done lane are not done: all_done=%v", done)
	}
	want := map[string]bool{dateless: true, iced: true, busy: true}
	if len(stuck) != 3 || !want[stuck[0]] || !want[stuck[1]] || !want[stuck[2]] {
		t.Errorf("stuck = %v, want the dateless, the iced and the busy box", stuck)
	}
	for _, id := range []string{dateless, iced, busy} {
		row := rowOf(t, a, id)
		if row.Waiting != nil {
			t.Errorf("%s must not read as waiting: %+v", row.Epic.Title, row.Waiting)
		}
		if !row.Stuck {
			t.Errorf("%s must carry the stuck flag: %+v", row.Epic.Title, row)
		}
	}
}

// standing stays exempt on its own terms: a waiting standing box is silent
// for both reasons, and reports its wait like any other box.
func TestWaitingStandingBoxStaysQuiet(t *testing.T) {
	a := newApp()
	inbox := mustEpic(t, a, "mandate", EpicAddOpts{Repos: []string{"o/r"}})
	setFlag(t, a, inbox, ptr(true), nil)
	park(t, a, mustAddReady(t, a, "check back", inbox), "2026-10-01")
	mustActivate(t, a, inbox)
	if done, stuck := boxSignals(t, a); len(done)+len(stuck) != 0 {
		t.Errorf("standing must stay exempt: all_done=%v stuck=%v", done, stuck)
	}
	if row := rowOf(t, a, inbox); row.Waiting == nil {
		t.Errorf("the wait is still information worth a row: %+v", row)
	}
}
