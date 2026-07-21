package main

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// ---------------------------------------------------------------------------
// T. scroll offsets survive a filter that makes them meaningless
// ---------------------------------------------------------------------------

// m.scroll is never reset when the filter shrinks a column. buildLayout clamps
// it to len(tasks)-1 per FRAME but the model keeps the stale value, so a column
// that now holds 2 cards renders scrolled to the last one — cards 0..n-2 are
// invisible with acres of blank space below, and only a "1 above" hint.
func TestAdvStaleScrollHidesCardsAfterFiltering(t *testing.T) {
	m := boardModel(t, 140, 40)
	col := m.lay.Col("backlog")
	if col == nil || col.Hidden == 0 {
		t.Skip("backlog is not scrollable at this size")
	}
	// scroll backlog well down
	for i := 0; i < 6; i++ {
		m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 12, Button: tea.MouseWheelDown})
	}
	// now filter it down to a handful that would all fit
	m.applyFilter("lane:backlog is:blocked")
	m.relayout()
	after := m.lay.Col("backlog")
	if after == nil {
		t.Skip("backlog not visible")
	}
	if after.Scroll > 0 && after.Hidden == 0 {
		t.Errorf("after filtering, backlog holds %d cards that all fit, yet the column "+
			"is still scrolled to %d — cards 0..%d are unreachable without scrolling back up",
			len(after.Tasks), after.Scroll, after.Scroll-1)
	}
}

// The same stale offset, reached with the keyboard only.
func TestAdvWheelHidesCardsInAFittingColumnConstructed(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.applyFilter("lane:ready")
	m.relayout()
	col := m.lay.Col("ready")
	if col == nil || col.Hidden != 0 || len(col.Tasks) < 2 {
		t.Skipf("ready: cards=%d hidden=%d", len(col.Tasks), col.Hidden)
	}
	n := len(col.Cards)
	m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 10, Button: tea.MouseWheelDown})
	after := m.lay.Col("ready")
	if len(after.Cards) < n {
		t.Errorf("one wheel-down on a column where all %d cards fit (Hidden=0) scrolled "+
			"to %d and now renders %d cards — the top card is simply gone",
			n, after.Scroll, len(after.Cards))
	}
}

// ---------------------------------------------------------------------------
// U. dropping into an empty column
// ---------------------------------------------------------------------------

func TestAdvDragIntoAnEmptyColumn(t *testing.T) {
	m := boardModel(t, 140, 40)
	if len(m.cols["inbox"]) != 0 {
		t.Skip("inbox is not empty in the fixture")
	}
	src := m.lay.Col("backlog")
	dst := m.lay.Col("inbox")
	if src == nil || dst == nil || len(src.Cards) < 1 {
		t.Fatal("board too small")
	}
	box := src.Cards[0]
	id := src.Tasks[box.Idx].ID
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: dst.X + 8, Y: dst.Top + 3, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: dst.X + 8, Y: dst.Top + 3, Button: tea.MouseLeft})
	if got := m.b.Task(id).Status; got != "inbox" {
		t.Errorf("dropped %s into the empty inbox column; it is in %s", id, got)
	}
}

// Dropping BELOW the last card of a short column (in the empty space under it)
// must append, not land at slot 0.
func TestAdvDragBelowTheLastCardAppends(t *testing.T) {
	m := boardModel(t, 140, 40)
	src := m.lay.Col("backlog")
	dst := m.lay.Col("ready")
	if src == nil || dst == nil || len(src.Cards) < 1 || len(dst.Cards) < 1 {
		t.Fatal("board too small")
	}
	box := src.Cards[0]
	id := src.Tasks[box.Idx].ID
	last := dst.Cards[len(dst.Cards)-1]
	deepY := last.Y + last.H + 3 // empty space well below the last card
	if deepY >= dst.Bot {
		t.Skip("no empty space below the last ready card")
	}
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: dst.X + 8, Y: deepY, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: dst.X + 8, Y: deepY, Button: tea.MouseLeft})
	got := m.b.LaneTasks("ready")
	if got[len(got)-1].ID != id {
		var ids []string
		for _, x := range got {
			ids = append(ids, x.ID)
		}
		t.Errorf("dropped %s in the empty space below every ready card; lane is now %v",
			id, ids)
	}
}

// ---------------------------------------------------------------------------
// V. the drag ghost when the source card is scrolled off
// ---------------------------------------------------------------------------

func TestAdvDragSurvivesTheSourceScrollingAway(t *testing.T) {
	m := boardModel(t, 140, 40)
	src := m.lay.Col("backlog")
	if src == nil || src.Hidden == 0 || len(src.Cards) < 1 {
		t.Skip("backlog not scrollable")
	}
	box := src.Cards[0]
	id := src.Tasks[box.Idx].ID
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	// drag downward hard so the edge auto-scroll kicks in
	for y := box.Y + 2; y < 40; y++ {
		m.Update(tea.MouseMotionMsg{X: box.X + 3, Y: y, Button: tea.MouseLeft})
	}
	m.Update(tea.MouseReleaseMsg{X: box.X + 3, Y: 37, Button: tea.MouseLeft})
	if m.b.Task(id) == nil {
		t.Fatalf("%s vanished", id)
	}
	if m.b.Task(id).Status != "backlog" {
		t.Errorf("a straight-down drag inside backlog moved %s to %s",
			id, m.b.Task(id).Status)
	}
}

