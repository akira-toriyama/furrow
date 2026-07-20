package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// End-to-end tests drive a REAL tea.Program headlessly by feeding it the bytes a
// terminal would send and reading the final model back out. No TTY, no
// teatest harness, no timing assumptions: the whole script is written into an
// input buffer up front and the program drains it and quits.

const (
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyLeft  = "\x1b[D"
	keyRight = "\x1b[C"
	keyEnter = "\r"

	// Escape is NOT "\x1b" here, and this is the one non-obvious thing about
	// driving bubbletea from a byte buffer. A real terminal sends a bare 0x1b
	// and the parser disambiguates it from an escape SEQUENCE by TIMING. A
	// buffer has no timing: every byte arrives at once, so "\x1b"+"q" parses as
	// alt+q and "\x1b"+"\x1b[<0;5;5m" swallows the escape into the mouse
	// report. Either way the Esc never arrives and the program never quits —
	// the test just hangs.
	//
	// "\x1b[27u" is the Kitty keyboard protocol's CSI-u encoding of Escape,
	// which bubbletea v2 decodes unambiguously with no timing at all (verified:
	// it yields exactly one KeyPressMsg whose String() is "esc").
	keyEsc = "\x1b[27u"
)

// sgr encodes one SGR (1006) mouse report. cb 0 = left button, +32 = motion
// while held; 'M' is press/motion, 'm' is release. The wire is 1-BASED, while
// Update sees 0-based coordinates — the decoder normalises.
func sgr(cb, x, y int, final byte) string {
	return fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, x+1, y+1, final)
}

func mousePress(x, y int) string   { return sgr(0, x, y, 'M') }
func mouseMotion(x, y int) string  { return sgr(32, x, y, 'M') }
func mouseRelease(x, y int) string { return sgr(0, x, y, 'm') }

// run boots a real program at w x h, feeds it script, and returns the final
// model.
func run(t *testing.T, w, h int, script ...string) *Model {
	t.Helper()
	m := New(newMockProvider())

	var in bytes.Buffer
	for _, s := range script {
		in.WriteString(s)
	}
	in.WriteString("q") // always quit, so Run() returns rather than blocking

	var out bytes.Buffer
	final, err := tea.NewProgram(m,
		tea.WithInput(&in), tea.WithOutput(&out),
		tea.WithoutSignals(), tea.WithWindowSize(w, h),
	).Run()
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	fm, ok := final.(*Model)
	if !ok {
		t.Fatalf("final model is %T", final)
	}
	return fm
}

// geometry builds an identical off-line model so a test can ask where a card
// will be BEFORE it sends the mouse bytes that land on it.
func geometry(t *testing.T, w, h int) *Model {
	t.Helper()
	return boardModel(t, w, h)
}

func laneOf(m *Model, id string) string {
	if task := m.b.Task(id); task != nil {
		return task.Status
	}
	return "<gone>"
}

// ---- keyboard ---------------------------------------------------------------

func TestE2EKeyboardMoveModeCommits(t *testing.T) {
	probe := geometry(t, 140, 40)
	// The board opens on the first lane with work; its first card is the mover.
	mover := probe.curTask().ID
	if from := laneOf(probe, mover); from != "backlog" {
		t.Fatalf("fixture drifted: expected to start in backlog, got %s", from)
	}

	// m enters move mode, → walks the drop lane to `ready`, ⏎ commits.
	m := run(t, 140, 40, "m", keyRight, keyEnter)

	if got := laneOf(m, mover); got != "ready" {
		t.Errorf("%s is in %s, want ready", mover, got)
	}
	if m.mode != modeNormal {
		t.Errorf("commit must leave move mode, mode = %d", m.mode)
	}
	if !strings.Contains(m.status, "backlog → ready") {
		t.Errorf("status = %q, want it to name the move", m.status)
	}
	// The cursor follows the card it just moved.
	if m.curTask() == nil || m.curTask().ID != mover {
		t.Errorf("selection = %v, want it to follow %s", m.curTask(), mover)
	}
}

