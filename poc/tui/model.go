package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type mode int

const (
	modeNormal mode = iota
	modeMove        // a card is lifted; arrows place it
	modeFilter      // the filter input has the keyboard
)

type viewKind int

const (
	viewBoard viewKind = iota
	viewTable
	// viewGraph is the dependency graph — a FULL-SCREEN view, not an overlay.
	// The board's geometry is irrelevant inside it, so it gets the whole
	// terminal instead of a cramped panel floating over the columns.
	viewGraph
)

// Model is the whole application state.
type Model struct {
	prov Provider
	b    *Board
	g    *Graph
	th   *theme
	ms   *measurer // card-height cache, kept across frames (see layout.go)
	keys keyMap
	help help.Model
	ti   textinput.Model
	vp   viewport.Model

	w, h     int
	mode     mode
	view     viewKind
	fullHelp bool
	peekOpen bool
	treeOpen bool
	mouseOn  bool

	q      Query
	pinned map[string]bool // ids forced visible despite the filter (jump targets)
	cols   map[string][]*Task

	laneOff   int
	curLane   int
	curIdx    map[string]int
	scroll    map[string]int
	jumpStack []string
	tableIdx  int

	// keyboard move mode. dropIdx is measured against the destination column
	// AS DISPLAYED, so it needs AdjustDropIndex on commit — same convention as
	// the mouse drag, one arithmetic path for both.
	moveID      string
	moveFrom    string
	moveFromIdx int
	dropLane    string
	dropIdx     int
	// The cursor as it was when the card was lifted, so esc can restore the
	// SELECTION and not just the board.
	moveCurLane int
	moveCurIdx  map[string]int

	drag dragState

	// The dependency graph view. graphFocus is what the picture is rooted on,
	// graphSel is the node the cursor is on (they start equal and diverge as
	// you walk), and graphStack retraces the re-roots.
	graphFocus  string
	graphSel    string
	graphRadius int
	graphScroll int
	graphStack  []string
	graphLay    *egoLayout

	lay       *layout
	status    string
	statusErr bool
}

// New builds the model over a provider.
func New(p Provider) *Model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.SetWidth(48)
	vp := viewport.New()
	vp.MouseWheelEnabled = true

	m := &Model{
		prov:    p,
		th:      newTheme(true),
		ms:      newMeasurer(nil, nil),
		keys:    defaultKeys(),
		help:    help.New(),
		ti:      ti,
		vp:      vp,
		w:           240,
		h:           60,
		mouseOn:     true,
		pinned:      map[string]bool{},
		curIdx:      map[string]int{},
		scroll:      map[string]int{},
		graphRadius: 2,
		status:      "space detail · ⇧space dep graph · ⏎ move mode · / filter · > blocker · ? help",
	}
	m.reload()
	// Start on the first lane that actually has work.
	for i, l := range m.b.Lanes() {
		if len(m.cols[l.Name]) > 0 {
			m.curLane = i
			break
		}
	}
	m.recompute()
	return m
}

// Init requests the terminal background so the palette can pick light or dark —
// lipgloss v2 removed AdaptiveColor, so this is now the idiomatic route.
func (m *Model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *Model) reload() {
	m.b = m.prov.Board()
	m.g = NewGraph(m.b)
	m.recompute()
}

// recompute rebuilds the derived graph and the filtered columns, then clamps
// every cursor. Called after any mutation: with 24 tasks it is free, and it
// removes a whole class of stale-index bugs.
func (m *Model) recompute() {
	m.g = NewGraph(m.b)
	m.ms.rebind(m.g, m.th)
	m.cols = map[string][]*Task{}
	for _, l := range m.b.Lanes() {
		var keep []*Task
		for _, t := range m.b.LaneTasks(l.Name) {
			if m.q.Empty() || m.q.Match(t, m.g) || m.pinned[t.ID] {
				keep = append(keep, t)
			}
		}
		m.cols[l.Name] = keep
	}
	for name, idx := range m.curIdx {
		m.curIdx[name] = clamp(idx, 0, maxInt(0, len(m.cols[name])-1))
	}
	m.curLane = clamp(m.curLane, 0, len(m.b.Lanes())-1)
	m.tableIdx = clamp(m.tableIdx, 0, maxInt(0, len(m.tableRows())-1))
	m.ensureVisible()
	m.syncPeek()
}

