package app

import "testing"

// The read verbs load the index ONCE. Each of these used to read the board
// twice or more (Tree via List + listIndex, EpicShow via load + ListItems,
// ShowBatch via EpicShow per box ref, RevisitSummary via RevisitEpics) — a
// second load is a second snapshot, and on a shared board that a co-writer
// is syncing the two can disagree.
func TestReadVerbsLoadTheBoardOnce(t *testing.T) {
	a, spy := spyApp(t)
	box := mustEpic(t, a, "box", EpicAddOpts{})
	gate := mustAdd(t, a, "gate", AddOpts{Status: "backlog"})
	mustAdd(t, a, "member", AddOpts{Epic: box, Status: "ready", Deps: []string{gate.ID}})

	reads := map[string]func() error{
		"Tree":           func() error { _, err := a.Tree(QueryOpts{}, ""); return err },
		"EpicList":       func() error { _, err := a.EpicList(EpicQueryOpts{}); return err },
		"EpicShow":       func() error { _, err := a.EpicShow(box); return err },
		"ShowBatch":      func() error { _, _, err := a.ShowBatch([]string{gate.ID, box}, true); return err },
		"RevisitSummary": func() error { _, err := a.RevisitSummary(QueryOpts{}, 0); return err },
	}
	for name, read := range reads {
		spy.loads = 0
		if err := read(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if spy.loads != 1 {
			t.Errorf("%s loaded the index %d times, want 1", name, spy.loads)
		}
	}
}
