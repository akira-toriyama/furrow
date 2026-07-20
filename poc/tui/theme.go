package main

import (
	"image/color"

	lg "charm.land/lipgloss/v2"
)

// Glyphs. Every one of these is asserted single-width by
// TestGlyphsAreSingleWidth — an East-Asian-ambiguous glyph would silently shear
// every card's right border on a CJK board, and this fixture IS a CJK board.
const (
	glyphActionable = "▸" // furrow next would hand you this one
	glyphBlocked    = "x" // unsatisfied deps
	glyphEpic       = "▤" // a container: a box, not work
	glyphStuck      = "!" // container with open work but nothing actionable
	glyphDone       = "v"
	glyphOpen       = "o"
	glyphUnknown    = "?" // a dep pointing at an id not on the board
	glyphWIPOver    = "!" // over the (unenforced) WIP limit
	glyphDrop       = "╌" // dashed, so it cannot be read as the header rule
	glyphDropL      = "▸" // the caret brackets make it an insertion POINT
	glyphDropR      = "◂"
	glyphLift       = "↕"
	glyphLaneDot    = "●"

	// glyphArrowDown terminates every graph edge. The graph draws upstream
	// ABOVE and downstream BELOW, so every edge in the picture points the same
	// way — downward, in the direction unblocking flows. Position and arrowhead
	// carry the same fact, which is deliberate redundancy: a reader must never
	// have to remember which way the arrows go.
	glyphArrowDown = '▼'
)

// theme is the whole palette. lipgloss v2 removed AdaptiveColor, so the light /
// dark choice is made once, from tea.BackgroundColorMsg, and baked in here.
type theme struct {
	dark bool

	base    lg.Style
	muted   lg.Style
	dim     lg.Style
	accent  lg.Style // move mode / drag
	ok      lg.Style // actionable
	warn    lg.Style
	danger  lg.Style
	invert  lg.Style
	title   lg.Style
	crumb   lg.Style
	rule    lg.Style
	chip    lg.Style
	chipAlt lg.Style

	tabOn     lg.Style
	tabOff    lg.Style
	colHdr    lg.Style
	colHdrOn  lg.Style
	colCount  lg.Style
	colBG     lg.Style
	colBGOn   lg.Style
	chipHues  []lg.Style
	laneHue   map[string]lg.Style
	card      lg.Style
	cardSel   lg.Style
	cardLift  lg.Style
	cardGhost lg.Style
	cardDone  lg.Style
	dropInd   lg.Style
	peek      lg.Style
	peekHdr   lg.Style
	status    lg.Style
	errText   lg.Style

	// The graph view. Node borders carry state REDUNDANTLY with colour —
	// double = the root, thick = the selection — because in -plain, on a
	// 16-colour TTY and for a colourblind reader a hue is not a signal.
	edge             lg.Style
	graphNode        lg.Style
	graphNodeSel     lg.Style
	graphNodeFocus   lg.Style
	graphNodeFocusSel lg.Style
	graphNodeDone    lg.Style
	graphNodeUnknown lg.Style
}