func (m *Model) laneName(i int) string { return m.b.Lanes()[i].Name }

func (m *Model) curLaneName() string { return m.laneName(m.curLane) }

func (m *Model) curTasks() []*Task { return m.cols[m.curLaneName()] }

func (m *Model) curPos() int {
	return clamp(m.curIdx[m.curLaneName()], 0, maxInt(0, len(m.curTasks())-1))
}

// curTask is the selected task, nil in an empty column.
func (m *Model) curTask() *Task {
	if m.view == viewTable {
		rows := m.tableRows()
		if m.tableIdx < len(rows) {
			return rows[m.tableIdx]
		}
		return nil
	}
	ts := m.curTasks()
	if len(ts) == 0 {
		return nil
	}
	return ts[m.curPos()]
}

// ensureVisible scrolls the focused column and pans the lane strip so the
// cursor is on screen.
//
// It is called from the paths that MOVE THE SELECTION, never blanket-called
// from Update. That distinction is load-bearing: run it on every event and it
// re-asserts "the cursor must be visible" immediately after the mouse wheel
// scrolled a column, so the column snaps back and the wheel appears dead. A
// column scrolled away from its selection is a legitimate state — GitHub's
// columns do it too.
func (m *Model) ensureVisible() {
	vis, colW := boardCols(maxInt(m.w, 1), len(m.b.Lanes()))
	if m.curLane < m.laneOff {
		m.laneOff = m.curLane
	}
	if m.curLane >= m.laneOff+vis {
		m.laneOff = m.curLane - vis + 1
	}
	m.laneOff = clamp(m.laneOff, 0, maxInt(0, len(m.b.Lanes())-vis))

	name := m.curLaneName()
	m.scroll[name] = scrollToShow(m.cols[name], m.curPos(), m.scroll[name],
		boardTop, maxInt(m.h, 1)-footerH, colW, m.ms)
}

// Update is the whole event loop.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.ti.SetWidth(maxInt(20, msg.Width-30))
		m.ensureVisible()
		m.syncPeek()

	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark())

	case editorDoneMsg:
		if msg.err != nil {
			m.fail("editor: %v", msg.err)
		} else if err := m.prov.SetBody(msg.id, msg.body); err != nil {
			m.fail("%v", err)
		} else {
			m.note("%s body updated (in memory only)", msg.id)
		}
		m.reload()

	case tea.KeyPressMsg:
		if c := m.onKey(msg); c != nil {
			cmds = append(cmds, c)
		}

	case tea.MouseClickMsg:
		m.onMouseDown(msg)
	case tea.MouseMotionMsg:
		if c := m.onMouseMove(msg); c != nil {
			cmds = append(cmds, c)
		}
	case tea.MouseReleaseMsg:
		m.onMouseUp(msg)
	case tea.MouseWheelMsg:
		m.onWheel(msg)

	case dragScrollMsg:
		if c := m.onDragScroll(msg); c != nil {
			cmds = append(cmds, c)
		}
	}

	m.relayout()
	return m, tea.Batch(cmds...)
}

// relayout measures the frame. It runs at the end of every Update so a mouse
// event arriving before the next render still hit-tests against current
// geometry.
func (m *Model) relayout() {
	// A terminal is at least one cell. -dump takes its size from the command
	// line, a resize can report 0, and every downstream strings.Repeat would
	// panic on a negative.
	m.w, m.h = maxInt(m.w, 1), maxInt(m.h, 1)
	m.lay = buildLayout(m.w, m.h, m.b.Lanes(), m.cols, m.laneOff, m.scroll, m.ms)
	m.laneOff = m.lay.LaneOff
}

