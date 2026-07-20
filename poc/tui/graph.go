package main

import (
	"fmt"
	"sort"
)

// The dependency GRAPH view's layout engine — pure, deterministic, and with no
// knowledge of lipgloss, the theme or the terminal. graphview.go draws what
// this file decides.
//
// The shape is an EGO GRAPH around one focus task: "what must finish before
// this" above it, "what closing this unblocks" below it. Direction is carried
// by POSITION (upstream is up) and, redundantly, by arrowheads — a reader must
// never have to remember which way the arrows point.
//
// The algorithm is Sugiyama PHASE 1 ONLY (longest-path layering) plus a single
// barycenter sweep. That is a deliberate stopping point, not laziness: measured
// over the real 658-task board, naive id-order placement produces **2** edge
// crossings across ALL 131 ego-graphs, so a real crossing-reduction pass would
// be several hundred lines buying two glyphs. The barycenter sweep is ~20 lines
// and is kept because it is nearly free and makes long fan-outs read straight.
//
// Everything here is bounded and cycle-safe. The real board has no cycles, but
// a git merge can produce one and a graph view that hangs is worse than one
// that draws a cycle awkwardly.

const (
	// graphAllRadius is the "all" setting of the hop-radius cycle. The real
	// board's longest chain is 5 edges, so 8 reaches everything while still
	// bounding every walk in this file.
	graphAllRadius = 8

	// graphHardCols caps how many nodes one layer may DRAW. The measured
	// widest layer over the whole board is 4; the cap exists so a pathological
	// fan-out degrades into "+N more" instead of into an unreadable frame.
	graphHardCols = 6

	graphNodeMinW = 28
	graphNodeMaxW = 104
	graphNodeGap  = 3
	graphDummyW   = 3
)

// egoKind separates real tasks from the routing dummies that carry an edge
// spanning more than one layer through the layers in between.
type egoKind int

const (
	egoReal egoKind = iota
	egoDummy
)

// egoNode is one placed node. Dummies get a synthetic Key and no Task.
type egoNode struct {
	Key   string // node identity in the layout (a task id, or a dummy key)
	ID    string // the task id; "" for a dummy
	Kind  egoKind
	Layer int // SIGNED: negative = upstream, 0 = focus, positive = downstream
	Row   int // index into egoLayout.Layers (0 = topmost)
	Slot  int // position within the row, left to right

	Focus   bool // the task the graph is rooted on
	Both    bool // reachable both upstream AND downstream (only possible in a cycle)
	Hidden  bool // the current board filter would hide this task
	Unknown bool // a dep pointing at an id that is not on the board

	// Filled by place(). X is the node's left column within the graph canvas.
	X, W int
}

// Anchor is the column an edge attaches to: the horizontal centre of the node.
func (n *egoNode) Anchor() int { return n.X + n.W/2 }

// egoEdge is one drawn edge, always between ADJACENT rows after dummy
// insertion, always pointing DOWN (From is the upper row).
type egoEdge struct{ From, To string }

// egoLayout is one frame's worth of graph structure.
type egoLayout struct {
	Focus  string
	Radius int

	Nodes  map[string]*egoNode
	Layers [][]*egoNode // index 0 = topmost row
	Edges  []egoEdge

	// Skipped are real dep edges the layered drawing cannot express: a cycle
	// folded two nodes onto the same row, or an edge pointing UP. They are
	// reported in the UI rather than silently dropped — a graph that quietly
	// omits an edge is worse than one that admits it.
	Skipped []egoEdge

	// Overflow counts nodes dropped from a row by graphHardCols, by row index.
	Overflow map[int]int

	UpCount, DownCount int
	W, H               int // canvas size, filled by place()
}

// FocusNode is the node the graph is rooted on.
func (l *egoLayout) FocusNode() *egoNode { return l.Nodes[l.Focus] }

// Real lists every real (non-dummy) node in draw order: row, then slot.
func (l *egoLayout) Real() []*egoNode {
	var out []*egoNode
	for _, row := range l.Layers {
		for _, n := range row {
			if n.Kind == egoReal {
				out = append(out, n)
			}
		}
	}
	return out
}

// Node looks a node up by key.
func (l *egoLayout) Node(key string) *egoNode { return l.Nodes[key] }

// Empty reports a focus with no dependency structure at all in either
// direction. The view says so in words rather than drawing a lone box in the
// middle of an empty screen and leaving the reader to wonder what broke.
func (l *egoLayout) Empty() bool { return l.UpCount == 0 && l.DownCount == 0 }

