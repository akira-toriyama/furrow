package main

// Board geometry. The rule here is that the layout is computed from the SAME
// cardLines() call the renderer uses, so a hit-test can never disagree with
// what is on screen. Cards are variable height (a wrapped CJK title is 1-3
// lines), which rules out the usual `y / cardH` arithmetic — every lookup goes
// through the measured boxes below.

const (
	// Column width is NEGOTIATED, not fixed. A hard 28 was two bugs at once: at
	// 200 columns the right third of the screen was dead space (GitHub's columns
	// share the available width), and at 20 columns the frame rendered 28 cells
	// wide and overflowed the terminal it was handed.
	//
	// colMaxW was 34, which was the same bug one size up: this board is read on
	// a 3840-point ultrawide, so at 240-400 columns a 34-cell cap left a third
	// of the screen empty while every Japanese title still wrapped to three
	// lines and truncated. Titles measure a median of 82 display cells and a
	// p90 of 133, so a card only becomes genuinely READABLE somewhere north of
	// 45 — which is exactly what six lanes across 320+ columns can afford.
	colMinW   = 24 // never narrower, unless the terminal itself is
	colOuterW = 28 // the width a column wants when it is the only one
	colMaxW   = 64 // a card may be a paragraph now; there is room for one
	colGap    = 2

	// cardGapY is the single strongest "these are discrete draggable objects"
	// cue on the board. Without it `╰───╯` sits directly on the next `╭───╮` and
	// the eye fuses the stack into one ruled table. The gutter is dead space for
	// hit-testing, which is exactly right: a pointer there means "between these
	// two", which is what idxAtY already computes.
	cardGapY = 1

	rowTitle  = 0 // "furrow board" + Board|Table tabs + counts + mode badge
	rowFilter = 1 // the filter bar
	rowColHdr = 2 // lane dot + name + count + WIP badge
	rowColSum = 3 // value/effort sums
	rowRule   = 4
	boardTop  = 5 // first card row
	footerH   = 2 // status line + help line
)

// cardBox is one card's measured place on screen.
type cardBox struct {
	ID   string
	Idx  int // index within the column's FILTERED task list
	X, Y int
	W, H int
}

// laneCol is one visible column.
type laneCol struct {
	Lane   Lane
	X, W   int
	Top    int // first card row (inclusive)
	Bot    int // last usable row (exclusive)
	Tasks  []*Task
	Cards  []cardBox // only the cards that actually fit
	Scroll int       // index of the first rendered card
	Hidden int       // cards below the fold
}

// layout is one frame's geometry.
type layout struct {
	W, H    int
	ColW    int // the negotiated column width for this frame
	Cols    []laneCol
	byName  map[string]*laneCol
	LaneOff int // index of the leftmost visible lane
	Visible int // how many columns fit
}

// boardCols negotiates how many columns fit in w and how wide each one is.
//
// The count is derived from colMinW, not from the PREFERRED width: the question
// a board has to answer first is "how many lanes can I show at all", and
// answering it with the preferred 28 hid lanes that would have fitted
// comfortably at 26. On this board that was the difference between four visible
// lanes and six — and a lane you cannot see is a lane you cannot drop into, so
// the old reading cost function, not just polish. Width is then shared out of
// whatever is actually there, capped at colMaxW.
func boardCols(w, lanes int) (n, cw int) {
	if lanes < 1 {
		lanes = 1
	}
	if w < colMinW {
		return 1, maxInt(1, w)
	}
	n = (w + colGap) / (colMinW + colGap)
	if n < 1 {
		n = 1
	}
	if n > lanes {
		n = lanes
	}
	cw = (w - (n-1)*colGap) / n
	cw = clamp(cw, colMinW, colMaxW)
	if cw > w {
		cw = w
	}
	return n, cw
}

// visibleCols is how many columns fit in w over a board of `lanes` lanes.
func visibleCols(w, lanes int) int {
	n, _ := boardCols(w, lanes)
	return n
}

// Col looks a visible column up by lane name.
func (l *layout) Col(name string) *laneCol {
	if l == nil {
		return nil
	}
	return l.byName[name]
}

