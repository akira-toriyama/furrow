package main

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Keyboard move mode — GitHub Projects' board gesture, which transplants to a
// TUI almost too well: enter lifts the card, arrows place it, enter commits,
// esc restores.
//
// commitMove at the bottom of this file is the SINGLE mutation path for every
// reorder gesture in the app (move mode, shift+J/K, and a mouse drop), which is
// what keeps the two index translations applied exactly once each.

func (m *Model) enterMove() {
	if m.drag.armed {
		// The mirror of onMouseDown's guard. Without it a card could be lifted
		// by the keyboard while the mouse still held it: the release commits one
		// move and the following Enter commits a second.
		m.note("a mouse drag is in flight — release it first")
		return
	}
	t := m.curTask()
	if t == nil {
		m.note("nothing to move in %s", m.curLaneName())
		return
	}
	if m.view == viewTable {
		m.note("move mode is a board gesture — press v for the board")
		return
	}
	m.mode = modeMove
	m.moveID, m.moveFrom, m.moveFromIdx = t.ID, t.Status, m.curPos()
	m.dropLane, m.dropIdx = t.Status, m.curPos()
	// followDrop() walks m.curLane/m.curIdx along with the drop target, so cancel
	// has to be able to put them back. "esc restores" has to mean the SELECTION
	// too, not just the board: leaving the cursor two lanes over means the next
	// d / x / Enter silently acts on a different task.
	m.moveCurLane = m.curLane
	m.moveCurIdx = make(map[string]int, len(m.curIdx))
	for k, v := range m.curIdx {
		m.moveCurIdx[k] = v
	}
	m.note("MOVE %s — arrows place it, ⏎ commits, esc restores", t.ID)
}

// cancelMove restores both the board (nothing was mutated) and the cursor.
func (m *Model) cancelMove() {
	m.mode = modeNormal
	m.note("move cancelled — %s stayed in %s", m.moveID, m.moveFrom)
	m.curLane = m.moveCurLane
	if m.moveCurIdx != nil {
		m.curIdx = m.moveCurIdx
	}
	m.moveID, m.moveCurIdx = "", nil
	m.ensureVisible()
	m.syncPeek()
}

func (m *Model) onMoveKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.cancelMove()
		return nil

	case key.Matches(msg, m.keys.Commit):
		id, from, to, fi, di := m.moveID, m.moveFrom, m.dropLane, m.moveFromIdx, m.dropIdx
		m.mode, m.moveID, m.moveCurIdx = modeNormal, "", nil
		moved, err := m.commitMove(id, from, to, fi, di)
		if err != nil {
			m.fail("%v", err)
			return nil
		}
		switch {
		case !moved:
			m.note("%s did not move — that is where it already was", id)
		case from == to:
			m.note("%s repositioned in %s", id, to)
		default:
			m.note("%s: %s → %s", id, from, to)
		}
		return nil

	case key.Matches(msg, m.keys.Quit):
		return tea.Quit

	// GitHub Projects' documented ctrl+arrow extremes: top / bottom of the
	// column, leftmost / rightmost column.
	case key.Matches(msg, m.keys.MoveTop):
		m.dropIdx = 0
	case key.Matches(msg, m.keys.MoveBottom):
		m.dropIdx = m.dropSpan(m.dropLane)
	case key.Matches(msg, m.keys.MoveFirst):
		m.setDropLane(0)
	case key.Matches(msg, m.keys.MoveLast):
		m.setDropLane(len(m.b.Lanes()) - 1)

	case key.Matches(msg, m.keys.Up):
		m.dropIdx = maxInt(0, m.dropIdx-1)
	case key.Matches(msg, m.keys.Down):
		m.dropIdx = minInt(m.dropSpan(m.dropLane), m.dropIdx+1)
	case key.Matches(msg, m.keys.Left):
		m.shiftDropLane(-1)
	case key.Matches(msg, m.keys.Right):
		m.shiftDropLane(+1)
	}
	m.followDrop()
	return nil
}

