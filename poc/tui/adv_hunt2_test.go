package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// J. differential move arithmetic: every (fromLane,fromIdx) x (toLane,dropIdx)
//    against a naive reference remove-then-insert.
// ---------------------------------------------------------------------------

func advIDs(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

// reference is what a human means by "drop this card into slot d of lane L, as
// the lane is currently DISPLAYED".
func advReference(cols map[string][]string, id, from, to string, dispIdx int) map[string][]string {
	out := map[string][]string{}
	for k, v := range cols {
		out[k] = append([]string(nil), v...)
	}
	// remove
	src := out[from]
	for i, x := range src {
		if x == id {
			src = append(src[:i], src[i+1:]...)
			break
		}
	}
	out[from] = src
	// insert: dispIdx counts the destination AS DISPLAYED, i.e. still holding
	// the moving card when from==to.
	idx := dispIdx
	if from == to && dispIdx > 0 {
		// the vacated slot above absorbs one
		fromIdx := -1
		for i, x := range cols[from] {
			if x == id {
				fromIdx = i
			}
		}
		if dispIdx > fromIdx {
			idx = dispIdx - 1
		}
	}
	dst := out[to]
	if idx > len(dst) {
		idx = len(dst)
	}
	if idx < 0 {
		idx = 0
	}
	dst = append(dst[:idx:idx], append([]string{id}, dst[idx:]...)...)
	out[to] = dst
	return out
}

func TestAdvMoveArithmeticAgainstAReference(t *testing.T) {
	lanes := []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}
	for _, from := range lanes {
		for _, to := range lanes {
			base := boardModel(t, 140, 40)
			src := base.cols[from]
			if len(src) == 0 {
				continue
			}
			for fi := range src {
				for di := 0; di <= len(base.cols[to]); di++ {
					m := boardModel(t, 140, 40)
					id := m.cols[from][fi].ID
					before := map[string][]string{}
					for _, l := range lanes {
						before[l] = advIDs(m.cols[l])
					}
					want := advReference(before, id, from, to, di)
					if _, err := m.commitMove(id, from, to, fi, di); err != nil {
						t.Fatalf("%s[%d] -> %s[%d]: %v", from, fi, to, di, err)
					}
					for _, l := range lanes {
						got := advIDs(m.cols[l])
						if strings.Join(got, ",") != strings.Join(want[l], ",") {
							t.Errorf("%s[%d] -> %s[%d]: lane %s is %v, want %v",
								from, fi, to, di, l, got, want[l])
						}
					}
				}
			}
		}
	}
}

// A drop that lands exactly where the card already was should be a NO-OP. It
// currently stamps Updated, which is furrow's staleness signal — the same
// signal the real store deliberately refuses to advance on a positional
// respace.
func TestAdvNoOpDropStampsUpdated(t *testing.T) {
	m := boardModel(t, 140, 40)
	id := m.cols["backlog"][1].ID
	task := m.b.Task(id)
	task.Updated = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	before := task.Updated

	// drag it two cells and put it straight back in its own slot
	col := m.lay.Col("backlog")
	box := col.Cards[1]
	m.Update(tea.MouseClickMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: box.X + 5, Y: box.Y + 2, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: box.X + 3, Y: box.Y + 1, Button: tea.MouseLeft})

	if got := m.b.Task(id).Updated; !got.Equal(before) {
		t.Errorf("a drop into the card's own slot advanced updated %s -> %s (and the "+
			"status line claims %q)", before.Format(time.RFC3339), got.Format(time.RFC3339), m.status)
	}
}

// ---------------------------------------------------------------------------
// K. wheel on a column that fits
// ---------------------------------------------------------------------------

func TestAdvWheelHidesCardsInAColumnThatFits(t *testing.T) {
	m := boardModel(t, 140, 44)
	var lane string
	for _, c := range m.lay.Cols {
		if c.Hidden == 0 && len(c.Tasks) >= 2 {
			lane = c.Lane.Name
			break
		}
	}
	if lane == "" {
		t.Skip("no fully-visible multi-card column at this size")
	}
	col := m.lay.Col(lane)
	n := len(col.Tasks)
	m.Update(tea.MouseWheelMsg{X: col.X + 4, Y: 10, Button: tea.MouseWheelDown})
	after := m.lay.Col(lane)
	if len(after.Cards) < n {
		t.Errorf("one wheel-down on %s (all %d cards fit, nothing below the fold) "+
			"scrolled to %d and now renders only %d cards",
			lane, n, after.Scroll, len(after.Cards))
	}
}

// ---------------------------------------------------------------------------
// L. compositor / negative coordinates
// ---------------------------------------------------------------------------

// At h<2 the status and help bars are placed at NEGATIVE y.
func TestAdvChromeIsPlacedAtNegativeY(t *testing.T) {
	m := boardModel(t, 60, 1)
	for _, l := range m.chromeLayers() {
		if l.GetY() < 0 {
			t.Errorf("a chrome layer is placed at y=%d on a 1-row terminal", l.GetY())
		}
	}
}

// ---------------------------------------------------------------------------
// M. peek geometry
// ---------------------------------------------------------------------------

// peekBox floors its height at 6 rows and anchors it at y=rowColHdr(2) without
// consulting m.h, so on any terminal shorter than 8 rows the panel hangs off
// the bottom of the frame. helpLayer has a MaxWidth/MaxHeight backstop; the
// peek has none.
func TestAdvPeekBoxExceedsTheTerminal(t *testing.T) {
	for _, h := range []int{5, 6, 7} {
		m := boardModel(t, 100, h)
		x, y, w, ph := m.peekBox()
		if y+ph > h {
			t.Errorf("h=%d: peek box y=%d h=%d ends at row %d (terminal has %d); x=%d w=%d",
				h, y, ph, y+ph, h, x, w)
		}
	}
}