func (m *Model) note(f string, a ...any) { m.status, m.statusErr = fmt.Sprintf(f, a...), false }
func (m *Model) fail(f string, a ...any) { m.status, m.statusErr = fmt.Sprintf(f, a...), true }

// ---- keyboard ---------------------------------------------------------------

func (m *Model) onKey(msg tea.KeyPressMsg) tea.Cmd {
	// A modal text input owns Esc, full stop. Checking cancelDrag() first let a
	// still-armed drag eat the Esc meant to dismiss the filter, leaving the
	// input modal with no way out but Enter.
	if m.mode == modeFilter {
		return m.onFilterKey(msg)
	}
	// Esc while a mouse button is down cancels the drag before anything else
	// gets to interpret it — and leaves the drag armed so the release that
	// follows is swallowed rather than treated as a drop.
	if key.Matches(msg, m.keys.Cancel) && m.cancelDrag() {
		return nil
	}
	if m.mode == modeMove {
		return m.onMoveKey(msg)
	}
	// The graph is a full-screen MODE, not an overlay: it owns the arrows,
	// enter and esc while it is up, so it is routed before the board keys
	// rather than being a case inside them.
	if m.view == viewGraph {
		return m.onGraphKey(msg)
	}
	return m.onNormalKey(msg)
}

// onGraphKey is the graph view's whole keyboard surface. Everything the board
// does to the BOARD is deliberately absent — the graph is a reading and walking
// tool, and a stray `d` closing a task you were only looking at would be a
// nasty surprise.
func (m *Model) onGraphKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.fullHelp = !m.fullHelp

	case key.Matches(msg, m.keys.Cancel):
		if m.fullHelp {
			m.fullHelp = false
			return nil
		}
		m.closeGraph()

	case key.Matches(msg, m.keys.GraphRoot):
		m.rerootGraph()

	case key.Matches(msg, m.keys.JumpBack):
		m.graphBack()

	case key.Matches(msg, m.keys.GraphRadius):
		switch msg.String() {
		case "1", "2", "3":
			m.graphRadius = int(msg.String()[0] - '0')
			m.note("hop radius %d", m.graphRadius)
		case "0":
			m.graphRadius = graphAllRadius
			m.note("hop radius all")
		default:
			m.cycleGraphRadius()
		}
		m.graphScroll = 0

	case key.Matches(msg, m.keys.Graph):
		// ⇧space on the node you are already on is a no-op re-root; treat it as
		// "root here", which is what the gesture means on the board.
		m.rerootGraph()

	case key.Matches(msg, m.keys.PeekScroll):
		if msg.String() == "ctrl+d" {
			m.graphScroll += maxInt(1, m.graphCanvasH()/2)
		} else {
			m.graphScroll -= maxInt(1, m.graphCanvasH()/2)
		}
		m.graphScroll = maxInt(0, m.graphScroll)

	case key.Matches(msg, m.keys.View):
		m.closeGraph()

	case key.Matches(msg, m.keys.Up):
		m.graphMove(0, -1)
	case key.Matches(msg, m.keys.Down):
		m.graphMove(0, +1)
	case key.Matches(msg, m.keys.Left):
		m.graphMove(-1, 0)
	case key.Matches(msg, m.keys.Right):
		m.graphMove(+1, 0)
	}
	return nil
}

func (m *Model) onFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.mode = modeNormal
		m.ti.Blur()
		m.applyFilter(m.q.Raw) // discard the in-progress edit
		return nil
	case key.Matches(msg, m.keys.Commit):
		m.mode = modeNormal
		m.ti.Blur()
		m.applyFilter(m.ti.Value())
		return nil
	}
	var c tea.Cmd
	m.ti, c = m.ti.Update(msg)
	m.applyFilter(m.ti.Value()) // live filtering as you type
	return c
}

func (m *Model) applyFilter(s string) {
	prev := m.curTask()
	m.q = ParseQuery(s)
	if strings.TrimSpace(s) == "" {
		m.pinned = map[string]bool{} // clearing the filter clears jump pins too
	}
	m.recompute()
	if prev != nil {
		m.selectID(prev.ID, false)
	}
}

