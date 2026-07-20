package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
)

// ADVERSARIAL BUG HUNT. Every test in this file is written to FAIL against the
// current code and to name a concrete defect. Nothing here is a fix.

// ---------------------------------------------------------------------------
// A. tiny terminals: the frame must never exceed the terminal it was given.
// ---------------------------------------------------------------------------

func advFrameSize(t *testing.T, m *Model) (w, h int) {
	t.Helper()
	out := m.View().Content
	h = lg.Height(out)
	for _, line := range strings.Split(out, "\n") {
		if lw := lg.Width(line); lw > w {
			w = lw
		}
	}
	return w, h
}

func TestAdvFrameOverflowsAtSmallSizes(t *testing.T) {
	sizes := [][2]int{{1, 1}, {20, 5}, {20, 8}, {30, 7}, {40, 10}, {50, 12}, {28, 20}, {27, 20}}
	for _, s := range sizes {
		for _, view := range []string{"board", "peek", "table", "help"} {
			m := boardModel(t, s[0], s[1])
			switch view {
			case "peek":
				m.peekOpen = true
				m.syncPeek()
			case "table":
				m.view = viewTable
			case "help":
				m.fullHelp = true
			}
			m.relayout()
			gw, gh := advFrameSize(t, m)
			if gw > s[0] || gh > s[1] {
				t.Errorf("%s at %dx%d rendered %dx%d (overflows by %+d cols, %+d rows)",
					view, s[0], s[1], gw, gh, gw-s[0], gh-s[1])
			}
		}
	}
}

// The dragged ghost card is 28 cells wide and ~6 tall and is only clamped to
// max(0, w-28) / max(0, h-cardH). On a terminal narrower/shorter than a card
// that clamp yields 0 and the layer still overflows the canvas.
func TestAdvGhostOverflowsANarrowTerminal(t *testing.T) {
	m := boardModel(t, 24, 14)
	col := m.lay.Col(m.curLaneName())
	if col == nil || len(col.Cards) == 0 {
		t.Skip("no cards at this size")
	}
	box := col.Cards[0]
	m.Update(tea.MouseClickMsg{X: box.X + 2, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: box.X + 6, Y: box.Y + 3, Button: tea.MouseLeft})
	if !m.drag.moved {
		t.Fatal("drag did not arm")
	}
	gw, gh := advFrameSize(t, m)
	if gw > 24 || gh > 14 {
		t.Errorf("ghost frame is %dx%d, terminal is 24x14", gw, gh)
	}
}

// ---------------------------------------------------------------------------
// B. an empty board / empty lanes
// ---------------------------------------------------------------------------

type emptyProvider struct{ b *Board }

func (p *emptyProvider) Board() *Board { return p.b }
func (p *emptyProvider) Move(id, lane string, idx int) ([]string, error) {
	return p.b.MoveTo(id, lane, idx)
}
func (p *emptyProvider) Done(id string) error { return p.b.Close(id) }
func (p *emptyProvider) ToggleCheck(id string, i int) error {
	return p.b.ToggleCheck(id, i)
}
func (p *emptyProvider) SetBody(id, body string) error { return p.b.SetBody(id, body) }
func (p *emptyProvider) Reload() error                 { p.b = NewBoard(nil); return nil }

func TestAdvEmptyBoardSurvivesEveryGesture(t *testing.T) {
	m := New(&emptyProvider{b: NewBoard(nil)})
	m.w, m.h = 100, 30
	m.recompute()
	m.relayout()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on an empty board: %v", r)
		}
	}()

	keys := []string{"j", "k", "h", "l", "enter", "esc", "space", "t", "b", ">", "<",
		"d", "x", "K", "J", "[", "]", "g", "G", "v", "M", "?", "r"}
	for _, k := range keys {
		m.Update(tea.KeyPressMsg{Code: keyCodeFor(k), Text: keyTextFor(k)})
		_ = m.View().Content
	}
	// a mouse gesture over an empty board
	m.Update(tea.MouseClickMsg{X: 5, Y: 7, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 40, Y: 12, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 40, Y: 12, Button: tea.MouseLeft})
	_ = m.View().Content
}