// ---------------------------------------------------------------------------
// N. filter x cursor
// ---------------------------------------------------------------------------

// Typing a filter that hides the selected card silently leaves the cursor on a
// DIFFERENT task, and every subsequent destructive key (d = done, x = check,
// enter = move) acts on that one.
func TestAdvFilterCanSilentlyRepointTheCursor(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(3)
	before := m.curTask()
	if before == nil {
		t.Fatal("no selection")
	}
	m.applyFilter("is:blocked")
	after := m.curTask()
	if after != nil && after.ID != before.ID {
		t.Logf("selection moved %s -> %s after filtering (expected); the danger is "+
			"that nothing tells the user", before.ID, after.ID)
	}
	// Now the real defect: pressing `d` closes whatever the cursor landed on.
	if after == nil {
		t.Skip("nothing visible")
	}
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.b.Task(after.ID).Status != "done" {
		t.Fatal("d did not close the selection")
	}
	if before.ID != after.ID && !strings.Contains(m.status, after.ID) {
		t.Errorf("closed %s but the status line says %q", after.ID, m.status)
	}
}

// ---------------------------------------------------------------------------
// O. is:<bogus> is called non-fatal but empties the board
// ---------------------------------------------------------------------------

func TestAdvUnknownIsValueEmptiesTheBoardWhileClaimingToBeNonFatal(t *testing.T) {
	m := boardModel(t, 140, 40)
	total := m.countVisible()
	m.applyFilter("is:bogus")
	if m.countVisible() == 0 && len(m.q.Problems) > 0 {
		t.Errorf("ParseQuery documents an unrecognised token as \"reported but not fatal\", "+
			"yet is:bogus keeps the term and drops all %d tasks (problems=%v)",
			total, m.q.Problems)
	}
}

// ---------------------------------------------------------------------------
// P. peek content width
// ---------------------------------------------------------------------------

func TestAdvPeekLinesFitTheirBox(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {80, 24}, {60, 20}} {
		m := boardModel(t, size[0], size[1])
		m.peekOpen, m.treeOpen = true, true
		// pick a task with both directions populated
		for _, task := range m.b.Tasks() {
			if len(task.Deps) > 0 && len(m.g.Blocks(task.ID)) > 0 {
				m.selectID(task.ID, false)
				break
			}
		}
		m.syncPeek()
		_, _, w, _ := m.peekBox()
		inner := maxInt(10, w-4)
		for i, line := range strings.Split(ansiStrip(m.peekContent(inner)), "\n") {
			if lw := lg.Width(line); lw > inner {
				t.Errorf("%dx%d peek line %d is %d cells, box inner width is %d: %q",
					size[0], size[1], i, lw, inner, line)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Q. move mode commit while the drop lane scrolled out of the visible strip
// ---------------------------------------------------------------------------

func TestAdvMoveModeAcrossTheWholeStrip(t *testing.T) {
	m := boardModel(t, 90, 40) // only ~3 columns fit
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	id := m.curTask().ID
	m.enterMove()
	for i := 0; i < 5; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	wantLane := m.dropLane
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.b.Task(id).Status; got != wantLane {
		t.Errorf("committed into %s, want %s", got, wantLane)
	}
	if m.lay.Col(wantLane) == nil {
		t.Errorf("committed into %s but that lane is not in the visible strip "+
			"(laneOff=%d visible=%d)", wantLane, m.laneOff, m.lay.Visible)
	}
}

// ---------------------------------------------------------------------------
// R. status-line honesty on a failed / clamped move
// ---------------------------------------------------------------------------

func TestAdvMoveIntoAnEmptyFilteredLaneAppendsToTheRealEnd(t *testing.T) {
	m := boardModel(t, 140, 40)
	// hide everything in backlog, then move a ready card into backlog slot 0.
	m.applyFilter("lane:ready")
	if len(m.cols["backlog"]) != 0 {
		t.Fatal("backlog should be empty under this filter")
	}
	full := advIDs(m.b.LaneTasks("backlog"))
	if len(full) < 2 {
		t.Fatal("need a populated backlog")
	}
	id := m.cols["ready"][0].ID
	if _, err := m.commitMove(id, "ready", "backlog", 0, 0); err != nil {
		t.Fatal(err)
	}
	got := advIDs(m.b.LaneTasks("backlog"))
	if got[0] != id {
		t.Errorf("dropped into slot 0 of a (filtered-empty) backlog; the card landed at "+
			"index %d of the real lane %v — the gesture said TOP, the board says BOTTOM",
			indexOfStr(got, id), got)
	}
}

func indexOfStr(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// S. -dump smoke across pathological sizes (panic hunt)
// ---------------------------------------------------------------------------

func TestAdvRenderDoesNotPanicAtPathologicalSizes(t *testing.T) {
	sizes := [][2]int{{0, 0}, {1, 1}, {2, 2}, {-1, -1}, {1, 100}, {400, 1}, {3, 3}, {28, 6}}
	for _, s := range sizes {
		s := s
		t.Run(fmt.Sprintf("%dx%d", s[0], s[1]), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic at %dx%d: %v", s[0], s[1], r)
				}
			}()
			m := New(newMockProvider())
			m.w, m.h = s[0], s[1]
			m.recompute()
			m.relayout()
			_ = m.View().Content
			m.peekOpen, m.fullHelp = true, true
			m.syncPeek()
			m.relayout()
			_ = m.View().Content
			m.view = viewTable
			_ = m.View().Content
		})
	}
}
