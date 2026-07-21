package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
)

// z-order. The compositor sorts on these, which is the whole reason this POC
// uses lipgloss v2's Layer/Compositor rather than string concatenation: a
// side-peek, a drop indicator and a dragged ghost are three different planes
// over the same board.
const (
	zChrome = 0
	zColBG  = 9 // the column container, under every card
	zCard   = 10
	zDrop   = 50 // drop indicator — deliberately given NO id so Hit() skips it
	zPeek   = 80
	zHelp   = 90
	zGhost  = 99 // the dragged card, above everything
)

// View renders one frame. Everything that used to be a NewProgram option in
// bubbletea v1 is now a field re-asserted here every render — which is exactly
// what makes runtime mouse toggling (the `M` key) free.
func (m *Model) View() tea.View {
	m.relayout()
	var content string
	switch m.view {
	case viewTable:
		content = m.renderTable()
	case viewGraph:
		content = m.renderGraph()
	default:
		content = m.renderBoard()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeNone
	if m.mouseOn {
		// CellMotion (1002), never AllMotion (1003): 1002 still delivers motion
		// events while a button is held — which is all a drag needs — and has
		// far better tmux/mosh support.
		v.MouseMode = tea.MouseModeCellMotion
	}
	// Ask for the Kitty keyboard protocol. Without it a terminal CANNOT ENCODE A
	// MODIFIED SPACE at all: shift+space arrives as a bare space, so the graph
	// binding is simply unreachable and the peek opens instead. That is not a
	// theory — it is what the first person to run this POC hit. Ghostty, kitty,
	// WezTerm and recent iTerm2 honour this; everywhere else the "S" alias is the
	// way in, which is why every gesture here keeps a plain-key twin.
	// ReportAllKeysAsEscapeCodes is the one that matters: a plain space is sent
	// as TEXT, and text carries no modifier, so shift+space is indistinguishable
	// from space until every key comes back as an escape code. Basic
	// disambiguation (flag 1, always on) is not enough for this gesture.
	v.KeyboardEnhancements = tea.KeyboardEnhancements{ReportAllKeysAsEscapeCodes: true}
	v.WindowTitle = "furrow board (POC)"
	return v
}

// fitFrame is the hard backstop: no frame may ever be larger than the terminal
// it was handed. Individual pieces clamp themselves, but a compositor grows its
// canvas to fit any layer, so one oversized child (a card wider than a 20-column
// terminal, a peek taller than 5 rows) silently expanded the whole frame — which
// is how a 1x1 terminal rendered 28x6.
func (m *Model) fitFrame(s string) string {
	return lg.NewStyle().MaxWidth(maxInt(m.w, 1)).MaxHeight(maxInt(m.h, 1)).Render(s)
}

func blankCanvas(w, h int) string {
	row := strings.Repeat(" ", maxInt(w, 1))
	rows := make([]string, maxInt(h, 1))
	for i := range rows {
		rows[i] = row
	}
	return strings.Join(rows, "\n")
}

func (m *Model) renderBoard() string {
	layers := []*lg.Layer{lg.NewLayer(blankCanvas(m.w, m.h)).X(0).Y(0).Z(zChrome - 1)}
	layers = append(layers, m.chromeLayers()...)
	layers = append(layers, m.columnLayers()...)
	if m.peekOpen {
		layers = append(layers, m.peekLayer())
	}
	if m.fullHelp {
		layers = append(layers, m.helpLayer())
	}
	if g := m.ghostLayer(); g != nil {
		layers = append(layers, g)
	}
	return m.fitFrame(lg.NewCompositor(layers...).Render())
}

// chromeLayers draws the title bar, filter bar, footer and help line.
func (m *Model) chromeLayers() []*lg.Layer {
	th := m.th
	total := len(m.b.Tasks())
	shown := m.countVisible()

	// The Board | Table tab strip. `v` toggled the table view with no on-screen
	// affordance at all; the tab row under the project title is GitHub's most
	// recognisable chrome after the columns themselves.
	tab := func(name string, on bool) string {
		if on {
			return th.tabOn.Render(name)
		}
		return th.tabOff.Render(name)
	}
	tabs := tab("Board", m.view == viewBoard) + th.dim.Render(" │ ") + tab("Table", m.view == viewTable)

	left := th.title.Render("furrow board") + th.crumb.Render("  ·  ") + tabs
	counts := fmt.Sprintf("%d/%d tasks", shown, total)
	if shown == total {
		counts = fmt.Sprintf("%d tasks", total)
	}
	right := th.crumb.Render(counts + "  ·  " + m.modeBadge())
	title := joinEnds(left, right, m.w)

	var filter string
	switch {
	case m.mode == modeFilter:
		filter = m.ti.View()
	case m.q.Empty():
		// GitHub's literal placeholder. The old text spelled out a full example
		// query, which read as an ACTIVE filter — in every dump the bar said
		// "lane:ready repo:vista is:blocked" while the header said "24 tasks"
		// and every lane was full. The syntax belongs in the ? overlay.
		filter = th.dim.Render("/ Filter by keyword or by field")
	default:
		filter = th.dim.Render("/ ") + th.chipAlt.Render(m.q.Raw)
	}
	if len(m.q.Problems) > 0 {
		filter = joinEnds(filter, th.errText.Render("⚠ "+strings.Join(m.q.Problems, "; ")), m.w)
	}
	if len(m.pinned) > 0 {
		filter = joinEnds(filter, th.accent.Render(fmt.Sprintf("+%d pinned by jump", len(m.pinned))), m.w)
	}

	status := m.statusLine()
	var helpBar string
	if m.mode == modeMove {
		helpBar = m.help.ShortHelpView(m.keys.moveHelp())
	} else {
		helpBar = m.help.ShortHelpView(m.keys.ShortHelp())
	}

	// maxInt(0, …): on a 1- or 2-row terminal m.h-2 is negative, and the
	// compositor NORMALISES negative coordinates by shifting the whole scene
	// down — so a 1-row terminal came back 6 rows tall with the help bar on row
	// 0. Clamp to the canvas and let fitFrame trim.
	return []*lg.Layer{
		lg.NewLayer(pad(title, m.w)).X(0).Y(rowTitle).Z(zChrome),
		lg.NewLayer(pad(filter, m.w)).X(0).Y(rowFilter).Z(zChrome),
		lg.NewLayer(pad(status, m.w)).X(0).Y(maxInt(0, m.h-2)).Z(zChrome),
		lg.NewLayer(pad(helpBar, m.w)).X(0).Y(maxInt(0, m.h-1)).Z(zChrome),
	}
}

func (m *Model) modeBadge() string {
	th := m.th
	switch {
	case m.mode == modeMove:
		return th.accent.Render("⟨MOVE⟩")
	case m.drag.moved:
		return th.accent.Render("⟨DRAG⟩")
	case m.mode == modeFilter:
		return th.chipAlt.Render("⟨FILTER⟩")
	case !m.mouseOn:
		return th.dim.Render("mouse off")
	}
	return th.dim.Render("mouse on")
}

func (m *Model) statusLine() string {
	th := m.th
	if m.mode == modeMove {
		return th.accent.Render(fmt.Sprintf("%s MOVE %s → %s [slot %d]   ⏎ commit · esc restore",
			glyphLift, m.moveID, m.dropLane, m.dropIdx))
	}
	if m.drag.moved {
		return th.accent.Render(fmt.Sprintf("%s DRAG %s → %s [slot %d]   release to drop · esc cancel",
			glyphLift, m.drag.id, m.drag.dropLane, m.drag.dropIdx))
	}
	if m.statusErr {
		return th.errText.Render("⚠ " + m.status)
	}
	return th.status.Render(m.status)
}

// columnLayers draws every visible column: container, header, rule, cards, drop
// indicator.
func (m *Model) columnLayers() []*lg.Layer {
	th := m.th
	var out []*lg.Layer
	for i := range m.lay.Cols {
		c := &m.lay.Cols[i]
		focused := c.Lane.Name == m.curLaneName()

		// The column CONTAINER. Without an inset background an empty column is
		// literally invisible — at 200 columns the right third of the screen
		// read as broken rather than as "these lanes are empty" — and an
		// invisible column is not an obvious drop target either. No ID(), so
		// Compositor.Hit() skips it and card hit-testing is untouched.
		if bh := c.Bot - c.Top; bh > 0 {
			bg := th.colBG
			if focused {
				bg = th.colBGOn
			}
			out = append(out, lg.NewLayer(bg.Render(blankCanvas(c.W, bh))).
				X(c.X).Y(c.Top).Z(zColBG))
		}

		hdrStyle := th.colHdr
		ruleStyle := th.rule
		if focused {
			hdrStyle, ruleStyle = th.colHdrOn, th.chipAlt
		}
		// Lane dot + display name + count, all packed at the LEFT. The count used
		// to be flush right, 22 cells of dead space away from the name, which is
		// spreadsheet grammar and fights the "board" read.
		count := fmt.Sprintf("%d", len(c.Tasks))
		if c.Lane.WIP > 0 {
			// GitHub Projects parity: the limit is RENDERED, never enforced.
			count = fmt.Sprintf("%d/%d", len(c.Tasks), c.Lane.WIP)
			if len(c.Tasks) > c.Lane.WIP {
				count = th.warn.Render(count + glyphWIPOver)
			} else {
				count = th.colCount.Render(" " + count + " ")
			}
		} else {
			count = th.colCount.Render(" " + count + " ")
		}
		hdr := th.laneDot(c.Lane).Render(glyphLaneDot) + " " +
			hdrStyle.Render(c.Lane.DisplayName()) + " " + count
		out = append(out, lg.NewLayer(pad(hdr, c.W)).X(c.X).Y(rowColHdr).Z(zChrome))

		sv, se := 0, 0
		for _, t := range c.Tasks {
			sv += t.Value
			se += t.Effort
		}
		// A number-field sum of zero under an empty lane is noise in the densest
		// part of the chrome; GitHub only shows sums it has something to sum.
		sum := ""
		if sv+se > 0 {
			sum = th.dim.Render(fmt.Sprintf("v%d e%d", sv, se))
		}
		var hint string
		switch {
		case c.Hidden > 0:
			hint = th.dim.Render(fmt.Sprintf("+%d below", c.Hidden))
		case c.Scroll > 0:
			hint = th.dim.Render(fmt.Sprintf("%d above", c.Scroll))
		}
		out = append(out, lg.NewLayer(joinEnds(sum, hint, c.W)).X(c.X).Y(rowColSum).Z(zChrome))
		out = append(out, lg.NewLayer(ruleStyle.Render(strings.Repeat("─", maxInt(c.W, 1)))).
			X(c.X).Y(rowRule).Z(zChrome))

		for _, box := range c.Cards {
			t := c.Tasks[box.Idx]
			st := cardNormal
			switch {
			case m.drag.moved && t.ID == m.drag.id:
				st = cardShadow
			case m.mode == modeMove && t.ID == m.moveID:
				st = cardLifted
			case focused && box.Idx == m.curPos() && m.mode != modeMove:
				st = cardSelected
			}
			out = append(out, lg.NewLayer(renderCard(t, m.g, th, c.W, st)).
				ID("task:"+t.ID).X(box.X).Y(box.Y).Z(zCard))
		}

		if c.Scroll > 0 && c.Bot > c.Top {
			out = append(out, lg.NewLayer(th.dim.Render("▲")).X(c.X+maxInt(0, c.W-2)).Y(c.Top).Z(zCard+1))
		}
	}
	if l := m.dropLayer(); l != nil {
		out = append(out, l)
	}
	return out
}

// dropLayer draws the insertion marker for whichever gesture is running. It is
// given NO id, so Compositor.Hit() skips it — a drop indicator must never
// swallow the click that is aimed at the card underneath.
//
// It is drawn as a bracketed dashed caret rather than a solid hairline: a solid
// bar was visually the same object as the header rule under each column and read
// as a section divider, not an insertion point.
func (m *Model) dropLayer() *lg.Layer {
	var lane string
	var idx int
	switch {
	case m.mode == modeMove:
		lane, idx = m.dropLane, m.dropIdx
	case m.drag.moved:
		lane, idx = m.drag.dropLane, m.drag.dropIdx
	default:
		return nil
	}
	c := m.lay.Col(lane)
	if c == nil || c.Bot <= c.Top {
		return nil
	}
	y, ok := m.lay.dropY(lane, idx)
	if !ok {
		return nil
	}
	bar := glyphDropL + strings.Repeat(glyphDrop, maxInt(0, c.W-2)) + glyphDropR
	return lg.NewLayer(m.th.dropInd.Render(pad(bar, c.W))).
		X(c.X).Y(clamp(y, c.Top, maxInt(c.Top, c.Bot-1))).Z(zDrop)
}

// ghostLayer is the dragged card following the cursor, offset by where it was
// grabbed so it does not snap under the pointer.
func (m *Model) ghostLayer() *lg.Layer {
	if !m.drag.moved {
		return nil
	}
	t := m.b.Task(m.drag.id)
	if t == nil {
		return nil
	}
	// The layout's negotiated width, not the preferred constant: on a terminal
	// narrower than one preferred column the ghost was still drawn at 28 cells
	// and the clamp degenerated to 0, so the ghost alone widened the frame.
	cw := minInt(m.lay.ColW, maxInt(m.w, 1))
	card := renderCard(t, m.g, m.th, cw, cardGhost)
	x := clamp(m.drag.x-m.drag.grabDX, 0, maxInt(0, m.w-cw))
	y := clamp(m.drag.y-m.drag.grabDY, 0, maxInt(0, m.h-lg.Height(card)))
	return lg.NewLayer(card).ID("ghost").X(x).Y(y).Z(zGhost)
}

// helpLayer is the `?` overlay. bubbles' FullHelpView lays every group out on
// ONE row and does not consult SetWidth, so at 80 columns it renders a 98-cell
// block and silently overflows the frame; the groups are packed into as many
// rows as the terminal actually has room for.
func (m *Model) helpLayer() *lg.Layer {
	inner := maxInt(16, m.w-6)

	var rows []string
	var cur [][]key.Binding
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, m.help.FullHelpView(cur))
			cur = nil
		}
	}
	for _, grp := range m.keys.FullHelp() {
		cand := append(append([][]key.Binding{}, cur...), grp)
		if len(cur) > 0 && lg.Width(m.help.FullHelpView(cand)) > inner {
			flush()
			cur = [][]key.Binding{grp}
			continue
		}
		cur = cand
	}
	flush()

	syntax := wrapJoin([]string{"filter syntax:",
		"lane: repo: label: type: is: no: has: id: parent:",
		"· comma = OR · leading - negates · bare word = title/id"}, " ", inner)
	note := wrapJoin([]string{"every mouse gesture above has a keyboard twin —",
		"that is the rule, not a bonus"}, " ", inner)
	box := m.th.peek.Render(m.th.peekHdr.Render("keys") + "\n\n" +
		strings.Join(rows, "\n") + "\n\n" +
		m.th.dim.Render(syntax) + "\n" + m.th.dim.Render(note))
	// Hard backstop: an overlay must never be able to overflow the frame.
	box = lg.NewStyle().MaxWidth(m.w).MaxHeight(m.h).Render(box)

	x := maxInt(0, (m.w-lg.Width(box))/2)
	y := maxInt(0, (m.h-lg.Height(box))/2)
	return lg.NewLayer(box).ID("help").X(x).Y(y).Z(zHelp)
}
