package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Mouse drag-and-drop.
//
// Four things here are not obvious and all four are load-bearing:
//
//  1. THE THRESHOLD IS CHEBYSHEV, NOT MANHATTAN. A press and a release one cell
//     apart is a CLICK, not a move. Manhattan distance got this wrong on the
//     diagonal — dx=1,dy=1 scores 2 and armed a real drag, and a diagonal twitch
//     is the single most common accidental mouse movement. max(|dx|,|dy|) treats
//     every one-cell neighbour, diagonals included, as a click.
//  2. THE BUTTON IS REMEMBERED, NOT RE-READ. Some terminals do not report the
//     button on motion or release events, so the drag records it at press time.
//     Testing `msg.Button == MouseLeft` on a motion event drops the drag on
//     those terminals.
//  3. ESC CANCELS, AND THE RELEASE THAT FOLLOWS MUST BE A NO-OP. The button is
//     still physically down after Esc, so the release still arrives; it has to
//     be swallowed rather than treated as a drop.
//  4. THE RELEASE DECIDES, NOT THE LAST LANE CROSSED. dropLane used to be
//     sticky, so yanking a card off the board and letting go still committed
//     the move into whatever column the pointer last brushed past — including a
//     release on the title bar. Pulling a card away from the board is the
//     universal escape hatch; it must cancel.
const dragThreshold = 2 // Chebyshev cells

// dragScrollInterval is the repeat rate of the edge auto-scroll. A terminal
// emits motion events only when the pointer MOVES, so parking it at a column
// edge stops delivering events — and the scroll with them. A tick makes holding
// still at the edge keep scrolling, the way every GUI board does.
const dragScrollInterval = 80 * time.Millisecond

// dragScrollMsg is one auto-scroll repeat. `seq` makes it self-cancelling: any
// newer pointer event bumps the counter and every in-flight tick from the old
// position becomes a no-op, so there is never more than one live timer.
type dragScrollMsg struct{ seq int }

type dragState struct {
	armed     bool // a button went down on a card
	moved     bool // the threshold was passed: this is a real drag
	cancelled bool // Esc during the drag; the pending release is a no-op

	id      string
	from    string
	fromIdx int
	button  tea.MouseButton

	pressX, pressY int
	x, y           int
	grabDX, grabDY int

	dropLane string
	dropIdx  int

	scrollDir int // -1 up, +1 down, 0 parked away from an edge
	scrollSeq int
}