func keyCodeFor(s string) rune {
	switch s {
	case "enter":
		return tea.KeyEnter
	case "esc":
		return tea.KeyEscape
	case "space":
		return tea.KeySpace
	}
	return []rune(s)[0]
}

func keyTextFor(s string) string {
	switch s {
	case "enter", "esc", "space":
		return ""
	}
	return s
}

// ---------------------------------------------------------------------------
// C. move mode
// ---------------------------------------------------------------------------

// The status line and doc.go both promise "esc restores". The BOARD is restored
// (nothing was mutated), but the CURSOR is left wherever the arrows parked the
// drop target: cancel never puts the selection back on the card you lifted.
func TestAdvMoveModeCancelLeavesTheCursorOnTheWrongTask(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	lifted := m.curTask()
	if lifted == nil {
		t.Fatal("no card to lift")
	}
	m.enterMove()
	// place it two lanes over and two slots down
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.mode != modeNormal {
		t.Fatalf("esc did not leave move mode")
	}
	if got := m.curTask(); got == nil || got.ID != lifted.ID {
		gotID := "<nil>"
		if got != nil {
			gotID = got.ID
		}
		t.Errorf("after esc the selection is %s, want the lifted card %s (lane %s idx %d)",
			gotID, lifted.ID, m.curLaneName(), m.curPos())
	}
}

// A mouse drag can be started and then a keyboard move mode entered on top of
// it, giving one card two owners. The reverse direction IS guarded
// (onMouseDown refuses while mode==modeMove); this direction is not, so the
// release commits one move and the following Enter commits a second.
func TestAdvKeyboardMoveModeCanBeEnteredMidDrag(t *testing.T) {
	m := boardModel(t, 140, 40)
	src := m.lay.Col("backlog")
	dst := m.lay.Col("ready")
	if src == nil || dst == nil || len(src.Cards) < 2 {
		t.Fatal("board too small")
	}
	box := src.Cards[1]
	id := src.Tasks[box.Idx].ID

	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: dst.X + 8, Y: dst.Top + 2, Button: tea.MouseLeft})
	if !m.drag.moved {
		t.Fatal("drag did not arm")
	}
	// The user presses `m` while still holding the button.
	m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if m.mode == modeMove && m.drag.armed {
		t.Errorf("card %s now has two owners: drag armed AND move mode active "+
			"(moveID=%s dropLane=%s / drag.id=%s dropLane=%s)",
			id, m.moveID, m.dropLane, m.drag.id, m.drag.dropLane)
	}
}

// ---------------------------------------------------------------------------
// D. drag
// ---------------------------------------------------------------------------

// Dragging a card out of every column and releasing there still commits a move
// into whichever lane the pointer last crossed, because dropLane is sticky and
// nothing checks that the RELEASE landed on a column. Releasing in the gutter,
// the title bar, or the footer should be a cancel, not a drop.
func TestAdvDropOutsideAnyColumnStillCommits(t *testing.T) {
	m := boardModel(t, 140, 40)
	src := m.lay.Col("backlog")
	dst := m.lay.Col("ready")
	if src == nil || dst == nil || len(src.Cards) < 2 {
		t.Fatal("board too small")
	}
	box := src.Cards[1]
	id := src.Tasks[box.Idx].ID
	before := m.b.Task(id).Status

	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	// cross "ready" ...
	m.Update(tea.MouseMotionMsg{X: dst.X + 8, Y: dst.Top + 2, Button: tea.MouseLeft})
	// ... then leave the board entirely and release far off to the right, in
	// the empty area past the last column.
	off := 139
	if _, ok := m.lay.laneAtX(off); ok {
		t.Skip("x=139 is inside a column at this width")
	}
	m.Update(tea.MouseMotionMsg{X: off, Y: 38, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: off, Y: 38, Button: tea.MouseLeft})

	if after := m.b.Task(id).Status; after != before {
		t.Errorf("released outside every column at x=%d,y=38 and %s still moved %s -> %s",
			off, id, before, after)
	}
}