func (m *Model) onNormalKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.fullHelp = !m.fullHelp

	case key.Matches(msg, m.keys.Cancel):
		switch {
		case m.treeOpen:
			m.treeOpen = false
		case m.peekOpen:
			m.peekOpen = false
		case !m.q.Empty():
			m.applyFilter("")
			m.ti.SetValue("")
			m.note("filter cleared")
		}

	case key.Matches(msg, m.keys.Filter):
		// Entering a modal input mid-drag would leave the drag armed and its
		// release would commit a move nobody is looking at any more.
		m.cancelDrag()
		m.mode = modeFilter
		m.ti.SetValue(m.q.Raw)
		return m.ti.Focus()

	case key.Matches(msg, m.keys.View):
		if m.view == viewBoard {
			m.view, m.tableIdx = viewTable, 0
			if t := m.curTask(); t != nil {
				m.selectID(t.ID, false)
			}
			m.note("table view — v returns to the board")
		} else {
			m.view = viewBoard
			m.note("board view")
		}

	case key.Matches(msg, m.keys.Mouse):
		m.mouseOn = !m.mouseOn
		if m.mouseOn {
			m.note("mouse tracking ON — drag cards; the terminal's own text selection needs the bypass modifier")
		} else {
			m.note("mouse tracking OFF — the terminal owns the mouse again; keyboard still does everything")
		}

	case key.Matches(msg, m.keys.Graph):
		m.openGraph()

	case key.Matches(msg, m.keys.Peek):
		m.peekOpen = !m.peekOpen
		m.syncPeek()

	case key.Matches(msg, m.keys.PeekScroll):
		// Driven explicitly rather than by forwarding keys to the viewport:
		// viewport's default keymap owns up/down, which would make j/k scroll
		// the peek AND move the board cursor at the same time.
		if m.peekOpen {
			if msg.String() == "ctrl+d" {
				m.vp.ScrollDown(maxInt(1, m.vp.Height()/2))
			} else {
				m.vp.ScrollUp(maxInt(1, m.vp.Height()/2))
			}
		}

	case key.Matches(msg, m.keys.Tree):
		m.treeOpen = !m.treeOpen
		if m.treeOpen {
			m.peekOpen = true
		}
		m.syncPeek()

	case key.Matches(msg, m.keys.OnlyBlock):
		// TOKEN-wise, never a substring ReplaceAll: on "-is:blocked" that left a
		// bare "-" behind, which parses as a bare-word term and quietly changed
		// what the board showed while the status line said "off".
		if q, had := dropToken(m.q.Raw, "is:blocked"); had {
			m.applyFilter(q)
			m.note("blocked-only off")
		} else {
			m.applyFilter(strings.TrimSpace(m.q.Raw + " is:blocked"))
			m.note("blocked-only on — %d tasks are waiting on something", m.countVisible())
		}
		m.ti.SetValue(m.q.Raw)

	case key.Matches(msg, m.keys.JumpBlock):
		m.jumpToBlocker()
	case key.Matches(msg, m.keys.JumpBack):
		m.jumpBack()

	case key.Matches(msg, m.keys.Reload):
		if err := m.prov.Reload(); err != nil {
			m.fail("reload: %v", err)
		} else {
			m.reload()
			m.note("reloaded from the fixture — session edits discarded")
		}

	case key.Matches(msg, m.keys.Done):
		if t := m.curTask(); t != nil {
			if err := m.prov.Done(t.ID); err != nil {
				m.fail("%v", err)
			} else {
				m.reload()
				if n := len(m.g.OpenBlocks(t.ID)); n > 0 {
					m.note("closed %s — unblocked %d task(s)", t.ID, n)
				} else {
					m.note("closed %s", t.ID)
				}
			}
		}

	case key.Matches(msg, m.keys.Check):
		m.toggleCheck()

	case key.Matches(msg, m.keys.Edit):
		if t := m.curTask(); t != nil {
			return m.editCmd(t)
		}

	case key.Matches(msg, m.keys.Move):
		m.enterMove()

	case key.Matches(msg, m.keys.QuickUp):
		m.quickReorder(-1)
	case key.Matches(msg, m.keys.QuickDown):
		m.quickReorder(+1)

	case key.Matches(msg, m.keys.LaneBack):
		m.cycleLane(-1)
	case key.Matches(msg, m.keys.LaneFwd):
		m.cycleLane(+1)

	case key.Matches(msg, m.keys.Top):
		m.setPos(0)
	case key.Matches(msg, m.keys.Bottom):
		m.setPos(len(m.curTasks()) - 1)

	case key.Matches(msg, m.keys.Up):
		m.moveCursor(0, -1)
	case key.Matches(msg, m.keys.Down):
		m.moveCursor(0, +1)
	case key.Matches(msg, m.keys.Left):
		m.moveCursor(-1, 0)
	case key.Matches(msg, m.keys.Right):
		m.moveCursor(+1, 0)
	case key.Matches(msg, m.keys.NextCol):
		m.moveCursor(+1, 0)
	case key.Matches(msg, m.keys.PrevCol):
		m.moveCursor(-1, 0)
	}
	return nil
}

