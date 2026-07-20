package main

import (
	"strings"
	"testing"
)

func laneIDs(b *Board, lane string) string {
	var ids []string
	for _, t := range b.LaneTasks(lane) {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ",")
}

func threeLane() *Board {
	return NewBoard([]*Task{
		{ID: "a", Status: "ready", Priority: 10},
		{ID: "b", Status: "ready", Priority: 20},
		{ID: "c", Status: "ready", Priority: 30},
		{ID: "z", Status: "backlog", Priority: 10},
	})
}

// AdjustDropIndex is the remove-then-insert boundary. Both gestures — keyboard
// move mode and mouse drag — measure their drop against the lane AS DISPLAYED,
// which still contains the moving card.
func TestAdjustDropIndex(t *testing.T) {
	tests := []struct {
		name     string
		sameLane bool
		fromIdx  int
		idx      int
		want     int
	}{
		{"cross-lane is never adjusted", false, 1, 0, 0},
		{"cross-lane at the end", false, 1, 3, 3},
		{"same lane, above the source", true, 2, 0, 0},
		{"same lane, at the source", true, 1, 1, 1},
		{"same lane, one slot below", true, 1, 2, 1},
		{"same lane, to the end", true, 0, 3, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AdjustDropIndex(tc.sameLane, tc.fromIdx, tc.idx); got != tc.want {
				t.Errorf("AdjustDropIndex(%v,%d,%d) = %d, want %d",
					tc.sameLane, tc.fromIdx, tc.idx, got, tc.want)
			}
		})
	}
}

// The off-by-one this guards, shown end to end. Lane displays [a,b,c,d]; you
// drag b onto the boundary between c and d, i.e. display insertion index 3.
//
//	adjusted  (3 -> 2): peers [a,c,d], insert at 2 => a,c,b,d   — where you aimed
//	unadjusted    (3):  peers [a,c,d], insert at 3 => a,c,d,b   — one slot too far
//
// The vacated slot shifts everything below it up by one, and only a same-lane
// move has a vacated slot above the target.
func TestSameLaneDropIsOffByOneWithoutAdjustment(t *testing.T) {
	four := func() *Board {
		return NewBoard([]*Task{
			{ID: "a", Status: "ready", Priority: 10},
			{ID: "b", Status: "ready", Priority: 20},
			{ID: "c", Status: "ready", Priority: 30},
			{ID: "d", Status: "ready", Priority: 40},
		})
	}

	b := four()
	if _, err := b.MoveTo("b", "ready", AdjustDropIndex(true, 1, 3)); err != nil {
		t.Fatal(err)
	}
	if got := laneIDs(b, "ready"); got != "a,c,b,d" {
		t.Errorf("adjusted drop = %s, want a,c,b,d", got)
	}

	b2 := four()
	if _, err := b2.MoveTo("b", "ready", 3); err != nil {
		t.Fatal(err)
	}
	if got := laneIDs(b2, "ready"); got != "a,c,d,b" {
		t.Errorf("unadjusted drop = %s, want a,c,d,b (the bug this guards)", got)
	}

	// A cross-lane drop must NOT be adjusted: there is no vacated slot in the
	// destination, so subtracting one would land a slot too high.
	b3 := four()
	b3.tasks = append(b3.tasks, &Task{ID: "z", Status: "backlog", Priority: 10})
	if _, err := b3.MoveTo("b", "backlog", AdjustDropIndex(false, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if got := laneIDs(b3, "backlog"); got != "z,b" {
		t.Errorf("cross-lane drop = %s, want z,b", got)
	}
}

func TestMoveToPositions(t *testing.T) {
	tests := []struct {
		name string
		id   string
		lane string
		idx  int
		want map[string]string
	}{
		{"to the top of its own lane", "c", "ready", 0, map[string]string{"ready": "c,a,b"}},
		{"to the end of its own lane", "a", "ready", 2, map[string]string{"ready": "b,c,a"}},
		{"cross-lane to the top", "b", "backlog", 0, map[string]string{"ready": "a,c", "backlog": "b,z"}},
		{"cross-lane to the end", "b", "backlog", 1, map[string]string{"ready": "a,c", "backlog": "z,b"}},
		{"index past the end clamps", "a", "backlog", 99, map[string]string{"backlog": "z,a"}},
		{"negative index clamps", "a", "backlog", -5, map[string]string{"backlog": "a,z"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := threeLane()
			if _, err := b.MoveTo(tc.id, tc.lane, tc.idx); err != nil {
				t.Fatal(err)
			}
			for lane, want := range tc.want {
				if got := laneIDs(b, lane); got != want {
					t.Errorf("%s = %s, want %s", lane, got, want)
				}
			}
		})
	}
}

func TestMoveToRejectsUnknowns(t *testing.T) {
	b := threeLane()
	if _, err := b.MoveTo("nope", "ready", 0); err == nil {
		t.Error("an unknown id must error")
	}
	if _, err := b.MoveTo("a", "nope", 0); err == nil {
		t.Error("an unknown lane must error")
	}
}

// Reordering edits ONE sparse integer. Only when the gap is exhausted does the
// lane respace, and then the neighbours that moved are reported.
func TestSparsePriorityAndRespace(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "a", Status: "ready", Priority: 10},
		{ID: "b", Status: "ready", Priority: 11}, // no room between a and b
		{ID: "c", Status: "ready", Priority: 30},
	})
	renumbered, err := b.MoveTo("c", "ready", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := laneIDs(b, "ready"); got != "a,c,b" {
		t.Fatalf("ready = %s, want a,c,b", got)
	}
	if len(renumbered) == 0 {
		t.Error("an exhausted gap must report the neighbours it renumbered")
	}
	for _, id := range renumbered {
		if id == "c" {
			t.Error("the moved task is not a 'renumbered neighbour'")
		}
	}

	// The roomy case must NOT respace.
	b2 := threeLane()
	if r, _ := b2.MoveTo("c", "ready", 1); len(r) != 0 {
		t.Errorf("a sparse gap needs no respace, got %v", r)
	}
}

