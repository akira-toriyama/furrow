package app

import "testing"

// brief reads the index ONCE and the boxes ONCE: it used to compose ten public
// reads, each loading for itself, and a task removed between two of those
// loads was counted in next_total yet missing from the picks with no note
// (t-m7kp measured the ten; t-dhzm the silent narrowing). On one snapshot
// every band describes the same board.
func TestBriefLoadsTheBoardOnce(t *testing.T) {
	a, spy := spyApp(t)
	seedForEdit(t, a)
	for _, title := range []string{"ready one", "ready two"} {
		if _, err := a.Add(title, AddOpts{Status: "ready", Repos: []string{"me/x"}}); err != nil {
			t.Fatal(err)
		}
	}
	spy.loads, spy.epicLoads = 0, 0
	b, err := a.Brief(QueryOpts{}, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if spy.loads != 1 || spy.epicLoads != 1 {
		t.Errorf("brief loaded the index %d times and the boxes %d times; want 1 and 1", spy.loads, spy.epicLoads)
	}
	hidden := 0
	for _, h := range b.NextHidden {
		hidden += len(h.IDs)
	}
	if b.NextTotal != 2 || len(b.Next) != 1 || b.NextTotal != len(b.Next)+hidden {
		t.Errorf("next_total %d must equal the picks (%d) plus what the cap hid (%v)", b.NextTotal, len(b.Next), b.NextHidden)
	}
}