// ---------------------------------------------------------------------------
// W. filter-mode keys that leak
// ---------------------------------------------------------------------------

// While the filter input has the keyboard, a MOUSE CLICK is ignored
// (onMouseDown returns early) but the WHEEL is not, so scrolling changes the
// board under a modal text input. Minor, but it is the kind of asymmetry that
// says the modality was not thought through.
func TestAdvWheelWorksInFilterModeButClicksDoNot(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.mode = modeFilter
	col := m.lay.Col("backlog")
	if col == nil || col.Hidden == 0 {
		t.Skip("backlog not scrollable")
	}
	before := m.scroll["backlog"]
	m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 12, Button: tea.MouseWheelDown})
	after := m.scroll["backlog"]
	if after != before {
		t.Errorf("the wheel scrolled backlog %d -> %d while the filter input was modal "+
			"(a click at the same spot is correctly ignored)", before, after)
	}
}

// ---------------------------------------------------------------------------
// X. `>` jump pins leak
// ---------------------------------------------------------------------------

// Every `<` (jump back) pins its target permanently, and pins are only cleared
// by emptying the filter. Bouncing between two tasks inflates "+N pinned by
// jump" forever and progressively defeats the active filter.
func TestAdvJumpBackLeaksPins(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.applyFilter("lane:backlog")
	var start *Task
	for _, task := range m.cols["backlog"] {
		if len(m.g.BlockedBy(task.ID)) > 0 {
			start = task
			break
		}
	}
	if start == nil {
		t.Skip("no blocked task in backlog")
	}
	m.selectID(start.ID, false)
	for i := 0; i < 4; i++ {
		m.jumpToBlocker()
		m.jumpBack()
	}
	if len(m.pinned) > 1 {
		t.Errorf("after 4 jump/back round trips the filter is defeated for %d pinned ids %v",
			len(m.pinned), pinIDs(m.pinned))
	}
}

func pinIDs(p map[string]bool) []string {
	var out []string
	for k := range p {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// Y. renderTable panics on a negative width
// ---------------------------------------------------------------------------

func TestAdvTableViewPanicsOnNegativeWidth(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderTable panicked at w=-1: %v (table.go strings.Repeat(\"─\", m.w))", r)
		}
	}()
	m := New(newMockProvider())
	m.w, m.h = -1, 20
	m.view = viewTable
	m.recompute()
	m.relayout()
	_ = m.View().Content
}

// ---------------------------------------------------------------------------
// Z. the bubbletea v2 key-string trap, for every binding that can fall into it
// ---------------------------------------------------------------------------

// key.Matches compares Key.String(), so a binding written with the wrong
// spelling compiles, runs, and silently never fires — the failure mode that
// makes WithKeys(" ") a no-op where WithKeys("space") works. Every binding whose
// name is not a bare letter is at risk, so each one is driven with the message a
// terminal would actually produce.
//
// (This replaces a test that only logged a complaint about a scratch file which
// is not in the tree. It asserted nothing, which is precisely what it accused
// that file of.)
func TestKeyBindingsMatchTheirRealKeyStrings(t *testing.T) {
	k := defaultKeys()
	cases := []struct {
		msg  tea.KeyPressMsg
		want key.Binding
		name string
	}{
		{tea.KeyPressMsg{Code: tea.KeySpace}, k.Peek, "space"},
		{tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModShift}, k.Graph, "shift+space"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, k.Move, "enter"},
		{tea.KeyPressMsg{Code: tea.KeyEscape}, k.Cancel, "esc"},
		{tea.KeyPressMsg{Code: tea.KeyTab}, k.NextCol, "tab"},
		{tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}, k.MoveTop, "ctrl+up"},
		{tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl}, k.MoveBottom, "ctrl+down"},
		{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl}, k.MoveFirst, "ctrl+left"},
		{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, k.MoveLast, "ctrl+right"},
		{tea.KeyPressMsg{Code: 'K', Text: "K"}, k.QuickUp, "K"},
		{tea.KeyPressMsg{Code: 'J', Text: "J"}, k.QuickDown, "J"},
		{tea.KeyPressMsg{Code: 'M', Text: "M"}, k.Mouse, "M"},
	}
	for _, tc := range cases {
		if !key.Matches(tc.msg, tc.want) {
			t.Errorf("%s: key.Matches saw String()=%q, which is in none of %v",
				tc.name, tc.msg.String(), tc.want.Keys())
		}
	}

	// shift+space opens the dep graph; plain space opens the peek. If the two
	// ever collapsed to the same string, one gesture would shadow the other.
	//
	// And they DO collapse on a terminal that does not speak the Kitty keyboard
	// protocol: a legacy terminal cannot encode a modified space at all, so
	// shift+space arrives as a bare space and the graph is unreachable. That is
	// not hypothetical — it is what the first person to try this POC hit. The
	// binding therefore carries "S" as the portable alias, and View() asks for
	// keyboard enhancements so the pretty gesture works where it can.
	shifted := tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModShift}
	if key.Matches(shifted, k.Peek) {
		t.Error("shift+space also matches the peek binding; the two gestures collide")
	}
	if key.Matches(tea.KeyPressMsg{Code: tea.KeySpace}, k.Graph) {
		t.Error("plain space matches the graph binding; the two gestures collide")
	}
	if !key.Matches(tea.KeyPressMsg{Code: 'S', Text: "S"}, k.Graph) {
		t.Error("S must open the graph too: it is the only way in on a terminal " +
			"that cannot encode shift+space")
	}
}