// laneAtX maps a screen column to a lane name.
func (l *layout) laneAtX(x int) (string, bool) {
	for i := range l.Cols {
		c := &l.Cols[i]
		if x >= c.X && x < c.X+c.W {
			return c.Lane.Name, true
		}
	}
	return "", false
}

// cardAt finds the card under a point.
func (l *layout) cardAt(x, y int) (lane string, idx int, ok bool) {
	name, ok := l.laneAtX(x)
	if !ok {
		return "", 0, false
	}
	c := l.byName[name]
	for _, b := range c.Cards {
		if y >= b.Y && y < b.Y+b.H {
			return name, b.Idx, true
		}
	}
	return name, 0, false
}

// idxAtY maps a screen row inside a column to an INSERTION index measured
// against the column as displayed — the card above whose midpoint you are goes
// before you. The result still counts the moving card when the drag started in
// this lane, which is exactly what AdjustDropIndex expects.
//
// Below the last RENDERED card the answer is the end of the LANE, not the end
// of the visible run. When nothing is below the fold the two are identical;
// when cards are hidden, the older "lastVisible+1" made the bottom of a long
// lane unreachable by any gesture — a 13-card column whose deepest droppable
// slot was 2.
func (l *layout) idxAtY(lane string, y int) int {
	c := l.byName[lane]
	if c == nil {
		return 0
	}
	if len(c.Cards) == 0 {
		return c.Scroll
	}
	for _, b := range c.Cards {
		if y < b.Y+b.H/2 {
			return b.Idx
		}
	}
	return len(c.Tasks)
}

// dropY is the screen row of the boundary before insertion index idx, used to
// draw the drop indicator. With a gutter between cards the boundary is the
// gutter row itself, so the marker never sits on top of a card border.
func (l *layout) dropY(lane string, idx int) (int, bool) {
	c := l.byName[lane]
	if c == nil {
		return 0, false
	}
	if len(c.Cards) == 0 {
		return c.Top, true
	}
	for _, b := range c.Cards {
		if b.Idx == idx {
			return maxInt(c.Top, b.Y-cardGapY), true
		}
	}
	last := c.Cards[len(c.Cards)-1]
	if idx > last.Idx {
		return clamp(last.Y+last.H, c.Top, maxInt(c.Top, c.Bot-1)), true
	}
	return c.Top, true
}

// measurer memoises card heights ACROSS frames.
//
// cardHeight RENDERS the card to measure it (predicting is one lipgloss wrapping
// rule away from being wrong, and a wrong height is handed straight to the mouse
// hit-test). That is affordable once per card — but scrollToShow re-lays a
// column once per candidate offset, so the honest measurement turned a 13-card
// column into O(n²) full card renders per frame, on the layout path that runs on
// every Update AND every View. The cache keeps the honesty and drops the cost.
//
// A per-FRAME cache still left maxScrollFor rendering every card in every lane
// on every frame, because it walks to the last card to find the largest useful
// offset. At the real board's 658 tasks that was ~36ms/frame — and a drag
// repaints on every mouse motion, so the board got heavy exactly when it had to
// feel light. The cache therefore outlives the frame, and is dropped whenever
// the thing a card is drawn FROM changes: the graph (rebuilt by recompute after
// any mutation) or the theme. Between mutations — i.e. all of navigation and
// the whole of a drag — every height is a hit.
type measurer struct {
	g     *Graph
	th    *theme
	cache map[measureKey]int
}

type measureKey struct {
	id string
	w  int
}

func newMeasurer(g *Graph, th *theme) *measurer {
	return &measurer{g: g, th: th, cache: map[measureKey]int{}}
}

// rebind points the measurer at a new graph/theme and drops every cached
// height. Call it whenever a card's CONTENT could have changed; keeping a stale
// height would desynchronise the renderer from the hit-test, which is the one
// failure this type exists to prevent.
func (m *measurer) rebind(g *Graph, th *theme) {
	m.g, m.th = g, th
	clear(m.cache)
}