// WidestRow is how many nodes the busiest row holds — the "4" of the measured
// 4-wide × 5-tall bound.
func (l *egoLayout) WidestRow() int {
	w := 0
	for _, row := range l.Layers {
		if len(row) > w {
			w = len(row)
		}
	}
	return w
}

// longestDist is bounded longest-path layering from `from` over the edges
// `next` yields. Longest path — not shortest — is Sugiyama's phase 1: it is
// what guarantees every edge points strictly downward, which is what lets the
// channel router assume a direction.
//
// It is a Bellman-Ford-shaped relaxation rather than a DFS precisely so a cycle
// cannot recurse forever: distances only ever increase, they are capped at
// radius by the expansion guard, and the outer loop runs at most radius times.
// A cycle simply saturates. Iteration order is over SORTED keys, so the same
// board always produces the same layering.
func longestDist(next func(string) []string, from string, radius int) map[string]int {
	dist := map[string]int{from: 0}
	for round := 0; round < radius; round++ {
		keys := make([]string, 0, len(dist))
		for k := range dist {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		changed := false
		for _, u := range keys {
			du := dist[u]
			if du >= radius {
				continue
			}
			for _, v := range next(u) {
				if v == from {
					continue // the focus owns layer 0; never re-layer it
				}
				if old, ok := dist[v]; !ok || du+1 > old {
					dist[v] = du + 1
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	delete(dist, from)
	return dist
}

// buildEgo lays out the dependency structure around `focus`.
//
// hidden reports whether the current board filter would hide a task. The graph
// draws it ANYWAY — the graph is about dependency structure, not about the
// active query, and an edge that vanishes because of a filter is a lie about
// the board. Such nodes are MARKED, never dropped.
func buildEgo(g *Graph, focus string, radius, maxCols int, hidden func(string) bool) *egoLayout {
	if radius < 1 {
		radius = 1
	}
	if maxCols < 1 {
		maxCols = 1
	}
	if maxCols > graphHardCols {
		maxCols = graphHardCols
	}

	l := &egoLayout{
		Focus:    focus,
		Radius:   radius,
		Nodes:    map[string]*egoNode{},
		Overflow: map[int]int{},
	}

	depsOf := func(id string) []string {
		t := g.Board().Task(id)
		if t == nil {
			return nil
		}
		return t.Deps
	}

	up := longestDist(depsOf, focus, radius)
	down := longestDist(g.Blocks, focus, radius)
	l.UpCount, l.DownCount = len(up), len(down)

	// --- 1. layer assignment ------------------------------------------------
	add := func(id string, layer int, both bool) {
		n := &egoNode{Key: id, ID: id, Kind: egoReal, Layer: layer, Both: both}
		n.Focus = id == focus
		n.Unknown = !g.Known(id)
		if hidden != nil {
			n.Hidden = hidden(id)
		}
		l.Nodes[id] = n
	}
	add(focus, 0, false)

	ids := make([]string, 0, len(up)+len(down))
	for id := range up {
		ids = append(ids, id)
	}
	for id := range down {
		if _, ok := up[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		du, okUp := up[id]
		dd, okDown := down[id]
		switch {
		case okUp && okDown:
			// Only reachable in a cycle. Place it on the side that puts it
			// CLOSER to the focus, upstream winning ties, and flag it — the
			// node genuinely occupies both roles and the strip says so.
			if du <= dd {
				add(id, -du, true)
			} else {
				add(id, dd, true)
			}
		case okUp:
			add(id, -du, false)
		default:
			add(id, dd, false)
		}
	}

	// --- 2. group into rows, cap the width ---------------------------------
	byLayer := map[int][]*egoNode{}
	for _, n := range l.Nodes {
		byLayer[n.Layer] = append(byLayer[n.Layer], n)
	}
	layerVals := make([]int, 0, len(byLayer))
	for v := range byLayer {
		layerVals = append(layerVals, v)
	}
	sort.Ints(layerVals)

	rowOf := map[int]int{}
	for i, v := range layerVals {
		rowOf[v] = i
		row := byLayer[v]
		sort.Slice(row, func(a, b int) bool { return row[a].Key < row[b].Key })
		if len(row) > maxCols {
			// The focus must survive its own graph, whatever the id ordering.
			if v == 0 {
				for j, n := range row {
					if n.Focus && j >= maxCols {
						row[maxCols-1], row[j] = row[j], row[maxCols-1]
						break
					}
				}
			}
			for _, n := range row[maxCols:] {
				delete(l.Nodes, n.Key)
			}
			l.Overflow[i] = len(row) - maxCols
			row = row[:maxCols]
		}
		for _, n := range row {
			n.Row = i
		}
		l.Layers = append(l.Layers, row)
	}

	// --- 3. edges over the INDUCED subgraph ---------------------------------
	// Every dep edge whose BOTH ends survived, plus dummies for spans > 1.
	type raw struct{ from, to string } // from = upper (the dependency)
	var raws []raw
	seen := map[raw]bool{}
	for _, n := range l.Real() {
		for _, dep := range depsOf(n.ID) {
			if _, ok := l.Nodes[dep]; !ok {
				continue
			}
			r := raw{from: dep, to: n.ID}
			if seen[r] {
				continue
			}
			seen[r] = true
			raws = append(raws, r)
		}
	}
	sort.Slice(raws, func(a, b int) bool {
		if raws[a].from != raws[b].from {
			return raws[a].from < raws[b].from
		}
		return raws[a].to < raws[b].to
	})

	for _, r := range raws {
		fr, to := l.Nodes[r.from], l.Nodes[r.to]
		span := to.Row - fr.Row
		if span <= 0 {
			// A cycle folded the edge flat or backwards. Report it; do not try
			// to route it, and above all do not loop looking for a way.
			l.Skipped = append(l.Skipped, egoEdge{From: r.from, To: r.to})
			continue
		}
		if span == 1 {
			l.Edges = append(l.Edges, egoEdge{From: r.from, To: r.to})
			continue
		}
		// span > 1: chain dummies through the rows in between, so the channel
		// router only ever sees adjacent-row edges.
		prev := r.from
		for row := fr.Row + 1; row < to.Row; row++ {
			key := fmt.Sprintf("\x00dummy:%s>%s@%d", r.from, r.to, row)
			d := &egoNode{Key: key, Kind: egoDummy, Layer: layerVals[row], Row: row}
			l.Nodes[key] = d
			l.Layers[row] = append(l.Layers[row], d)
			l.Edges = append(l.Edges, egoEdge{From: prev, To: key})
			prev = key
		}
		l.Edges = append(l.Edges, egoEdge{From: prev, To: r.to})
	}

	l.orderRows()
	return l
}

// orderRows is the single barycenter sweep, run OUTWARD from the focus row so
// that every row is ordered against a row whose slots are already fixed.
//
// Sorting is stable and the tie-break is the node key, so a node with no
// neighbours in the reference row keeps its id-order position instead of
// floating.
func (l *egoLayout) orderRows() {
	focusRow := 0
	if n := l.FocusNode(); n != nil {
		focusRow = n.Row
	}

	adj := map[string][]string{}
	for _, e := range l.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	slotIn := func(row int) map[string]int {
		m := map[string]int{}
		for i, n := range l.Layers[row] {
			m[n.Key] = i
		}
		return m
	}

	sweep := func(row, ref int) {
		refSlots := slotIn(ref)
		bary := map[string]float64{}
		for _, n := range l.Layers[row] {
			sum, cnt := 0.0, 0
			for _, nb := range adj[n.Key] {
				if s, ok := refSlots[nb]; ok {
					sum += float64(s)
					cnt++
				}
			}
			if cnt == 0 {
				bary[n.Key] = -1 // no opinion: keep the incoming order
			} else {
				bary[n.Key] = sum / float64(cnt)
			}
		}
		sort.SliceStable(l.Layers[row], func(a, b int) bool {
			na, nb := l.Layers[row][a], l.Layers[row][b]
			ba, bb := bary[na.Key], bary[nb.Key]
			if ba < 0 || bb < 0 {
				return false // stable: leave opinionless nodes where they are
			}
			if ba != bb {
				return ba < bb
			}
			return na.Key < nb.Key
		})
	}

	for r := focusRow - 1; r >= 0; r-- {
		sweep(r, r+1)
	}
	for r := focusRow + 1; r < len(l.Layers); r++ {
		sweep(r, r-1)
	}
	for _, row := range l.Layers {
		for i, n := range row {
			n.Slot = i
		}
	}
}

// place assigns every node an X and a width inside a canvas `avail` wide.
//
// Real nodes all get the SAME width — the grid reads as a grid — sized so the
// busiest row fits. Dummies get graphDummyW, because a routing artefact should
// not cost 60 columns. Each row is then CENTRED, which is what puts the focus
// box in the middle of the frame with its fan-out spread symmetrically under it.
func (l *egoLayout) place(avail int) {
	if avail < 1 {
		avail = 1
	}
	cols := 1
	for _, row := range l.Layers {
		n := 0
		for _, nd := range row {
			if nd.Kind == egoReal {
				n++
			}
		}
		if n > cols {
			cols = n
		}
	}

	nw := (avail - (cols-1)*graphNodeGap) / cols
	nw = clamp(nw, graphNodeMinW, graphNodeMaxW)
	if nw > avail {
		nw = maxInt(1, avail)
	}

	for _, row := range l.Layers {
		total := 0
		for i, n := range row {
			n.W = nw
			if n.Kind == egoDummy {
				n.W = graphDummyW
			}
			if i > 0 {
				total += graphNodeGap
			}
			total += n.W
		}
		x := maxInt(0, (avail-total)/2)
		for _, n := range row {
			n.X = x
			x += n.W + graphNodeGap
		}
	}
	l.W = avail
}

// rowEdges returns the edges that live in the channel between row r and r+1.
func (l *egoLayout) rowEdges(r int) []egoEdge {
	var out []egoEdge
	for _, e := range l.Edges {
		if l.Nodes[e.From].Row == r && l.Nodes[e.To].Row == r+1 {
			out = append(out, e)
		}
	}
	return out
}

// ---- edge canvas ------------------------------------------------------------

// Direction bits. A cell records which of its four sides a line leaves by, and
// the rune is then LOOKED UP from that mask. This is the whole junction-merge
// table — the thing worth porting out of a charting library — expressed as the
// 16-entry array it always was: two lines meeting in one cell can only produce
// the glyph their combined mask names, so `─` crossing `│` cannot come out as
// anything but `┼`, and a tee cannot come out as a corner.
const (
	dirN uint8 = 1 << iota
	dirE
	dirS
	dirW
)

var junction = [16]rune{
	0:                     ' ',
	dirN:                  '╵',
	dirE:                  '─',
	dirN | dirE:           '╰',
	dirS:                  '╷',
	dirN | dirS:           '│',
	dirE | dirS:           '╭',
	dirN | dirE | dirS:    '├',
	dirW:                  '─',
	dirN | dirW:           '╯',
	dirE | dirW:           '─',
	dirN | dirE | dirW:    '┴',
	dirS | dirW:           '╮',
	dirN | dirS | dirW:    '┤',
	dirE | dirS | dirW:    '┬',
	dirN | dirE | dirS | dirW: '┼',
}

// edgeCanvas is a plain rune grid, and it is only ever used for the CHANNEL
// bands between node rows — never for anything containing text.
//
// That restriction is the whole reason it is safe. A rune-per-cell grid has no
// idea that a Japanese glyph is two cells wide, so the moment one is written
// into it every row to the right shears. (That is exactly the defect that
// disqualified ntcharts' canvas for this job.) Node boxes therefore go through
// lipgloss, which measures display width, and only box-drawing characters —
// every one of them single-width — ever reach this type.
type edgeCanvas struct {
	w, h  int
	mask  []uint8
	glyph []rune // an explicit glyph wins over the junction table
}

func newEdgeCanvas(w, h int) *edgeCanvas {
	w, h = maxInt(w, 1), maxInt(h, 0)
	return &edgeCanvas{w: w, h: h, mask: make([]uint8, w*h), glyph: make([]rune, w*h)}
}

func (c *edgeCanvas) in(x, y int) bool { return x >= 0 && x < c.w && y >= 0 && y < c.h }

func (c *edgeCanvas) or(x, y int, bits uint8) {
	if c.in(x, y) {
		c.mask[y*c.w+x] |= bits
	}
}

func (c *edgeCanvas) put(x, y int, r rune) {
	if c.in(x, y) {
		c.glyph[y*c.w+x] = r
	}
}

// vline draws a vertical run at x from y0 to y1 inclusive.
func (c *edgeCanvas) vline(x, y0, y1 int) {
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		var b uint8
		if y > y0 {
			b |= dirN
		}
		if y < y1 {
			b |= dirS
		}
		if y0 == y1 {
			b = dirN | dirS
		}
		c.or(x, y, b)
	}
}

// hline draws a horizontal run at y from x0 to x1 inclusive.
func (c *edgeCanvas) hline(y, x0, x1 int) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		var b uint8
		if x > x0 {
			b |= dirW
		}
		if x < x1 {
			b |= dirE
		}
		if x0 == x1 {
			b = dirE | dirW
		}
		c.or(x, y, b)
	}
}

// rows renders the canvas to strings. Every rune it can emit is single-width,
// which is asserted by TestEdgeCanvasGlyphsAreSingleWidth.
func (c *edgeCanvas) rows() []string {
	out := make([]string, c.h)
	buf := make([]rune, c.w)
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			if g := c.glyph[y*c.w+x]; g != 0 {
				buf[x] = g
				continue
			}
			buf[x] = junction[c.mask[y*c.w+x]&0xf]
		}
		out[y] = string(buf)
	}
	return out
}

// ---- channel routing --------------------------------------------------------

// routedEdge is one edge with its assigned bus row inside a channel.
type routedEdge struct {
	Edge     egoEdge
	X1, X2   int
	Bus      int  // -1 = a straight drop, no horizontal run at all
	ToDummy  bool // no arrowhead: the line continues through a pass-through
}

// routeChannel packs the edges between two rows into as few bus rows as it can,
// and returns the channel's height.
//
// The ordering rule is the one that makes an orthogonal channel readable:
// **shortest horizontal span nearest the source row.** Long edges then arc
// AROUND short ones instead of cutting across them. Combined with the measured
// crossing count (2 across the entire real board) this is all the
// crossing-avoidance the drawing needs.
//
// Rows are shared by interval packing: two edges whose horizontal runs do not
// overlap sit on the same row, so the common fan-out — several short hops —
// costs one row rather than one row each.
func routeChannel(l *egoLayout, edges []egoEdge) (routes []routedEdge, height int) {
	type span struct{ lo, hi int }
	var busy [][]span

	for _, e := range edges {
		from, to := l.Nodes[e.From], l.Nodes[e.To]
		routes = append(routes, routedEdge{
			Edge: e, X1: from.Anchor(), X2: to.Anchor(),
			Bus: -1, ToDummy: to.Kind == egoDummy,
		})
	}
	sort.SliceStable(routes, func(a, b int) bool {
		sa := abs(routes[a].X2 - routes[a].X1)
		sb := abs(routes[b].X2 - routes[b].X1)
		if sa != sb {
			return sa < sb
		}
		if routes[a].X1 != routes[b].X1 {
			return routes[a].X1 < routes[b].X1
		}
		return routes[a].Edge.From < routes[b].Edge.From
	})

	for i := range routes {
		r := &routes[i]
		if r.X1 == r.X2 {
			continue // straight drop: it needs no bus row
		}
		lo, hi := minInt(r.X1, r.X2), maxInt(r.X1, r.X2)
		placed := false
		for b := range busy {
			free := true
			for _, s := range busy[b] {
				// A one-cell margin: two horizontal runs that merely TOUCH
				// would fuse into a single line and read as one edge.
				if lo <= s.hi+1 && s.lo <= hi+1 {
					free = false
					break
				}
			}
			if free {
				busy[b] = append(busy[b], span{lo, hi})
				r.Bus = b
				placed = true
				break
			}
		}
		if !placed {
			busy = append(busy, []span{{lo, hi}})
			r.Bus = len(busy) - 1
		}
	}

	// Row 0 is a clean stub under the source boxes, rows 1..n are the buses,
	// and the last row carries the arrowheads directly above the target boxes.
	height = len(busy) + 2
	if len(edges) == 0 {
		height = 1
	}
	for i := range routes {
		if routes[i].Bus >= 0 {
			routes[i].Bus++
		}
	}
	return routes, height
}

// drawChannel paints one channel's edges onto a fresh canvas.
func drawChannel(w int, routes []routedEdge, height int) *edgeCanvas {
	c := newEdgeCanvas(w, height)
	arrowRow := height - 1
	for _, r := range routes {
		if r.Bus < 0 {
			c.vline(r.X1, 0, arrowRow-1)
		} else {
			c.vline(r.X1, 0, r.Bus)
			c.hline(r.Bus, r.X1, r.X2)
			c.vline(r.X2, r.Bus, arrowRow-1)
		}
		if r.ToDummy {
			// A pass-through is not a destination; the line simply continues.
			c.vline(r.X2, arrowRow-1, arrowRow)
		} else {
			c.put(r.X2, arrowRow, glyphArrowDown)
		}
	}
	return c
}