func newTheme(dark bool) *theme {
	c := func(d, l string) color.Color {
		if dark {
			return lg.Color(d)
		}
		return lg.Color(l)
	}
	var (
		fg     = c("#d7d9e0", "#1c1e26")
		muted  = c("#8b8fa3", "#63677a")
		dim    = c("#5b5f72", "#9aa0b4")
		line   = c("#3a3d4d", "#c9ccd8")
		accent = c("#f2a90f", "#b26f00")
		ok     = c("#57d38c", "#12783f")
		danger = c("#f2707a", "#c02c38")
		sel    = c("#7aa2f7", "#2f5fd0")
		panel  = c("#22242e", "#eef0f6")
	)
	t := &theme{dark: dark}
	t.base = lg.NewStyle().Foreground(fg)
	t.muted = lg.NewStyle().Foreground(muted)
	t.dim = lg.NewStyle().Foreground(dim)
	t.accent = lg.NewStyle().Foreground(accent)
	t.ok = lg.NewStyle().Foreground(ok)
	t.warn = lg.NewStyle().Foreground(accent)
	t.danger = lg.NewStyle().Foreground(danger)
	t.invert = lg.NewStyle().Foreground(c("#11131a", "#ffffff")).Background(sel).Bold(true)
	t.title = lg.NewStyle().Foreground(fg).Bold(true)
	t.crumb = lg.NewStyle().Foreground(muted)
	t.rule = lg.NewStyle().Foreground(line)
	t.chip = lg.NewStyle().Foreground(c("#c0c4d6", "#3a3d4d")).Background(c("#2e3140", "#dfe2ec"))
	t.chipAlt = lg.NewStyle().Foreground(sel)

	t.tabOn = lg.NewStyle().Foreground(fg).Bold(true).Underline(true)
	t.tabOff = lg.NewStyle().Foreground(dim)

	t.colHdr = lg.NewStyle().Foreground(muted).Bold(true)
	t.colHdrOn = lg.NewStyle().Foreground(fg).Bold(true)
	// A count pill, not a right-aligned number.
	t.colCount = lg.NewStyle().Foreground(muted).Background(c("#2a2d3a", "#e3e6ef"))
	// The column container: one step off the terminal background, deliberately
	// NOT the `panel` colour the selected card uses, so the two never merge.
	t.colBG = lg.NewStyle().Background(c("#1a1c24", "#f2f4f9"))
	t.colBGOn = lg.NewStyle().Background(c("#1e2029", "#eaeef7"))

	// Per-label hues. One flat grey chip for every label made `cli`, `ui` and
	// `testing` visually identical, which reads as a tag LIST; GitHub's coloured
	// labels are one of the two or three most recognisable things on a card.
	// Hashed by name, so a label keeps its colour on every card and every run.
	for _, h := range [][2]string{
		{"#7aa2f7", "#2f5fd0"}, {"#57d38c", "#12783f"}, {"#f2a90f", "#8a5b00"},
		{"#f2707a", "#c02c38"}, {"#b48ef2", "#6b3fb8"}, {"#4fd1c5", "#0f7a72"},
		{"#e08fc0", "#a83b7d"}, {"#9aa7c7", "#4a5570"},
	} {
		t.chipHues = append(t.chipHues, lg.NewStyle().Foreground(c(h[0], h[1])).Bold(true))
	}
	// Per-lane identity: GitHub derives a coloured dot from the single-select
	// option, which is what lets you find "Done" without reading.
	t.laneHue = map[string]lg.Style{
		"inbox":       lg.NewStyle().Foreground(c("#8b8fa3", "#63677a")),
		"backlog":     lg.NewStyle().Foreground(c("#7aa2f7", "#2f5fd0")),
		"ready":       lg.NewStyle().Foreground(c("#57d38c", "#12783f")),
		"in-progress": lg.NewStyle().Foreground(c("#f2a90f", "#8a5b00")),
		"done":        lg.NewStyle().Foreground(c("#b48ef2", "#6b3fb8")),
		"icebox":      lg.NewStyle().Foreground(c("#5b5f72", "#9aa0b4")),
	}

	cardBase := lg.NewStyle().Border(lg.RoundedBorder()).Padding(0, 1)
	t.card = cardBase.BorderForeground(line).Foreground(fg)
	// A THICK border, not just a blue one: in -plain, on a 16-colour TTY and for
	// a colourblind reader, a hue-only focus ring is byte-identical to its
	// neighbours. Still one cell wide, so every measured height holds.
	t.cardSel = cardBase.Border(lg.ThickBorder()).BorderForeground(sel).Foreground(fg).Background(panel)
	t.cardLift = cardBase.Border(lg.DoubleBorder()).BorderForeground(accent).Foreground(accent)
	t.cardGhost = cardBase.BorderForeground(accent).Foreground(accent).Background(panel)
	t.cardDone = cardBase.BorderForeground(line).Foreground(dim)
	t.dropInd = lg.NewStyle().Foreground(accent).Bold(true)

	t.peek = lg.NewStyle().Border(lg.RoundedBorder()).BorderForeground(sel).
		Background(panel).Foreground(fg).Padding(0, 1)
	t.peekHdr = lg.NewStyle().Foreground(sel).Bold(true)
	t.status = lg.NewStyle().Foreground(muted)
	t.errText = lg.NewStyle().Foreground(danger)

	// Edges are drawn one step brighter than a card border: they are the
	// SUBJECT of the graph view, not its chrome.
	t.edge = lg.NewStyle().Foreground(c("#7c8098", "#6a6f85"))
	graphNodeBase := lg.NewStyle().Border(lg.RoundedBorder()).Padding(0, 1)
	t.graphNode = graphNodeBase.BorderForeground(line).Foreground(fg)
	t.graphNodeSel = graphNodeBase.Border(lg.ThickBorder()).BorderForeground(sel).
		Foreground(fg).Background(panel)
	t.graphNodeFocus = graphNodeBase.Border(lg.DoubleBorder()).BorderForeground(accent).
		Foreground(fg)
	t.graphNodeFocusSel = graphNodeBase.Border(lg.ThickBorder()).BorderForeground(accent).
		Foreground(fg).Background(panel)
	t.graphNodeDone = graphNodeBase.BorderForeground(line).Foreground(dim)
	t.graphNodeUnknown = graphNodeBase.BorderForeground(danger).Foreground(danger)
	return t
}

// chipFor is a label's stable colour: FNV-1a over the name, into a fixed
// palette. Stable per name means `ui` is the same colour on every card and
// across sessions, which is the only thing that makes a colour a LABEL rather
// than decoration.
func (t *theme) chipFor(name string) lg.Style {
	if len(t.chipHues) == 0 {
		return t.chip
	}
	var h uint32 = 2166136261
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619
	}
	return t.chipHues[int(h%uint32(len(t.chipHues)))]
}

// laneDot is the column's colour identity, falling back to the muted default
// for a lane the palette does not know.
func (t *theme) laneDot(l Lane) lg.Style {
	if s, ok := t.laneHue[l.Name]; ok {
		return s
	}
	return t.dim
}