// Releasing in the title/filter rows (y=0..1) — above every column — is also
// treated as a drop into the lane under x.
func TestAdvDropOnTheTitleBarCommits(t *testing.T) {
	m := boardModel(t, 140, 40)
	src := m.lay.Col("backlog")
	if src == nil || len(src.Cards) < 2 {
		t.Fatal("board too small")
	}
	box := src.Cards[1]
	id := src.Tasks[box.Idx].ID
	before := m.b.Task(id).Status
	beforeIdx := m.b.IndexIn(before, id)

	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	dst := m.lay.Col("in-progress")
	if dst == nil {
		t.Fatal("no in-progress column")
	}
	m.Update(tea.MouseMotionMsg{X: dst.X + 5, Y: 0, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: dst.X + 5, Y: 0, Button: tea.MouseLeft})

	after := m.b.Task(id).Status
	if after != before || m.b.IndexIn(after, id) != beforeIdx {
		t.Errorf("released on the TITLE BAR (y=0) and %s moved %s[%d] -> %s[%d]",
			id, before, beforeIdx, after, m.b.IndexIn(after, id))
	}
}

// The threshold is Manhattan >= 2, so a diagonal 1+1 twitch — the single most
// common accidental mouse movement — is a full drag, not a click.
func TestAdvDiagonalOneCellTwitchIsADrag(t *testing.T) {
	m := boardModel(t, 140, 40)
	col := m.lay.Col("backlog")
	if col == nil || len(col.Cards) < 2 {
		t.Fatal("board too small")
	}
	box := col.Cards[1]
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: box.X + 4, Y: box.Y + 2, Button: tea.MouseLeft})
	if m.drag.moved {
		t.Errorf("a 1-cell diagonal twitch (dx=1,dy=1, Manhattan 2) armed a real drag")
	}
}

// ---------------------------------------------------------------------------
// E. wheel
// ---------------------------------------------------------------------------

// The wheel clamps scroll to len(tasks)-1 with no regard for whether the column
// already fits entirely on screen, so a 1-card column can be scrolled until the
// card is gone... and a fully-visible column can be scrolled off the top.
func TestAdvWheelScrollsAFittingColumnIntoNothing(t *testing.T) {
	m := boardModel(t, 140, 40)
	lane := "in-progress"
	col := m.lay.Col(lane)
	if col == nil || col.Hidden != 0 || len(col.Tasks) == 0 {
		t.Skipf("%s does not fit entirely at this size", lane)
	}
	n := len(col.Tasks)
	for i := 0; i < n+3; i++ {
		m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 10, Button: tea.MouseWheelDown})
	}
	after := m.lay.Col(lane)
	if len(after.Cards) == 0 {
		t.Errorf("wheeled a fully-visible %d-card column (%s) until 0 cards render; scroll=%d",
			n, lane, after.Scroll)
	}
}

// ---------------------------------------------------------------------------
// F. dep logic
// ---------------------------------------------------------------------------

func advCyclicBoard() *Board {
	return NewBoard([]*Task{
		{ID: "a", Title: "A", Status: "ready", Priority: 10, Deps: []string{"b"}},
		{ID: "b", Title: "B", Status: "ready", Priority: 20, Deps: []string{"c"}},
		{ID: "c", Title: "C", Status: "ready", Priority: 30, Deps: []string{"a"}},
		{ID: "s", Title: "S", Status: "ready", Priority: 40, Deps: []string{"s"}},
		{ID: "e", Title: "E", Status: "backlog", Priority: 10, Type: "epic"},
		{ID: "k", Title: "K", Status: "backlog", Priority: 20, Parent: "e"},
	})
}

