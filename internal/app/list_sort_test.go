package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func julyUTC(d int) time.Time { return time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC) }

// seedUpdated rewrites tasks' Updated stamps below the app layer so List's
// date-window and sort behavior can be exercised under the test's fixed clock.
func seedUpdated(t *testing.T, a *App, when map[string]time.Time) {
	t.Helper()
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for id, ts := range when {
		_, i := idx.Find(id)
		if i < 0 {
			t.Fatalf("no such task %s", id)
		}
		idx.Tasks[i].Updated = ts
	}
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
}

func listIDs(tasks []core.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestListSortByUpdated(t *testing.T) {
	a := newApp()
	x, _ := a.Add("x", AddOpts{})
	y, _ := a.Add("y", AddOpts{})
	z, _ := a.Add("z", AddOpts{})
	seedUpdated(t, a, map[string]time.Time{x.ID: julyUTC(3), y.ID: julyUTC(9), z.ID: julyUTC(6)})

	got, err := a.List(QueryOpts{Sort: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{y.ID, z.ID, x.ID}; !equalStrs(listIDs(got), want) {
		t.Errorf("sort updated desc = %v, want %v", listIDs(got), want)
	}
}

func TestListSinceUntilWindow(t *testing.T) {
	a := newApp()
	old, _ := a.Add("old", AddOpts{})
	mid, _ := a.Add("mid", AddOpts{})
	recent, _ := a.Add("recent", AddOpts{})
	seedUpdated(t, a, map[string]time.Time{old.ID: julyUTC(1), mid.ID: julyUTC(5), recent.ID: julyUTC(10)})

	// since only: updated >= july 5.
	got, err := a.List(QueryOpts{Since: tp(julyUTC(5))})
	if err != nil {
		t.Fatal(err)
	}
	if ids := listIDs(got); len(ids) != 2 || !contains(ids, mid.ID) || !contains(ids, recent.ID) {
		t.Errorf("since july-5 should keep mid+recent, got %v", ids)
	}

	// window: july 4 .. july 6 inclusive -> only mid.
	got, err = a.List(QueryOpts{Since: tp(julyUTC(4)), Until: tp(julyUTC(6))})
	if err != nil {
		t.Fatal(err)
	}
	if ids := listIDs(got); len(ids) != 1 || ids[0] != mid.ID {
		t.Errorf("window july4..6 should keep only mid, got %v", ids)
	}
}

func TestListSortLimitAppliesAfterOrdering(t *testing.T) {
	a := newApp()
	lo, _ := a.Add("lo", AddOpts{Value: ptrInt(1)})
	hi, _ := a.Add("hi", AddOpts{Value: ptrInt(5)})
	a.Add("mid", AddOpts{Value: ptrInt(3)})

	// top 2 by value should be hi(5) then mid(3), NOT the first 2 in canonical order.
	got, err := a.List(QueryOpts{Sort: "value", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if ids := listIDs(got); len(ids) != 2 || ids[0] != hi.ID {
		t.Errorf("sort value + limit 2 should return the top 2 (hi first), got %v", ids)
	}
	_ = lo
}

func TestListUnknownSortFailsFast(t *testing.T) {
	a := newApp()
	a.Add("x", AddOpts{})
	_, err := a.List(QueryOpts{Sort: "bogus"})
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Fatalf("unknown --sort should fail fast with candidates, got %v", err)
	}
}

func TestListNoSortKeepsCanonicalOrder(t *testing.T) {
	a := newApp()
	a.Add("first", AddOpts{})
	a.Add("second", AddOpts{})
	// Without a sort the order is unchanged (canonical); just assert it still works
	// and honors limit as before.
	got, err := a.List(QueryOpts{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("limit 1 should return 1 task, got %d", len(got))
	}
}

func tp(t time.Time) *time.Time { return &t }
func ptrInt(n int) *int         { return &n }
func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