func (m *Model) countVisible() int {
	n := 0
	for _, l := range m.b.Lanes() {
		n += len(m.cols[l.Name])
	}
	return n
}

func (m *Model) setPos(i int) {
	if m.view == viewTable {
		m.tableIdx = clamp(i, 0, maxInt(0, len(m.tableRows())-1))
		m.syncPeek()
		return
	}
	m.curIdx[m.curLaneName()] = clamp(i, 0, maxInt(0, len(m.curTasks())-1))
	m.ensureVisible()
	m.syncPeek()
}

// moveCursor walks the grid. Moving between columns keeps the row position when
// it exists, which is what makes a kanban feel like a grid rather than a set of
// unrelated lists.
func (m *Model) moveCursor(dx, dy int) {
	if m.view == viewTable {
		if dy != 0 {
			m.tableIdx = clamp(m.tableIdx+dy, 0, maxInt(0, len(m.tableRows())-1))
			m.syncPeek()
		}
		return
	}
	if dy != 0 {
		m.setPos(m.curPos() + dy)
		return
	}
	if dx == 0 {
		return
	}
	want := m.curPos()
	for i := m.curLane + dx; i >= 0 && i < len(m.b.Lanes()); i += dx {
		if len(m.cols[m.laneName(i)]) == 0 {
			// Land on an empty lane anyway: you must be able to drop into one,
			// and a lane you cannot focus is a lane you cannot drop into.
			m.curLane = i
			m.curIdx[m.laneName(i)] = 0
			m.ensureVisible()
			m.syncPeek()
			return
		}
		m.curLane = i
		m.curIdx[m.laneName(i)] = clamp(want, 0, len(m.cols[m.laneName(i)])-1)
		m.ensureVisible()
		m.syncPeek()
		return
	}
}

// selectID moves the cursor onto a task, pinning it past the filter when asked
// — a jump that lands nowhere is worse than no jump.
func (m *Model) selectID(id string, pin bool) bool {
	t := m.b.Task(id)
	if t == nil {
		return false
	}
	if pin {
		m.pinned[id] = true
		m.recompute()
	}
	for i, l := range m.b.Lanes() {
		for j, x := range m.cols[l.Name] {
			if x.ID == id {
				m.curLane, m.curIdx[l.Name] = i, j
				if m.view == viewTable {
					for k, r := range m.tableRows() {
						if r.ID == id {
							m.tableIdx = k
						}
					}
				}
				m.ensureVisible()
				m.syncPeek()
				return true
			}
		}
	}
	return false
}