func (m *measurer) height(t *Task, w int) int {
	k := measureKey{id: t.ID, w: w}
	if h, ok := m.cache[k]; ok {
		return h
	}
	h := cardHeight(t, m.g, m.th, w)
	m.cache[k] = h
	return h
}

// layCards places as many cards as fit starting at index `scroll`. Every piece
// of geometry in the app — the renderer, the hit-test, the scroll clamp — goes
// through this one function, so the gutter can never be counted twice in one
// place and not at all in another.
func layCards(tasks []*Task, scroll, x, top, bot, colW int, ms *measurer) []cardBox {
	var out []cardBox
	y := top
	for idx := maxInt(0, scroll); idx < len(tasks); idx++ {
		h := ms.height(tasks[idx], colW)
		if y+h > bot {
			break
		}
		out = append(out, cardBox{ID: tasks[idx].ID, Idx: idx, X: x, Y: y, W: colW, H: h})
		y += h + cardGapY
	}
	return out
}

// buildLayout measures one frame: which lanes are visible, which cards fit in
// each, and where every card sits.
func buildLayout(w, h int, lanes []Lane, cols map[string][]*Task, laneOff int,
	scroll map[string]int, ms *measurer) *layout {

	w, h = maxInt(w, 1), maxInt(h, 1)
	vis, colW := boardCols(w, len(lanes))
	if laneOff > len(lanes)-vis {
		laneOff = len(lanes) - vis
	}
	if laneOff < 0 {
		laneOff = 0
	}
	// Deliberately NOT floored at boardTop+1: on a terminal too short for any
	// card the board simply has no usable rows. Forcing one row made the column
	// band overlap the footer, which is how a 1-row terminal rendered 6 rows.
	bot := h - footerH

	l := &layout{W: w, H: h, ColW: colW, LaneOff: laneOff, Visible: vis,
		byName: map[string]*laneCol{}}
	for i := 0; i < vis && laneOff+i < len(lanes); i++ {
		lane := lanes[laneOff+i]
		c := laneCol{
			Lane:  lane,
			X:     i * (colW + colGap),
			W:     colW,
			Top:   boardTop,
			Bot:   bot,
			Tasks: cols[lane.Name],
		}
		// The scroll offset is clamped to what is actually scrollable, not to
		// len(tasks)-1. A filter that shrinks a column used to leave a stale
		// offset behind, rendering a 2-card column scrolled past both of them.
		c.Scroll = clamp(scroll[lane.Name], 0,
			maxScrollFor(c.Tasks, c.Top, c.Bot, colW, ms))
		c.Cards = layCards(c.Tasks, c.Scroll, c.X, c.Top, c.Bot, colW, ms)
		c.Hidden = len(c.Tasks) - c.Scroll - len(c.Cards)
		l.Cols = append(l.Cols, c)
	}
	for i := range l.Cols {
		l.byName[l.Cols[i].Lane.Name] = &l.Cols[i]
	}
	return l
}

// maxScrollFor is the largest offset that still leaves something to look at:
// the smallest scroll that puts the LAST card on screen. Past it there is
// nothing below the fold, so scrolling further only hides cards.
func maxScrollFor(tasks []*Task, top, bot, colW int, ms *measurer) int {
	if len(tasks) == 0 {
		return 0
	}
	return scrollToShow(tasks, len(tasks)-1, 0, top, bot, colW, ms)
}

// scrollToShow returns the smallest scroll offset that puts card idx on screen,
// growing the offset one card at a time because heights vary.
func scrollToShow(tasks []*Task, idx, scroll, top, bot, colW int, ms *measurer) int {
	if idx < 0 || len(tasks) == 0 {
		return 0
	}
	if idx > len(tasks)-1 {
		idx = len(tasks) - 1
	}
	if scroll > idx {
		scroll = idx
	}
	if scroll < 0 {
		scroll = 0
	}
	for scroll < idx {
		shows := false
		for _, b := range layCards(tasks, scroll, 0, top, bot, colW, ms) {
			if b.Idx == idx {
				shows = true
				break
			}
		}
		if shows {
			break
		}
		scroll++
	}
	return scroll
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