func TestAdvDepCycleIsSurvivable(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on a cyclic dep graph: %v", r)
			}
		}()
		b := advCyclicBoard()
		g := NewGraph(b)
		for _, id := range []string{"a", "b", "c", "s"} {
			_ = g.Actionable(id)
			_ = g.BlockedBy(id)
			_ = g.Stuck(id)
			_, _ = g.Progress(id, true)
			_ = g.TreeOf(id, dirBlockedBy, 4)
			_ = g.TreeOf(id, dirBlocks, 4)
		}
	}()
	<-done
}

// A parent CYCLE (which a git merge can produce, and which furrow's lint calls
// `parent-cycle`) must not hang the tree renderer either.
func TestAdvParentCycleIsSurvivable(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "p", Title: "P", Status: "backlog", Type: "epic", Parent: "q"},
		{ID: "q", Title: "Q", Status: "backlog", Type: "epic", Parent: "p"},
	})
	g := NewGraph(b)
	_ = g.Stuck("p")
	_, _ = g.Progress("p", true)
	_ = g.Children("p")
}

// A self-dep must block: `s` depends on `s`, which is not done, so `s` can
// never be actionable.
func TestAdvSelfDepBlocks(t *testing.T) {
	g := NewGraph(advCyclicBoard())
	if g.Actionable("s") {
		t.Error("a task that depends on itself is reported actionable")
	}
	if len(g.BlockedBy("s")) == 0 {
		t.Error("a self-dep is not counted as a blocker")
	}
}

// jumpToBlocker follows Deps[0] blindly; when the first dep is DONE and a later
// one is not, it jumps to a satisfied task and calls it "blocker 1/N".
func TestAdvJumpToBlockerReportsTheWrongCount(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "d1", Title: "closed", Status: "done", Priority: 10},
		{ID: "d2", Title: "open", Status: "ready", Priority: 10},
		{ID: "me", Title: "me", Status: "ready", Priority: 20, Deps: []string{"d1", "d2"}},
	})
	m := New(&emptyProvider{b: b})
	m.w, m.h = 140, 40
	m.recompute()
	m.relayout()
	if !m.selectID("me", false) {
		t.Fatal("cannot select me")
	}
	m.jumpToBlocker()
	if got := m.curTask(); got == nil || got.ID != "d2" {
		id := "<nil>"
		if got != nil {
			id = got.ID
		}
		t.Errorf("jumped to %s; the only real blocker is d2 (d1 is done). status=%q", id, m.status)
	}
}

// ---------------------------------------------------------------------------
// G. CJK / display width
// ---------------------------------------------------------------------------

// Every rendered frame line must measure <= the terminal width AND the board
// must line up: with Japanese titles, one mis-measured cell shears a column.
func TestAdvCJKColumnsAlignAtManyWidths(t *testing.T) {
	for w := 56; w <= 160; w += 3 {
		m := boardModel(t, w, 30)
		out := ansiStrip(m.View().Content)
		for i, line := range strings.Split(out, "\n") {
			if lw := lg.Width(line); lw > w {
				t.Errorf("w=%d line %d measures %d: %q", w, i, lw, line)
				break
			}
		}
		// the vertical borders of every visible column must be at the exact
		// same x on every card row.
		for _, c := range m.lay.Cols {
			for _, box := range c.Cards {
				card := renderCard(c.Tasks[box.Idx], m.g, m.th, c.W, cardNormal)
				for j, l := range strings.Split(card, "\n") {
					if cw := lg.Width(l); cw != c.W {
						t.Errorf("w=%d lane=%s card %s line %d is %d wide, want %d",
							w, c.Lane.Name, box.ID, j, cw, c.W)
					}
				}
			}
		}
	}
}