func TestE2EKeyboardMoveModeReordersWithinALane(t *testing.T) {
	probe := geometry(t, 140, 40)
	before := ids(probe.cols["backlog"])
	mover := before[0]

	// m, then ↓↓ to walk the drop slot down two boundaries, then commit.
	m := run(t, 140, 40, "m", keyDown, keyDown, keyEnter)

	after := ids(m.cols["backlog"])
	if laneOf(m, mover) != "backlog" {
		t.Fatalf("the card left its lane: %s", laneOf(m, mover))
	}
	if after[0] == mover {
		t.Errorf("the card did not move: %v", after)
	}
	if after[1] != mover {
		t.Errorf("order = %v, want %s at index 1", after, mover)
	}
	if len(after) != len(before) {
		t.Errorf("lane length changed: %d -> %d", len(before), len(after))
	}
}

func TestE2EKeyboardEscRestores(t *testing.T) {
	probe := geometry(t, 140, 40)
	mover := probe.curTask().ID
	before := ids(probe.cols["backlog"])

	m := run(t, 140, 40, "m", keyRight, keyRight, keyDown, keyEsc)

	if got := laneOf(m, mover); got != "backlog" {
		t.Errorf("esc must restore the original lane, got %s", got)
	}
	if got := ids(m.cols["backlog"]); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("esc must restore the original order:\n got %v\nwant %v", got, before)
	}
	if m.mode != modeNormal {
		t.Error("esc must leave move mode")
	}
	if !strings.Contains(m.status, "cancelled") {
		t.Errorf("status = %q", m.status)
	}
}

func TestE2EKeyboardNavigationAndPeekAndFilter(t *testing.T) {
	// space opens the peek; / types a filter and ⏎ applies it.
	m := run(t, 140, 40, " ", "/", "lane:ready", keyEnter)

	if !m.peekOpen {
		t.Error("space must open the detail peek")
	}
	if m.q.Raw != "lane:ready" {
		t.Errorf("filter = %q", m.q.Raw)
	}
	if n := m.countVisible(); n != 1 {
		t.Errorf("lane:ready matched %d tasks, want 1", n)
	}
	// The filter must actually reach the frame.
	frame := ansiStrip(m.View().Content)
	if !strings.Contains(frame, "lane:ready") {
		t.Error("the applied filter is not shown in the filter bar")
	}
	if strings.Contains(frame, "t-jv3j") {
		t.Error("a filtered-out task is still on the board")
	}
}

func TestE2EJumpToBlockerAndBack(t *testing.T) {
	// Filter to the blocked tasks, select the first, jump to its blocker, then
	// pop back. Max chain depth on the real board is 5, so two presses reach any
	// root blocker.
	m := run(t, 140, 40, "/", "is:blocked", keyEnter, ">")

	cur := m.curTask()
	if cur == nil {
		t.Fatal("no selection after the jump")
	}
	if len(m.jumpStack) != 1 {
		t.Fatalf("jump stack = %v, want one entry", m.jumpStack)
	}
	origin := m.jumpStack[0]
	// The cursor must be sitting on an actual unfinished blocker of the origin.
	if !contains(m.g.BlockedBy(origin), cur.ID) {
		t.Errorf("jumped to %s, which does not block %s (%v)", cur.ID, origin, m.g.BlockedBy(origin))
	}
	// A blocker hidden by the filter has to be pinned into view, never dropped.
	if !m.q.Match(cur, m.g) && !m.pinned[cur.ID] {
		t.Error("a blocker hidden by the filter must be pinned, not lost")
	}

	back := run(t, 140, 40, "/", "is:blocked", keyEnter, ">", "<")
	if back.curTask() == nil || back.curTask().ID != origin {
		t.Errorf("< returned to %v, want %s", back.curTask(), origin)
	}
	if len(back.jumpStack) != 0 {
		t.Errorf("the stack must pop, got %v", back.jumpStack)
	}
}

func TestE2EDoneAndLaneCycle(t *testing.T) {
	probe := geometry(t, 140, 40)
	target := probe.curTask().ID

	m := run(t, 140, 40, "d")
	if got := laneOf(m, target); got != "done" {
		t.Errorf("d closed %s into %s, want done", target, got)
	}
	if m.b.Task(target).Closed.IsZero() {
		t.Error("closing must stamp Closed")
	}

	// ] and [ cycle a lane forward and back, and land where they started.
	m2 := run(t, 140, 40, "]", "[")
	if got := laneOf(m2, target); got != "backlog" {
		t.Errorf("] then [ left %s in %s", target, got)
	}
}