func TestRespaceDoesNotAdvanceNeighbourUpdated(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "a", Status: "ready", Priority: 10},
		{ID: "b", Status: "ready", Priority: 11},
		{ID: "c", Status: "ready", Priority: 30},
	})
	before := b.Task("a").Updated
	if _, err := b.MoveTo("c", "ready", 1); err != nil {
		t.Fatal(err)
	}
	if !b.Task("a").Updated.Equal(before) {
		t.Error("a respace is positional bookkeeping, not progress — staleness signals must stay honest")
	}
	if b.Task("c").Updated.Equal(before) {
		t.Error("the MOVED task's Updated must advance")
	}
}

func TestCloseStampsAndReopenClears(t *testing.T) {
	b := threeLane()
	if err := b.Close("a"); err != nil {
		t.Fatal(err)
	}
	if b.Task("a").Status != "done" {
		t.Errorf("status = %s", b.Task("a").Status)
	}
	if b.Task("a").Closed.IsZero() {
		t.Error("closing must stamp Closed")
	}
	if _, err := b.MoveTo("a", "ready", 0); err != nil {
		t.Fatal(err)
	}
	if !b.Task("a").Closed.IsZero() {
		t.Error("leaving the done lane must clear Closed")
	}
}

// boardInsertIndex is the OTHER translation: a drop measured in a FILTERED
// column has to land in the right slot of the full lane.
func TestBoardInsertIndexUnderAFilter(t *testing.T) {
	full := []*Task{{ID: "a"}, {ID: "hidden1"}, {ID: "b"}, {ID: "hidden2"}, {ID: "c"}}
	vis := []*Task{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	tests := []struct{ visIdx, want int }{
		{0, 0}, // before a
		{1, 2}, // before b, which is really index 2
		{2, 4}, // before c, which is really index 4
		{3, 5}, // past the end of the visible list => end of the full lane
		{9, 5},
	}
	for _, tc := range tests {
		if got := boardInsertIndex(full, vis, tc.visIdx); got != tc.want {
			t.Errorf("boardInsertIndex(visIdx=%d) = %d, want %d", tc.visIdx, got, tc.want)
		}
	}

	// With no filter the translation is the identity.
	for i := 0; i <= len(vis); i++ {
		if got := boardInsertIndex(vis, vis, i); got != i {
			t.Errorf("unfiltered translation must be identity: %d -> %d", i, got)
		}
	}
}

// The filtered-drop end to end: the board must reorder around hidden tasks
// rather than counting them as slots.
func TestCommitMoveRespectsHiddenTasks(t *testing.T) {
	m := New(newMockProvider())
	m.w, m.h = 140, 40
	m.applyFilter("label:ui")
	m.recompute()

	vis := m.cols["backlog"]
	if len(vis) < 3 {
		t.Fatalf("need at least 3 visible backlog tasks, got %d", len(vis))
	}
	full := m.b.LaneTasks("backlog")
	if len(full) == len(vis) {
		t.Fatal("this test needs the filter to actually hide something")
	}
	mover, anchor := vis[0].ID, vis[2].ID

	// Drop the first visible card just before the third visible card.
	if _, err := m.commitMove(mover, "backlog", "backlog", 0, 2); err != nil {
		t.Fatal(err)
	}
	newVis := m.cols["backlog"]
	if newVis[1].ID != mover {
		t.Errorf("visible order = %v, want the mover at index 1", ids(newVis))
	}
	// And in the FULL lane it must sit immediately before its visible anchor.
	newFull := m.b.LaneTasks("backlog")
	mi, ai := indexOf(newFull, mover), indexOf(newFull, anchor)
	if mi < 0 || ai < 0 || mi >= ai {
		t.Errorf("full lane order wrong: mover at %d, anchor at %d (%v)", mi, ai, ids(newFull))
	}
}

func ids(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func indexOf(ts []*Task, id string) int {
	for i, t := range ts {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// quickReorder (shift+K / shift+J) goes through the same arithmetic.
func TestQuickReorder(t *testing.T) {
	m := New(newMockProvider())
	m.w, m.h = 140, 40
	m.curLane = m.b.LaneIndex("backlog")
	m.curIdx["backlog"] = 1
	m.recompute()

	before := ids(m.cols["backlog"])
	second := before[1]

	m.quickReorder(-1)
	after := ids(m.cols["backlog"])
	if after[0] != second {
		t.Errorf("K did not raise: %v -> %v", before, after)
	}
	if m.curTask().ID != second {
		t.Errorf("the cursor must follow the card, got %s", m.curTask().ID)
	}

	m.quickReorder(+1)
	if got := ids(m.cols["backlog"]); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("J did not put it back: %v vs %v", got, before)
	}

	// At the top, K is a no-op with an explanation rather than a silent nothing.
	m.setPos(0)
	m.quickReorder(-1)
	if !strings.Contains(m.status, "already at the top") {
		t.Errorf("status = %q", m.status)
	}
}

// Move mode's arrow arithmetic: the drop index walks 0..len and clamps when the
// lane changes.
func TestMoveModeDropIndexArithmetic(t *testing.T) {
	m := New(newMockProvider())
	m.w, m.h = 140, 40
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	m.recompute()
	m.enterMove()

	if m.mode != modeMove {
		t.Fatal("enterMove did not enter move mode")
	}
	n := len(m.cols["backlog"])

	for i := 0; i < n+5; i++ {
		m.dropIdx = minInt(m.dropSpan(m.dropLane), m.dropIdx+1)
	}
	if m.dropIdx != n {
		t.Errorf("dropIdx ran to %d, want a clamp at %d", m.dropIdx, n)
	}
	for i := 0; i < n+5; i++ {
		m.dropIdx = maxInt(0, m.dropIdx-1)
	}
	if m.dropIdx != 0 {
		t.Errorf("dropIdx floored at %d, want 0", m.dropIdx)
	}

	// Moving to a shorter lane clamps the index into range.
	m.dropIdx = n
	m.dropLane = "backlog"
	m.shiftDropLane(+1) // -> ready, which has one task
	if m.dropIdx > len(m.cols[m.dropLane]) {
		t.Errorf("dropIdx %d exceeds %s (%d slots)", m.dropIdx, m.dropLane, len(m.cols[m.dropLane]))
	}
	// And it cannot walk off either end of the lane vocabulary.
	for i := 0; i < 20; i++ {
		m.shiftDropLane(+1)
	}
	if m.b.LaneIndex(m.dropLane) != len(m.b.Lanes())-1 {
		t.Errorf("dropLane = %s, want the last lane", m.dropLane)
	}
	for i := 0; i < 20; i++ {
		m.shiftDropLane(-1)
	}
	if m.b.LaneIndex(m.dropLane) != 0 {
		t.Errorf("dropLane = %s, want the first lane", m.dropLane)
	}
}
