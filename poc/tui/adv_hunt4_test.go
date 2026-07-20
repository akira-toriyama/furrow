package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
)

// A tiny synthetic board: short ASCII titles so several cards fit a column and
// the arithmetic is easy to read.
func advSmallBoard() *Board {
	var ts []*Task
	for i, id := range []string{"a1", "a2", "a3"} {
		ts = append(ts, &Task{ID: id, Title: id, Status: "ready", Priority: (i + 1) * 10})
	}
	for i, id := range []string{"b1", "b2", "b3", "b4"} {
		ts = append(ts, &Task{ID: id, Title: id, Status: "backlog", Priority: (i + 1) * 10})
	}
	return NewBoard(ts)
}

func advSmallModel(t *testing.T, w, h int) *Model {
	t.Helper()
	m := New(&emptyProvider{b: advSmallBoard()})
	m.w, m.h = w, h
	m.recompute()
	m.relayout()
	return m
}

// The wheel clamps scroll to len(tasks)-1 and never asks whether anything is
// actually below the fold. On a column where every card already fits, one
// wheel-down scrolls the top card off screen with no way to know it is there
// beyond a "1 above" hint.
func TestAdvWheelScrollsAColumnThatEntirelyFits(t *testing.T) {
	m := advSmallModel(t, 140, 40)
	col := m.lay.Col("ready")
	if col == nil || col.Hidden != 0 || len(col.Cards) != 3 {
		t.Fatalf("setup: cards=%d hidden=%d", len(col.Cards), col.Hidden)
	}
	m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 10, Button: tea.MouseWheelDown})
	after := m.lay.Col("ready")
	if after.Scroll != 0 || len(after.Cards) != 3 {
		t.Errorf("wheel-down on a column with Hidden=0: scroll %d->%d, cards 3->%d "+
			"(card %q is now off screen)", col.Scroll, after.Scroll, len(after.Cards),
			after.Tasks[0].ID)
	}
}

// ensureVisible only ever repairs the FOCUSED lane's scroll offset, so a stale
// offset in any other lane survives a filter change and hides its cards.
func TestAdvStaleScrollSurvivesInAnUnfocusedLane(t *testing.T) {
	m := advSmallModel(t, 140, 40)
	// focus ready, scroll BACKLOG (unfocused) to its last card
	m.curLane = m.b.LaneIndex("ready")
	m.setPos(0)
	bcol := m.lay.Col("backlog")
	for i := 0; i < 3; i++ {
		m.Update(tea.MouseWheelMsg{X: bcol.X + 4, Y: 10, Button: tea.MouseWheelDown})
	}
	m.relayout()
	after := m.lay.Col("backlog")
	if after.Scroll != 0 {
		t.Errorf("backlog holds %d cards that all fit (Hidden=%d) yet renders scrolled to %d: "+
			"%d of them are invisible and the lane is not focused, so ensureVisible will "+
			"never repair it", len(after.Tasks), after.Hidden, after.Scroll, after.Scroll)
	}
}

// A card dropped onto a column that is scrolled can only ever land among the
// VISIBLE cards: idxAtY caps the insertion index at lastVisible+1, so with
// cards below the fold there is no gesture that appends to the true end.
func TestAdvCannotDropBelowTheFold(t *testing.T) {
	m := boardModel(t, 140, 24) // short: backlog has cards below the fold
	col := m.lay.Col("backlog")
	if col == nil || col.Hidden == 0 {
		t.Skipf("backlog hidden=%d", col.Hidden)
	}
	maxIdx := m.lay.idxAtY("backlog", col.Bot-1)
	if maxIdx < len(col.Tasks) {
		t.Errorf("backlog has %d cards (%d below the fold) but the deepest reachable "+
			"insertion index is %d — the bottom of the lane is not a droppable target",
			len(col.Tasks), col.Hidden, maxIdx)
	}
}

// ---------------------------------------------------------------------------
// Esc routing while the filter input is modal
// ---------------------------------------------------------------------------

// onKey checks cancelDrag() BEFORE the mode switch, so an Esc meant to dismiss
// the filter input is eaten by a still-armed drag instead.
func TestAdvEscInFilterModeIsEatenByAnArmedDrag(t *testing.T) {
	m := boardModel(t, 140, 40)
	col := m.lay.Col(m.curLaneName())
	if col == nil || len(col.Cards) == 0 {
		t.Fatal("no card")
	}
	box := col.Cards[0]
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	// user presses / while still holding the button, types, then presses esc
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.mode != modeFilter {
		t.Fatal("did not enter filter mode")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode == modeFilter {
		t.Errorf("esc was swallowed by the armed drag; the filter input is still modal "+
			"(drag.armed=%v cancelled=%v)", m.drag.armed, m.drag.cancelled)
	}
}

// ---------------------------------------------------------------------------
// negative-Y chrome actually distorts the frame
// ---------------------------------------------------------------------------

func TestAdvNegativeYShiftsTheWholeFrame(t *testing.T) {
	m := boardModel(t, 60, 1)
	out := ansiStrip(m.View().Content)
	lines := strings.Split(out, "\n")
	if lg.Height(out) != 1 {
		t.Errorf("1-row terminal rendered %d rows; first row is %q",
			lg.Height(out), lines[0])
	}
}

// ---------------------------------------------------------------------------
// the status line lies about a clamped drop
// ---------------------------------------------------------------------------

func TestAdvStatusLineClaimsARepositionThatDidNotHappen(t *testing.T) {
	m := advSmallModel(t, 140, 40)
	col := m.lay.Col("ready")
	box := col.Cards[0]
	id := col.Tasks[0].ID
	before := advIDs(m.b.LaneTasks("ready"))

	// drag a1 upward, above the top of its own column, and release there
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: box.X + 3, Y: box.Y - 2, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: box.X + 3, Y: box.Y - 2, Button: tea.MouseLeft})

	after := advIDs(m.b.LaneTasks("ready"))
	if strings.Join(before, ",") == strings.Join(after, ",") &&
		strings.Contains(m.status, "repositioned") {
		t.Errorf("nothing moved (%v) but the status line reports %q — every drop reports "+
			"success, so a clamped or no-op drop is indistinguishable from a real one",
			after, m.status)
	}
	_ = id
}