func TestE2EMouseToggleIsDeclarative(t *testing.T) {
	// MouseMode is a per-render View field in bubbletea v2, so toggling mouse
	// tracking at runtime costs nothing — the escape hatch for the terminal's
	// own text selection.
	on := run(t, 140, 40)
	if on.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("mouse tracking should start on, in CellMotion")
	}
	off := run(t, 140, 40, "M")
	if off.View().MouseMode != tea.MouseModeNone {
		t.Error("M must turn mouse tracking off")
	}
	if again := run(t, 140, 40, "M", "M"); again.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("M must toggle back on")
	}
}

// ---- mouse ------------------------------------------------------------------

func TestE2EMouseDragAcrossColumns(t *testing.T) {
	const w, h = 140, 40
	probe := geometry(t, w, h)

	src := probe.lay.Col("backlog")
	dstCol := probe.lay.Col("ready")
	if src == nil || dstCol == nil || len(src.Cards) < 2 {
		t.Fatal("expected populated backlog and ready columns")
	}
	grab := src.Cards[1]
	mover := src.Tasks[grab.Idx].ID
	dstTop := dstCol.Cards[0]
	before := ids(probe.cols["ready"])

	m := run(t, w, h,
		mousePress(grab.X+3, grab.Y+1),
		// Several motion events, as a real terminal emits.
		mouseMotion(grab.X+10, grab.Y+1),
		mouseMotion(dstCol.X+3, dstTop.Y+1),
		mouseMotion(dstCol.X+4, dstTop.Y+1),
		mouseRelease(dstCol.X+4, dstTop.Y+1),
	)

	if got := laneOf(m, mover); got != "ready" {
		t.Fatalf("%s ended in %s, want ready (status: %s)", mover, got, m.status)
	}
	after := ids(m.cols["ready"])
	if len(after) != len(before)+1 {
		t.Errorf("ready = %v, want one more than %v", after, before)
	}
	// Dropped on the top half of the first card => it goes in above it.
	if after[0] != mover {
		t.Errorf("ready order = %v, want %s first", after, mover)
	}
	if m.drag.armed || m.drag.moved {
		t.Error("the drag state must be cleared after a drop")
	}
}

func TestE2EMouseDragWithinAColumnReorders(t *testing.T) {
	const w, h = 140, 40
	probe := geometry(t, w, h)
	col := probe.lay.Col("backlog")
	if len(col.Cards) < 3 {
		t.Fatalf("need >= 3 cards, got %d", len(col.Cards))
	}
	grab, target := col.Cards[0], col.Cards[2]
	mover := col.Tasks[grab.Idx].ID

	// Drag the top card down onto the LOWER half of the third card, which means
	// "insert below it" — the same-lane case that needs AdjustDropIndex.
	m := run(t, w, h,
		mousePress(grab.X+3, grab.Y+1),
		mouseMotion(grab.X+3, grab.Y+4),
		mouseMotion(grab.X+3, target.Y+target.H-1),
		mouseRelease(grab.X+3, target.Y+target.H-1),
	)

	after := ids(m.cols["backlog"])
	if laneOf(m, mover) != "backlog" {
		t.Fatalf("the card left its lane: %s", laneOf(m, mover))
	}
	if got := indexOf(m.cols["backlog"], mover); got != 2 {
		t.Errorf("%s landed at index %d, want 2 — order %v", mover, got, after)
	}
}

// A press and a release one cell apart is a CLICK. Without a threshold, every
// selection click that twitches silently reorders the board.
func TestE2EMouseTwitchIsAClickNotAMove(t *testing.T) {
	const w, h = 140, 40
	probe := geometry(t, w, h)
	col := probe.lay.Col("backlog")
	grab := col.Cards[2]
	mover := col.Tasks[grab.Idx].ID
	before := ids(probe.cols["backlog"])

	m := run(t, w, h,
		mousePress(grab.X+3, grab.Y+1),
		mouseMotion(grab.X+4, grab.Y+1), // Manhattan distance 1 — under the threshold
		mouseRelease(grab.X+4, grab.Y+1),
	)

	if got := ids(m.cols["backlog"]); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("a twitch reordered the board:\n got %v\nwant %v", got, before)
	}
	// It must still SELECT, because that is what a click is for.
	if m.curTask() == nil || m.curTask().ID != mover {
		t.Errorf("the click did not select %s (got %v)", mover, m.curTask())
	}
	if !strings.Contains(m.status, "selected") {
		t.Errorf("status = %q, want it to report a selection", m.status)
	}
}