// jumpToBlocker is the one interactive dep feature a static drawing cannot do.
// The real board's longest chain is 5 edges, so two presses reach any root
// blocker.
func (m *Model) jumpToBlocker() {
	t := m.curTask()
	if t == nil {
		return
	}
	blockers := m.g.BlockedBy(t.ID)
	if len(blockers) == 0 {
		m.note("%s is not blocked", t.ID)
		return
	}
	target := blockers[0]
	if !m.g.Known(target) {
		m.fail("%s depends on %s, which is not on this board", t.ID, target)
		return
	}
	m.jumpStack = append(m.jumpStack, t.ID)
	pinned := ""
	if !m.selectID(target, false) {
		m.selectID(target, true)
		pinned = " (pinned past the filter)"
	}
	m.note("→ %s (blocker %d/%d of %s)%s  ·  < to come back",
		target, 1, len(blockers), t.ID, pinned)
}

func (m *Model) jumpBack() {
	if len(m.jumpStack) == 0 {
		m.note("jump stack empty")
		return
	}
	id := m.jumpStack[len(m.jumpStack)-1]
	m.jumpStack = m.jumpStack[:len(m.jumpStack)-1]
	m.selectID(id, true)
	m.note("← %s (%d left on the stack)", id, len(m.jumpStack))
}

// toggleCheck flips the first unfinished checklist item, or the last finished
// one when all are done. A POC shortcut: a real client would put a cursor in
// the checklist.
func (m *Model) toggleCheck() {
	t := m.curTask()
	if t == nil {
		return
	}
	if len(t.Checklist) == 0 {
		m.note("%s has no checklist", t.ID)
		return
	}
	idx := -1
	for i, c := range t.Checklist {
		if !c.Done {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = len(t.Checklist) - 1
	}
	if err := m.prov.ToggleCheck(t.ID, idx); err != nil {
		m.fail("%v", err)
		return
	}
	m.reload()
	// Re-read from the RELOADED board. Reading CheckProgress off the pre-reload
	// pointer only worked because mockProvider hands back the same *Task; a
	// provider that returns copies — the whole point of the Provider seam — would
	// report the count from before the toggle.
	live := m.b.Task(t.ID)
	if live == nil {
		m.note("%s toggled", t.ID)
		return
	}
	d, tot := live.CheckProgress()
	m.note("%s checklist %d/%d", t.ID, d, tot)
}

// dropToken removes every occurrence of `tok` from a whitespace-separated query,
// reporting whether it was there.
func dropToken(raw, tok string) (string, bool) {
	var keep []string
	had := false
	for _, f := range strings.Fields(raw) {
		if f == tok {
			had = true
			continue
		}
		keep = append(keep, f)
	}
	return strings.Join(keep, " "), had
}

// cycleLane moves a task one lane over without entering move mode, appending it
// at the end of the destination.
func (m *Model) cycleLane(d int) {
	t := m.curTask()
	if t == nil {
		return
	}
	i := m.b.LaneIndex(t.Status) + d
	if i < 0 || i >= len(m.b.Lanes()) {
		m.note("no lane that way")
		return
	}
	dest := m.laneName(i)
	if _, err := m.prov.Move(t.ID, dest, len(m.b.LaneTasks(dest))); err != nil {
		m.fail("%v", err)
		return
	}
	m.reload()
	m.selectID(t.ID, false)
	m.note("%s → %s", t.ID, dest)
}

// quickReorder is shift+K / shift+J: nudge within the lane without the ceremony
// of move mode.
func (m *Model) quickReorder(d int) {
	t := m.curTask()
	if t == nil {
		return
	}
	vis := m.cols[t.Status]
	from := -1
	for i, x := range vis {
		if x.ID == t.ID {
			from = i
		}
	}
	if from < 0 {
		return
	}
	to := from + d
	if to < 0 || to >= len(vis) {
		m.note("%s is already at the %s of %s", t.ID, endName(d), t.Status)
		return
	}
	moved, err := m.commitMove(t.ID, t.Status, t.Status, from, to+boolToInt(d > 0))
	if err != nil {
		m.fail("%v", err)
		return
	}
	if !moved {
		m.note("%s did not move", t.ID)
		return
	}
	m.note("%s %s in %s", t.ID, map[bool]string{true: "lowered", false: "raised"}[d > 0], t.Status)
}

func endName(d int) string {
	if d < 0 {
		return "top"
	}
	return "bottom"
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