func (d *dragState) reset() { *d = dragState{} }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// inPeek reports whether a point is inside the open side-peek, which owns its
// own clicks and scrolling.
func (m *Model) inPeek(x, y int) bool {
	if !m.peekOpen {
		return false
	}
	px, py, pw, ph := m.peekBox()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// dropTarget resolves a point to a lane that can actually receive a card: it
// must be inside a column horizontally AND inside that column's card band
// vertically. The chrome rows, the footer and the empty area past the last
// column are all "off the board".
func (m *Model) dropTarget(x, y int) (string, bool) {
	if m.lay == nil {
		return "", false
	}
	lane, ok := m.lay.laneAtX(x)
	if !ok {
		return "", false
	}
	c := m.lay.Col(lane)
	if c == nil || y < c.Top || y >= c.Bot {
		return "", false
	}
	return lane, true
}

func (m *Model) onMouseDown(msg tea.MouseClickMsg) {
	if !m.mouseOn || m.mode == modeFilter || m.lay == nil {
		return
	}
	if msg.Button != tea.MouseLeft {
		return
	}
	if m.inPeek(msg.X, msg.Y) {
		return
	}
	if m.mode == modeMove {
		// A keyboard move is in flight; a click would give the card two owners.
		m.note("finish the keyboard move first (⏎ commit / esc cancel)")
		return
	}

	lane, idx, ok := m.lay.cardAt(msg.X, msg.Y)
	if !ok {
		if lane != "" {
			if i := m.b.LaneIndex(lane); i >= 0 {
				m.curLane = i
			}
		}
		return
	}
	c := m.lay.Col(lane)
	if c == nil || idx >= len(c.Tasks) {
		return
	}
	t := c.Tasks[idx]

	if i := m.b.LaneIndex(lane); i >= 0 {
		m.curLane = i
	}
	m.curIdx[lane] = idx
	m.syncPeek()

	var box cardBox
	for _, b := range c.Cards {
		if b.Idx == idx {
			box = b
		}
	}
	m.drag = dragState{
		armed: true, id: t.ID, from: lane, fromIdx: idx, button: msg.Button,
		pressX: msg.X, pressY: msg.Y, x: msg.X, y: msg.Y,
		grabDX: msg.X - box.X, grabDY: msg.Y - box.Y,
		dropLane: lane, dropIdx: idx,
	}
}

func (m *Model) onMouseMove(msg tea.MouseMotionMsg) tea.Cmd {
	if !m.drag.armed || m.drag.cancelled || m.lay == nil {
		return nil
	}
	// Deliberately NOT checking msg.Button: see (2) above.
	m.drag.x, m.drag.y = msg.X, msg.Y
	if !m.drag.moved {
		if maxInt(abs(msg.X-m.drag.pressX), abs(msg.Y-m.drag.pressY)) < dragThreshold {
			return nil
		}
		m.drag.moved = true
	}
	if lane, ok := m.lay.laneAtX(msg.X); ok {
		m.drag.dropLane = lane
		m.drag.dropIdx = m.lay.idxAtY(lane, msg.Y)
	}

	// Edge auto-scroll: arm a repeating tick while the pointer sits in the hot
	// zone, and disarm the moment it leaves.
	m.drag.scrollSeq++
	m.drag.scrollDir = 0
	if c := m.lay.Col(m.drag.dropLane); c != nil {
		switch {
		case msg.Y <= c.Top:
			m.drag.scrollDir = -1
		case msg.Y >= c.Bot-1:
			m.drag.scrollDir = +1
		}
	}
	if m.drag.scrollDir == 0 {
		return nil
	}
	m.dragScrollStep()
	return m.dragScrollTick()
}

// dragScrollStep scrolls the hovered column one card, reporting whether it
// actually could. It re-measures immediately so the drop index the status line
// shows matches the rows now under the pointer.
func (m *Model) dragScrollStep() bool {
	c := m.lay.Col(m.drag.dropLane)
	if c == nil || m.drag.scrollDir == 0 {
		return false
	}
	switch {
	case m.drag.scrollDir < 0 && c.Scroll > 0:
		m.scroll[m.drag.dropLane] = c.Scroll - 1
	case m.drag.scrollDir > 0 && c.Hidden > 0:
		m.scroll[m.drag.dropLane] = c.Scroll + 1
	default:
		return false
	}
	m.relayout()
	m.drag.dropIdx = m.lay.idxAtY(m.drag.dropLane, m.drag.y)
	return true
}

func (m *Model) dragScrollTick() tea.Cmd {
	seq := m.drag.scrollSeq
	return tea.Tick(dragScrollInterval, func(time.Time) tea.Msg {
		return dragScrollMsg{seq: seq}
	})
}

func (m *Model) onDragScroll(msg dragScrollMsg) tea.Cmd {
	if !m.drag.armed || !m.drag.moved || m.drag.cancelled {
		return nil
	}
	if msg.seq != m.drag.scrollSeq || m.drag.scrollDir == 0 {
		return nil // superseded by a newer pointer position
	}
	if !m.dragScrollStep() {
		return nil // hit the end of the column: stop ticking
	}
	return m.dragScrollTick()
}

func (m *Model) onMouseUp(msg tea.MouseReleaseMsg) {
	if !m.drag.armed {
		return
	}
	if m.drag.cancelled {
		m.drag.reset()
		return
	}
	if !m.drag.moved {
		// A click, not a drag. Selection already happened on press.
		m.note("selected %s — drag it, or press ⏎ for move mode", m.drag.id)
		m.drag.reset()
		return
	}

	id, from, fromIdx := m.drag.id, m.drag.from, m.drag.fromIdx
	to, onBoard := m.dropTarget(msg.X, msg.Y)
	if !onBoard {
		m.drag.reset()
		m.note("released off the board — %s stayed in %s", id, from)
		return
	}
	// The RELEASE decides where it lands, not the last lane the pointer brushed.
	dropIdx := m.lay.idxAtY(to, msg.Y)
	m.drag.reset()

	moved, err := m.commitMove(id, from, to, fromIdx, dropIdx)
	if err != nil {
		m.fail("%v", err)
		return
	}
	switch {
	case !moved:
		m.note("%s did not move — dropped back into its own slot", id)
	case from == to:
		m.note("%s repositioned in %s", id, to)
	default:
		m.note("%s: %s → %s (dropped)", id, from, to)
	}
}

func (m *Model) onWheel(msg tea.MouseWheelMsg) {
	if !m.mouseOn || m.mode == modeFilter {
		// Symmetry with onMouseDown: while the filter input is modal it owns the
		// mouse too. Scrolling the board under a modal text input is the kind of
		// asymmetry that says the modality was not thought through.
		return
	}
	if m.inPeek(msg.X, msg.Y) {
		switch msg.Button {
		case tea.MouseWheelUp:
			m.vp.ScrollUp(3)
		case tea.MouseWheelDown:
			m.vp.ScrollDown(3)
		}
		return
	}
	if m.lay == nil {
		return
	}
	lane, ok := m.lay.laneAtX(msg.X)
	if !ok {
		return
	}
	c := m.lay.Col(lane)
	if c == nil {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scroll[lane] = maxInt(0, c.Scroll-1)
	case tea.MouseWheelDown:
		// Only scroll if there is something below the fold. Clamping to
		// len(tasks)-1 instead let a column whose cards all fit be scrolled
		// until the top one was simply gone.
		if c.Hidden > 0 {
			m.scroll[lane] = c.Scroll + 1
		}
	}
}

// cancelDrag is Esc while a button is down. The drag stops immediately but stays
// armed so the release that inevitably follows is swallowed.
func (m *Model) cancelDrag() bool {
	if !m.drag.armed || m.drag.cancelled {
		return false
	}
	id := m.drag.id
	m.drag.cancelled, m.drag.moved = true, false
	m.drag.scrollDir, m.drag.scrollSeq = 0, m.drag.scrollSeq+1
	m.note("drag cancelled — %s stayed in %s", id, m.drag.from)
	return true
}