// Esc mid-drag cancels, and the release that inevitably follows (the button is
// still physically down) must be swallowed rather than treated as a drop.
func TestE2EMouseEscCancelsAndSwallowsTheRelease(t *testing.T) {
	const w, h = 140, 40
	probe := geometry(t, w, h)
	src := probe.lay.Col("backlog")
	dst := probe.lay.Col("ready")
	grab := src.Cards[0]
	mover := src.Tasks[grab.Idx].ID
	beforeBacklog := ids(probe.cols["backlog"])
	beforeReady := ids(probe.cols["ready"])

	m := run(t, w, h,
		mousePress(grab.X+3, grab.Y+1),
		mouseMotion(dst.X+3, dst.Top+1),
		keyEsc,
		mouseRelease(dst.X+3, dst.Top+1), // must be a no-op
	)

	if got := laneOf(m, mover); got != "backlog" {
		t.Errorf("%s moved to %s despite the cancel", mover, got)
	}
	if got := ids(m.cols["backlog"]); strings.Join(got, ",") != strings.Join(beforeBacklog, ",") {
		t.Errorf("backlog changed: %v vs %v", got, beforeBacklog)
	}
	if got := ids(m.cols["ready"]); strings.Join(got, ",") != strings.Join(beforeReady, ",") {
		t.Errorf("ready changed: %v vs %v", got, beforeReady)
	}
	if m.drag.armed {
		t.Error("the swallowed release must clear the drag state")
	}
}

// Motion events that carry no button (some terminals do not report one) must
// still drive the drag: the button is remembered from the press.
func TestE2EMouseDragSurvivesButtonlessMotion(t *testing.T) {
	const w, h = 140, 40
	probe := geometry(t, w, h)
	src := probe.lay.Col("backlog")
	dst := probe.lay.Col("ready")
	grab := src.Cards[0]
	mover := src.Tasks[grab.Idx].ID

	m := New(newMockProvider())
	m.w, m.h = w, h
	m.recompute()
	m.relayout()

	m.Update(tea.MouseClickMsg{X: grab.X + 3, Y: grab.Y + 1, Button: tea.MouseLeft})
	// Button: MouseNone — exactly what a terminal that drops the button reports.
	m.Update(tea.MouseMotionMsg{X: dst.X + 3, Y: dst.Top + 2, Button: tea.MouseNone})
	m.Update(tea.MouseReleaseMsg{X: dst.X + 3, Y: dst.Top + 2, Button: tea.MouseNone})

	if got := laneOf(m, mover); got != "ready" {
		t.Errorf("%s ended in %s — a button-less motion event dropped the drag", mover, got)
	}
}

func TestE2EWheelScrollsTheHoveredColumn(t *testing.T) {
	const w, h = 140, 40
	m := New(newMockProvider())
	m.w, m.h = w, h
	m.recompute()
	m.relayout()

	col := m.lay.Col("backlog")
	if col.Hidden == 0 {
		t.Skip("backlog fits entirely; nothing to scroll")
	}
	x := col.X + 3
	m.Update(tea.MouseWheelMsg{X: x, Y: col.Top + 2, Button: tea.MouseWheelDown})
	if m.scroll["backlog"] != 1 {
		t.Errorf("wheel down = %d, want 1", m.scroll["backlog"])
	}
	m.Update(tea.MouseWheelMsg{X: x, Y: col.Top + 2, Button: tea.MouseWheelUp})
	if m.scroll["backlog"] != 0 {
		t.Errorf("wheel up = %d, want 0", m.scroll["backlog"])
	}
	// It must not scroll past the top.
	m.Update(tea.MouseWheelMsg{X: x, Y: col.Top + 2, Button: tea.MouseWheelUp})
	if m.scroll["backlog"] != 0 {
		t.Errorf("wheel up past the top = %d, want 0", m.scroll["backlog"])
	}
}

// The drop indicator must be invisible to hit-testing, or it would swallow the
// click aimed at the card underneath it.
func TestDropIndicatorIsNotHitTestable(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	m.enterMove()
	m.dropIdx = 1
	m.relayout()

	l := m.dropLayer()
	if l == nil {
		t.Fatal("no drop indicator while in move mode")
	}
	if id := l.GetID(); id != "" {
		t.Errorf("the drop indicator has id %q; Hit() only skips id-less layers", id)
	}
}