// The table view pads a Japanese title to a fixed cell count. A double-width
// glyph straddling the boundary must not make the row short or long.
func TestAdvTableRowsAreExactlyTerminalWidth(t *testing.T) {
	for _, w := range []int{72, 73, 74, 75, 100, 101, 140, 141} {
		m := boardModel(t, w, 30)
		m.view = viewTable
		m.relayout()
		out := ansiStrip(m.View().Content)
		for i, line := range strings.Split(out, "\n") {
			if lw := lg.Width(line); lw > w {
				t.Errorf("table w=%d row %d measures %d cells: %q", w, i, lw, line)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// H. filter x reorder
// ---------------------------------------------------------------------------

// shift+J/K reorder inside a FILTERED column. The visible neighbour is not the
// board neighbour, so "lower by one" must land immediately after the next
// VISIBLE card and must not jump over hidden ones... but it must also actually
// change the board order.
func TestAdvQuickReorderUnderAFilterMovesExactlyOneVisibleSlot(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.applyFilter("lane:backlog")
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	vis := append([]*Task(nil), m.cols["backlog"]...)
	if len(vis) < 3 {
		t.Fatal("need >=3 visible backlog tasks")
	}
	first, second := vis[0].ID, vis[1].ID
	m.quickReorder(+1)
	got := m.cols["backlog"]
	if got[0].ID != second || got[1].ID != first {
		t.Errorf("after shift+J the visible order is %s,%s; want %s,%s",
			got[0].ID, got[1].ID, second, first)
	}
}

// The `b` (blocked-only) toggle does a raw string ReplaceAll on the query, so a
// NEGATED is:blocked term leaves a stray "-" token behind.
func TestAdvBlockedToggleCorruptsANegatedQuery(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.applyFilter("-is:blocked")
	m.ti.SetValue("-is:blocked")
	before := m.countVisible()
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if m.q.Raw == "-" {
		t.Errorf("pressing b on %q left the query %q (a bare '-' bare-word term); "+
			"visible went %d -> %d", "-is:blocked", m.q.Raw, before, m.countVisible())
	}
}

// ---------------------------------------------------------------------------
// I. stale pointer after reload
// ---------------------------------------------------------------------------

// toggleCheck reads CheckProgress off the PRE-reload *Task. It only works
// because mockProvider hands back the same pointer; a provider that re-reads
// (which is the whole point of the Provider seam) would report stale counts.
func TestAdvToggleCheckReadsAStaleTaskPointer(t *testing.T) {
	m := boardModel(t, 140, 40)
	var target *Task
	for _, task := range m.b.Tasks() {
		if len(task.Checklist) > 2 {
			target = task
			break
		}
	}
	if target == nil {
		t.Skip("no checklist in the fixture")
	}
	m.selectID(target.ID, false)
	// swap in a provider that returns a DEEP COPY, as a real furrow --json
	// client would.
	m.prov = &copyingProvider{inner: m.prov}
	m.toggleCheck()
	live := m.b.Task(target.ID)
	d, tot := live.CheckProgress()
	want := fmt.Sprintf("%s checklist %d/%d", target.ID, d, tot)
	if m.status != want {
		t.Errorf("status says %q, the reloaded board says %q", m.status, want)
	}
}

type copyingProvider struct{ inner Provider }

func (p *copyingProvider) Board() *Board {
	src := p.inner.Board()
	out := make([]*Task, 0, len(src.Tasks()))
	for _, t := range src.Tasks() {
		c := *t
		c.Checklist = append([]ChecklistItem(nil), t.Checklist...)
		out = append(out, &c)
	}
	return NewBoard(out)
}
func (p *copyingProvider) Move(id, lane string, i int) ([]string, error) {
	return p.inner.Move(id, lane, i)
}
func (p *copyingProvider) Done(id string) error               { return p.inner.Done(id) }
func (p *copyingProvider) ToggleCheck(id string, i int) error { return p.inner.ToggleCheck(id, i) }
func (p *copyingProvider) SetBody(id, b string) error         { return p.inner.SetBody(id, b) }
func (p *copyingProvider) Reload() error                      { return p.inner.Reload() }
