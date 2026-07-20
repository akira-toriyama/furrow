package main

import (
	"fmt"
	"strings"

	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The dependency graph VIEW: a full-screen, Obsidian-shaped picture of the
// structure around one task. graph.go decided the geometry; this file paints it.
//
// Two rules keep it honest at 240-400 columns:
//
//  1. NODE BOXES GO THROUGH LIPGLOSS, EDGES GO THROUGH THE RUNE GRID. Text is
//     measured in DISPLAY cells (a Japanese title is two cells per glyph);
//     box-drawing characters are all single-width and can therefore live in a
//     plain rune buffer. Mixing the two is the CJK shear bug, so the frame is
//     built as alternating bands: a lipgloss-composed node row, then a rune-grid
//     channel, then a node row.
//
//  2. HEIGHT IS NEGOTIATED, NOT ASSUMED. Channel heights fall out of the edge
//     routing, so the title-line budget is whatever is left over divided by the
//     number of rows. That is why the same graph reads as 2 title lines at
//     240x60 and 3 at 400x90 instead of scrolling on the smaller one.

const (
	graphRowHdr  = 1 // focus / radius / counts / legend
	graphTop     = 2 // first canvas row
	graphStripH  = 8 // the selected-node detail strip, border included
	graphMinNodeLines = 1
	graphMaxNodeLines = 3
)

// graphRadii is the hop-radius cycle. The last entry is "all" — bounded by
// graphAllRadius, which exceeds the real board's longest chain (5).
var graphRadii = []int{1, 2, 3, graphAllRadius}

func radiusLabel(r int) string {
	if r >= graphAllRadius {
		return "all"
	}
	return fmt.Sprintf("%d", r)
}

// graphCanvasH is how many rows the drawing itself may use.
func (m *Model) graphCanvasH() int {
	return maxInt(1, m.h-graphTop-m.graphStripHeight()-footerH)
}

// graphStripHeight shrinks the detail strip on a short terminal rather than
// letting it push the graph off the screen entirely.
func (m *Model) graphStripHeight() int {
	if m.h < 24 {
		return minInt(graphStripH, maxInt(0, m.h-graphTop-footerH-3))
	}
	return graphStripH
}

// buildGraph lays out the ego graph for the current focus at the current
// radius. It is called from the render path and from every graph key handler,
// so navigation always walks the geometry that is actually on screen.
func (m *Model) buildGraph() *egoLayout {
	avail := maxInt(1, m.w-2)
	cols := clamp(avail/graphNodeMinW, 1, graphHardCols)
	hidden := func(id string) bool {
		if m.q.Empty() || m.pinned[id] {
			return false
		}
		t := m.b.Task(id)
		return t != nil && !m.q.Match(t, m.g)
	}
	l := buildEgo(m.g, m.graphFocus, m.graphRadius, cols, hidden)
	l.place(avail)
	return l
}

// graphBands measures the frame: the height of every channel, and how many
// title lines a node box can afford.
func (m *Model) graphBands(l *egoLayout) (channels []int, routes [][]routedEdge, titleLines int) {
	for r := 0; r+1 < len(l.Layers); r++ {
		rt, h := routeChannel(l, l.rowEdges(r))
		routes = append(routes, rt)
		channels = append(channels, h)
	}
	sum := 0
	for _, h := range channels {
		sum += h
	}
	rows := maxInt(1, len(l.Layers))
	per := (m.graphCanvasH() - sum) / rows
	// A node box is border(2) + the id line + the meta line, so `per-4` is the
	// title budget. It is clamped, never trusted: on a tiny terminal the graph
	// scrolls instead of rendering boxes with no title at all.
	titleLines = clamp(per-4, graphMinNodeLines, graphMaxNodeLines)
	return channels, routes, titleLines
}

// renderGraph draws the whole graph view.
func (m *Model) renderGraph() string {
	l := m.buildGraph()
	m.graphLay = l
	m.clampGraphSel(l)

	channels, routes, titleLines := m.graphBands(l)

	var bands []string
	for r, row := range l.Layers {
		bands = append(bands, m.graphRowBand(l, row, titleLines))
		if r < len(routes) {
			c := drawChannel(l.W, routes[r], channels[r])
			for _, line := range c.rows() {
				bands = append(bands, m.th.edge.Render(line))
			}
		}
	}

	// A focus with no structure at all: say so in words. A lone box floating in
	// an empty screen reads as a bug, not as an answer.
	if l.Empty() {
		msg := m.th.dim.Render("— nothing depends on this, and it waits on nothing —")
		bands = append([]string{m.th.dim.Render("— no blockers —"), ""},
			append(bands, "", msg)...)
	}

	canvasH := m.graphCanvasH()
	m.graphScroll = clamp(m.graphScroll, 0, maxInt(0, len(bands)-canvasH))
	m.graphScroll = m.scrollGraphToSel(l, channels, titleLines, len(bands), canvasH)

	shown := bands
	if len(shown) > canvasH {
		shown = shown[m.graphScroll:minInt(len(bands), m.graphScroll+canvasH)]
	}
	canvas := make([]string, 0, canvasH)
	for _, s := range shown {
		canvas = append(canvas, " "+pad(s, maxInt(1, m.w-2)))
	}
	for len(canvas) < canvasH {
		canvas = append(canvas, strings.Repeat(" ", maxInt(1, m.w)))
	}

	parts := []string{
		pad(m.graphTitleBar(l), m.w),
		pad(m.graphHeader(l, len(bands) > canvasH), m.w),
		strings.Join(canvas, "\n"),
	}
	if sh := m.graphStripHeight(); sh > 0 {
		parts = append(parts, m.graphStrip(l, sh))
	}
	parts = append(parts,
		pad(m.statusLine(), m.w),
		pad(m.help.ShortHelpView(m.keys.graphHelp()), m.w))

	return m.fitFrame(strings.Join(parts, "\n"))
}

func (m *Model) graphTitleBar(l *egoLayout) string {
	th := m.th
	left := th.title.Render("furrow board") + th.crumb.Render("  ·  ") +
		th.tabOff.Render("Board") + th.dim.Render(" │ ") +
		th.tabOff.Render("Table") + th.dim.Render(" │ ") +
		th.tabOn.Render("Graph")
	right := th.crumb.Render(fmt.Sprintf("%d nodes · %d edges  ·  ",
		len(l.Real()), len(l.Edges))) + th.accent.Render("⟨GRAPH⟩")
	return joinEnds(left, right, m.w)
}

// graphHeader is the one line that says what you are looking at, which way is
// which, and how deep the walk went.
func (m *Model) graphHeader(l *egoLayout, clipped bool) string {
	th := m.th
	focus := m.b.Task(l.Focus)
	name := l.Focus
	if focus != nil {
		name = l.Focus + " " + focus.Title
	}
	left := th.peekHdr.Render("↑ blockers") + th.dim.Render(" / ") +
		th.peekHdr.Render("↓ unblocks") + th.dim.Render("  ·  rooted on ") +
		th.chipAlt.Render(ansi.Truncate(name, maxInt(10, m.w/2), "…"))

	bits := []string{fmt.Sprintf("radius %s", radiusLabel(l.Radius)),
		fmt.Sprintf("%d up / %d down", l.UpCount, l.DownCount)}
	if n := len(l.Skipped); n > 0 {
		bits = append(bits, th.warn.Render(fmt.Sprintf("%d cyclic edge(s) not drawn", n)))
	}
	if len(l.Overflow) > 0 {
		n := 0
		for _, v := range l.Overflow {
			n += v
		}
		bits = append(bits, th.warn.Render(fmt.Sprintf("+%d over the row cap", n)))
	}
	if clipped {
		bits = append(bits, th.dim.Render("^u/^d scroll"))
	}
	return joinEnds(left, th.dim.Render(strings.Join(bits, " · ")), m.w)
}

// graphRowBand composes one row of node boxes into a rectangular band.
//
// Boxes are joined by SLICING each box's rendered lines and concatenating them
// with plain-space gaps, rather than by lipgloss.JoinHorizontal: every box is
// already exactly n.W display cells wide and exactly the same height, so the
// concatenation is width-exact and the channel anchors below it line up to the
// cell.
func (m *Model) graphRowBand(l *egoLayout, row []*egoNode, titleLines int) string {
	h := titleLines + 4
	lines := make([]string, h)

	type piece struct {
		x    int
		rows []string
	}
	var pieces []piece
	for _, n := range row {
		if n.Kind == egoDummy {
			col := make([]string, h)
			for i := range col {
				col[i] = m.th.edge.Render(pad(" │ ", n.W))
			}
			pieces = append(pieces, piece{x: n.X, rows: col})
			continue
		}
		box := m.renderGraphNode(n, titleLines)
		pieces = append(pieces, piece{x: n.X, rows: strings.Split(box, "\n")})
	}

	for i := 0; i < h; i++ {
		var b strings.Builder
		cur := 0
		for _, p := range pieces {
			if p.x > cur {
				b.WriteString(strings.Repeat(" ", p.x-cur))
				cur = p.x
			}
			var seg string
			if i < len(p.rows) {
				seg = p.rows[i]
			}
			b.WriteString(seg)
			cur += lg.Width(seg)
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}

// renderGraphNode draws one node box: id line, title, metadata. This is the
// half of a graph view that is actually hard — a layered picture of 12 boxes is
// arithmetic, but a box that says something useful in 50-95 cells is design.
//
// At 240+ columns a node gets 52-95 cells of inner width, so the median 82-cell
// title lands in one or two lines instead of the 22-37% stub a 140-column
// terminal could show. That is the entire argument for building this wide-first.
func (m *Model) renderGraphNode(n *egoNode, titleLines int) string {
	th := m.th
	inner := maxInt(4, n.W-4)
	t := m.b.Task(n.ID)

	var lines []string

	if t == nil {
		// A dep pointing at an id that is not on the board. Say exactly that.
		lines = append(lines, joinEnds(th.danger.Render(glyphUnknown+" "+n.ID), "", inner))
		for i := 0; i < titleLines; i++ {
			body := ""
			if i == 0 {
				body = th.dim.Render("not on this board")
			}
			lines = append(lines, pad(body, inner))
		}
		lines = append(lines, pad(th.dim.Render("unresolved dependency"), inner))
		return th.graphNodeUnknown.Width(n.W).Render(strings.Join(lines, "\n"))
	}

	glyph, styleFor := cardMarker(t, m.g)
	head := styleFor(th).Render(glyph) + " " + th.chipAlt.Render(t.ID)
	if n.Focus {
		head += " " + th.accent.Render("◉ focus")
	}
	if n.Both {
		head += " " + th.warn.Render("↕ both directions")
	}
	right := th.laneDot(*m.b.Lane(t.Status)).Render(glyphLaneDot) + " " + th.muted.Render(t.Status)
	if r := t.ShortRepo(); r != "" {
		right += th.dim.Render(" · ") + th.chipAlt.Render(r)
	}
	lines = append(lines, joinEnds(head, right, inner))

	body := wrapLines(t.Title, inner)
	if len(body) > titleLines {
		body = body[:titleLines]
		body[titleLines-1] = ansi.Truncate(body[titleLines-1], inner-1, "…")
	}
	titleStyle := th.base
	if m.g.IsDone(t.ID) {
		titleStyle = th.dim
	}
	for i := 0; i < titleLines; i++ {
		s := ""
		if i < len(body) {
			s = titleStyle.Render(body[i])
		}
		lines = append(lines, pad(s, inner))
	}

	var bits []string
	if t.Value > 0 || t.Effort > 0 {
		bits = append(bits, th.muted.Render(fmt.Sprintf("v%d e%d", t.Value, t.Effort)))
	}
	if nb := len(m.g.BlockedBy(t.ID)); nb > 0 {
		bits = append(bits, th.danger.Render(fmt.Sprintf("%s%d blocked", glyphBlocked, nb)))
	}
	if m.g.Actionable(t.ID) {
		bits = append(bits, th.ok.Render(glyphActionable+" actionable"))
	}
	if m.g.IsContainer(t.ID) {
		d, tot := m.g.Progress(t.ID, false)
		bits = append(bits, th.accent.Render(fmt.Sprintf("%s %d/%d", glyphEpic, d, tot)))
	}
	if d, tot := t.CheckProgress(); tot > 0 {
		bits = append(bits, th.muted.Render(fmt.Sprintf("[%d/%d]", d, tot)))
	}
	for _, lb := range t.Labels {
		bits = append(bits, th.chipFor(lb).Render("●")+th.muted.Render(" "+lb))
	}
	meta := strings.Join(bits, th.dim.Render(" · "))
	tag := ""
	if n.Hidden {
		// The graph deliberately shows what the board filter hides — an edge
		// that disappears because of a query is a lie about the board — so the
		// node is MARKED rather than dropped.
		tag = th.warn.Render("filtered out")
	}
	lines = append(lines, joinEnds(meta, tag, inner))

	return m.graphNodeStyle(n, t).Width(n.W).Render(strings.Join(lines, "\n"))
}

func (m *Model) graphNodeStyle(n *egoNode, t *Task) lg.Style {
	th := m.th
	sel := n.Key == m.graphSel
	switch {
	case n.Focus && sel:
		return th.graphNodeFocusSel
	case n.Focus:
		return th.graphNodeFocus
	case sel:
		return th.graphNodeSel
	case t != nil && m.g.IsDone(t.ID):
		return th.graphNodeDone
	}
	return th.graphNode
}

// graphStrip is the selected node's full record, uncut.
//
// The layout research flagged LABELLING as the hard half of a graph view, and
// it is right: a box can only ever show a truncated title, so the graph would
// be a picture you cannot read the captions of. The strip is the answer — the
// selection's whole title and metadata, wrapped, never elided, always on
// screen, so the boxes are free to be a MAP rather than a document.
func (m *Model) graphStrip(l *egoLayout, h int) string {
	th := m.th
	inner := maxInt(10, m.w-4)
	n := l.Node(m.graphSel)
	if n == nil {
		n = l.FocusNode()
	}
	var t *Task
	if n != nil {
		t = m.b.Task(n.ID)
	}
	if t == nil {
		return th.peek.Width(m.w).Height(h).Render(th.dim.Render("no node selected"))
	}

	// Two columns when there is room: identity and prose on the left, the
	// resolved dep lists on the right. At 240+ this is free real estate.
	leftW := inner
	rightW := 0
	if inner >= 120 {
		rightW = inner / 2
		leftW = inner - rightW - 3
	}

	var left []string
	head := th.peekHdr.Render(t.ID) + " " + th.chipAlt.Render("["+t.Status+"]")
	if m.g.Actionable(t.ID) {
		head += " " + th.ok.Render(glyphActionable+" actionable")
	}
	if nb := len(m.g.BlockedBy(t.ID)); nb > 0 {
		head += " " + th.danger.Render(fmt.Sprintf("%s blocked by %d", glyphBlocked, nb))
	}
	if n.Hidden {
		head += " " + th.warn.Render("· hidden by the current filter")
	}
	left = append(left, head)
	// The FULL title, wrapped, never truncated — that is the strip's whole job.
	for _, line := range wrapLines(t.Title, leftW) {
		left = append(left, th.base.Render(line))
	}
	meta := []string{"type " + t.EffectiveType()}
	if t.Value > 0 || t.Effort > 0 {
		meta = append(meta, fmt.Sprintf("value %d", t.Value), fmt.Sprintf("effort %d", t.Effort))
	}
	if len(t.Repos) > 0 {
		meta = append(meta, "repos "+strings.Join(t.Repos, ","))
	} else {
		meta = append(meta, "draft (no repo)")
	}
	if len(t.Labels) > 0 {
		meta = append(meta, "labels "+strings.Join(t.Labels, ","))
	}
	if t.Parent != "" {
		meta = append(meta, "parent "+t.Parent)
	}
	if d, tot := t.CheckProgress(); tot > 0 {
		meta = append(meta, fmt.Sprintf("checklist %d/%d", d, tot))
	}
	meta = append(meta, "updated "+ago(t.Updated))
	for _, line := range strings.Split(wrapJoin(meta, " · ", leftW), "\n") {
		left = append(left, th.muted.Render(line))
	}

	var right []string
	if rightW > 0 {
		up, down := m.g.BlockedBy(t.ID), m.g.OpenBlocks(t.ID)
		right = append(right, th.muted.Render(fmt.Sprintf("blocked by %d open · blocks %d open",
			len(up), len(down))))
		for _, id := range t.Deps {
			right = append(right, "↑ "+m.depLine(id, rightW-2))
		}
		for _, id := range m.g.Blocks(t.ID) {
			right = append(right, "↓ "+m.depLine(id, rightW-2))
		}
		if len(t.Deps) == 0 && len(m.g.Blocks(t.ID)) == 0 {
			right = append(right, th.dim.Render("— no dependency edges —"))
		}
	}

	body := h - 2
	rows := make([]string, 0, body)
	for i := 0; i < body; i++ {
		lseg, rseg := "", ""
		if i < len(left) {
			lseg = left[i]
		}
		if i < len(right) {
			rseg = right[i]
		}
		if rightW == 0 {
			rows = append(rows, pad(lseg, inner))
			continue
		}
		rows = append(rows, pad(lseg, leftW)+"   "+pad(rseg, rightW))
	}
	return th.peek.Width(m.w).Height(h).Render(strings.Join(rows, "\n"))
}

// ---- navigation -------------------------------------------------------------

// clampGraphSel keeps the selection on a node that still exists — a radius
// change or a re-root can remove the node the cursor was on.
func (m *Model) clampGraphSel(l *egoLayout) {
	if n := l.Node(m.graphSel); n != nil && n.Kind == egoReal {
		return
	}
	m.graphSel = l.Focus
}

// graphMove walks the selection. dy crosses rows keeping the nearest slot
// (which is what makes the grid feel like a grid); dx walks within a row.
// Dummies are routing artefacts and are skipped.
func (m *Model) graphMove(dx, dy int) {
	l := m.graphLay
	if l == nil {
		return
	}
	cur := l.Node(m.graphSel)
	if cur == nil {
		return
	}
	if dx != 0 {
		row := l.Layers[cur.Row]
		for i := cur.Slot + dx; i >= 0 && i < len(row); i += dx {
			if row[i].Kind == egoReal {
				m.graphSel = row[i].Key
				return
			}
		}
		return
	}
	if dy == 0 {
		return
	}
	want := cur.Anchor()
	for r := cur.Row + dy; r >= 0 && r < len(l.Layers); r += dy {
		best, bestD := "", 1<<30
		for _, n := range l.Layers[r] {
			if n.Kind != egoReal {
				continue
			}
			if d := abs(n.Anchor() - want); d < bestD {
				best, bestD = n.Key, d
			}
		}
		if best != "" {
			m.graphSel = best
			return
		}
	}
}

// scrollGraphToSel keeps the selected node's band on screen. Band offsets are
// recomputed from the same channel heights the renderer used, so the scroll can
// never disagree with what is drawn.
func (m *Model) scrollGraphToSel(l *egoLayout, channels []int, titleLines, total, canvasH int) int {
	if total <= canvasH {
		return 0
	}
	n := l.Node(m.graphSel)
	if n == nil {
		return clamp(m.graphScroll, 0, total-canvasH)
	}
	nodeH := titleLines + 4
	top := 0
	for r := 0; r < n.Row; r++ {
		top += nodeH
		if r < len(channels) {
			top += channels[r]
		}
	}
	bot := top + nodeH
	s := m.graphScroll
	if top < s {
		s = top
	}
	if bot > s+canvasH {
		s = bot - canvasH
	}
	return clamp(s, 0, total-canvasH)
}

// ---- keys -------------------------------------------------------------------

// openGraph roots the graph on the current selection and switches to it.
func (m *Model) openGraph() {
	t := m.curTask()
	if t == nil {
		m.note("nothing selected — the graph is rooted on a task")
		return
	}
	m.cancelDrag()
	m.graphFocus, m.graphSel = t.ID, t.ID
	m.graphScroll = 0
	m.graphStack = nil
	m.view = viewGraph
	m.note("graph rooted on %s — ⏎ re-roots on the selected node · z cycles radius · esc returns", t.ID)
}

// rerootGraph is the thing a static picture cannot do: walk the graph. The
// previous root is pushed so `<` retraces the walk.
func (m *Model) rerootGraph() {
	l := m.graphLay
	if l == nil {
		return
	}
	n := l.Node(m.graphSel)
	if n == nil || n.Kind != egoReal {
		return
	}
	if n.Key == m.graphFocus {
		m.note("%s is already the root — move the selection first", n.Key)
		return
	}
	if m.b.Task(n.ID) == nil {
		m.fail("%s is not on this board, so it has no structure to root on", n.ID)
		return
	}
	m.graphStack = append(m.graphStack, m.graphFocus)
	m.graphFocus, m.graphSel = n.Key, n.Key
	m.graphScroll = 0
	m.note("→ re-rooted on %s  ·  < retraces", n.Key)
}

func (m *Model) graphBack() {
	if len(m.graphStack) == 0 {
		m.note("graph walk is at its start")
		return
	}
	id := m.graphStack[len(m.graphStack)-1]
	m.graphStack = m.graphStack[:len(m.graphStack)-1]
	m.graphFocus, m.graphSel = id, id
	m.graphScroll = 0
	m.note("← back to %s (%d left)", id, len(m.graphStack))
}

func (m *Model) cycleGraphRadius() {
	for i, r := range graphRadii {
		if r == m.graphRadius {
			m.graphRadius = graphRadii[(i+1)%len(graphRadii)]
			m.note("hop radius %s", radiusLabel(m.graphRadius))
			return
		}
	}
	m.graphRadius = graphRadii[0]
}

// closeGraph returns to the board, landing the board cursor on whatever node
// the graph walk ended on — the walk was navigation, so it should have moved
// you.
func (m *Model) closeGraph() {
	m.view = viewBoard
	if n := m.graphLay.Node(m.graphSel); n != nil && n.Kind == egoReal {
		m.selectID(n.ID, true)
	}
	m.note("board view — the cursor followed the graph walk")
}