// dropSpan is the largest insertion index for a lane. The moving card is still
// DISPLAYED in its source lane (nothing reflows under the cursor), so both the
// source and a foreign lane offer indices 0..len — the difference is undone by
// AdjustDropIndex at commit time, not here.
func (m *Model) dropSpan(lane string) int { return len(m.cols[lane]) }

func (m *Model) shiftDropLane(d int) { m.setDropLane(m.b.LaneIndex(m.dropLane) + d) }

func (m *Model) setDropLane(i int) {
	if i < 0 || i >= len(m.b.Lanes()) {
		return
	}
	m.dropLane = m.laneName(i)
	m.dropIdx = clamp(m.dropIdx, 0, m.dropSpan(m.dropLane))
}

// followDrop keeps the viewport tracking the drop target, so placing a card
// into an off-screen lane scrolls the board to it.
func (m *Model) followDrop() {
	if i := m.b.LaneIndex(m.dropLane); i >= 0 {
		m.curLane = i
	}
	m.curIdx[m.dropLane] = clamp(m.dropIdx, 0, maxInt(0, len(m.cols[m.dropLane])-1))
	m.ensureVisible()
}

// commitMove is the ONE mutation path for every reorder gesture — move mode,
// shift+J/K, and mouse drop all land here. It reports whether the board
// actually changed, so a clamped or no-op gesture can say so instead of
// claiming a reposition that never happened.
//
// dispIdx is an insertion index measured against the destination column AS
// DISPLAYED: it still counts the moving card (nothing reflows under the cursor)
// and it counts only tasks the filter lets through. Both facts have to be
// undone before the board can be told anything.
func (m *Model) commitMove(id, from, to string, fromIdx, dispIdx int) (moved bool, err error) {
	adj := AdjustDropIndex(from == to, fromIdx, dispIdx)

	visNoSelf := withoutID(m.cols[to], id)
	fullNoSelf := withoutID(m.b.LaneTasks(to), id)
	boardIdx := boardInsertIndex(fullNoSelf, visNoSelf, adj)

	// A drop into the slot the card already occupies must not touch the store.
	// furrow's contract is that positional bookkeeping does not advance
	// `updated` — that field is the staleness signal `lint` reads.
	if from == to && m.b.IndexIn(to, id) == boardIdx {
		m.selectID(id, false)
		return false, nil
	}

	renumbered, err := m.prov.Move(id, to, boardIdx)
	if err != nil {
		return false, err
	}
	m.reload()
	m.selectID(id, false)
	if len(renumbered) > 0 {
		m.note("respaced %s (%d neighbours renumbered)", to, len(renumbered))
	}
	return true, nil
}

func withoutID(ts []*Task, id string) []*Task {
	out := make([]*Task, 0, len(ts))
	for _, t := range ts {
		if t.ID != id {
			out = append(out, t)
		}
	}
	return out
}

// boardInsertIndex translates an insertion index in a FILTERED column into one
// in the full lane: land immediately before whichever visible task currently
// holds that slot. Without this, dropping "second from the top" of a filtered
// column would silently mean "second from the top" of the unfiltered lane.
//
// The two edges are where it used to lie about the gesture:
//   - nothing visible at all (an empty lane, or one the filter emptied): the
//     gesture said TOP, and the old fallback appended to the real BOTTOM.
//   - past the last visible card: that means "after the last card you can SEE",
//     not "after cards the filter is hiding from you".
func boardInsertIndex(full, vis []*Task, visIdx int) int {
	if len(vis) == 0 {
		return 0
	}
	if visIdx >= len(vis) {
		last := vis[len(vis)-1].ID
		for i, t := range full {
			if t.ID == last {
				return i + 1
			}
		}
		return len(full)
	}
	target := vis[visIdx].ID
	for i, t := range full {
		if t.ID == target {
			return i
		}
	}
	return len(full)
}
