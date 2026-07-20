package main

import (
	"fmt"
	"strings"

	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// maxTitleLines caps a card's title. The board's job is to let you FIND a task;
// the detail peek shows the whole title. Without a cap one 60-character
// Japanese title would eat a whole column.
const maxTitleLines = 3

// cardState selects the card's visual treatment.
type cardState int

const (
	cardNormal cardState = iota
	cardSelected
	cardLifted // move mode: the source card, visibly detached
	cardShadow // mouse drag: the source card, left in place so nothing reflows
	cardGhost  // mouse drag: the copy that follows the cursor
)

// cardStyle picks the border/foreground treatment for a state.
func (t *theme) cardStyle(st cardState, done bool) lg.Style {
	switch st {
	case cardSelected:
		return t.cardSel
	case cardLifted:
		return t.cardLift
	case cardShadow:
		return t.cardDone
	case cardGhost:
		return t.cardGhost
	}
	if done {
		return t.cardDone
	}
	return t.card
}

// cardMarker is the leading glyph: the single most important fact about a task.
// Priority order is deliberate — blocked beats actionable beats box.
func cardMarker(t *Task, g *Graph) (glyph string, style func(*theme) lg.Style) {
	switch {
	case len(g.BlockedBy(t.ID)) > 0:
		return glyphBlocked, func(th *theme) lg.Style { return th.danger }
	case g.Actionable(t.ID):
		return glyphActionable, func(th *theme) lg.Style { return th.ok }
	case g.IsContainer(t.ID):
		if g.Stuck(t.ID) {
			return glyphStuck, func(th *theme) lg.Style { return th.warn }
		}
		return glyphEpic, func(th *theme) lg.Style { return th.accent }
	case g.IsDone(t.ID):
		return glyphDone, func(th *theme) lg.Style { return th.dim }
	}
	return " ", func(th *theme) lg.Style { return th.dim }
}

// wrapLines wraps to width w, hard-wrapping runs with no breakpoints (a
// Japanese title has no spaces at all) and returns unpadded lines.
func wrapLines(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	wrapped := lg.NewStyle().Width(w).Render(s)
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

// pad right-pads to exactly w display cells, truncating with an ellipsis when
// too long. Everything that composes a card line goes through this, which is
// why a double-width Japanese glyph cannot shear the right border.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lg.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if d := w - lg.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// cardLines builds a card's inner content (no border, no padding) at exactly
// width w. Height is a pure function of the task and w, so the layout can be
// computed from the same call the renderer uses — geometry and pixels can never
// disagree.
func cardLines(t *Task, g *Graph, th *theme, w int) []string {
	var out []string

	glyph, styleFor := cardMarker(t, g)
	body := wrapLines(t.Title, w-2)
	if len(body) > maxTitleLines {
		body = body[:maxTitleLines]
		body[maxTitleLines-1] = ansi.Truncate(body[maxTitleLines-1], w-2-1, "…")
	}
	for i, l := range body {
		lead := "  "
		if i == 0 {
			lead = styleFor(th).Render(glyph) + " "
		}
		out = append(out, lead+pad(l, w-2))
	}

	// chip line: labels only, and dropped entirely when there are none.
	//
	// It sits ABOVE the metadata, which is GitHub's order (title → labels →
	// footer metadata). Putting the id line above the chips was a large part of
	// why the card read as a log entry rather than a card.
	if len(t.Labels) > 0 {
		var chips []string
		for _, l := range t.Labels {
			chips = append(chips, th.chipFor(l).Render("●")+th.chip.Render(" "+l+" "))
		}
		out = append(out, pad(strings.Join(chips, " "), w))
	}

	// meta line: id + repo on the left, the numbers on the right. Repo lives
	// here rather than on the chip line so a label-less task (most of them)
	// costs one row less — at 6 rows a card the board only shows four.
	left := th.dim.Render(t.ID)
	if r := t.ShortRepo(); r != "" {
		left += " " + th.chipAlt.Render(r)
	}
	var bits []string
	if t.Value > 0 || t.Effort > 0 {
		bits = append(bits, th.muted.Render(fmt.Sprintf("%d/%d", t.Value, t.Effort)))
	}
	if n := len(g.BlockedBy(t.ID)); n > 0 {
		bits = append(bits, th.danger.Render(fmt.Sprintf("%s%d", glyphBlocked, n)))
	}
	if g.IsContainer(t.ID) {
		d, tot := g.Progress(t.ID, false)
		bits = append(bits, th.accent.Render(fmt.Sprintf("%d/%d", d, tot)))
	}
	if n, tot := t.CheckProgress(); tot > 0 {
		bits = append(bits, th.muted.Render(fmt.Sprintf("[%d/%d]", n, tot)))
	}
	right := strings.Join(bits, " ")
	out = append(out, joinEnds(left, right, w))
	return out
}

// joinEnds places left and right at the two ends of a w-wide line, dropping the
// right side rather than overflowing.
func joinEnds(left, right string, w int) string {
	lw, rw := lg.Width(left), lg.Width(right)
	if lw+rw+1 > w {
		if rw+1 >= w {
			return pad(right, w)
		}
		left = ansi.Truncate(left, w-rw-1, "…")
		lw = lg.Width(left)
	}
	gap := w - lw - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// cardInner is the usable content width inside a card of outer width outerW.
// lipgloss v2's Style.Width(n) is the TOTAL rendered width — border and padding
// included — so a RoundedBorder plus Padding(0,1) costs 4 columns. Getting this
// off by one does not error: it silently re-wraps one line per card, which
// makes every measured card height a lie and shears the columns below it.
func cardInner(outerW int) int { return maxInt(1, outerW-4) }

// renderCard draws a whole card, border included, at outer width outerW.
func renderCard(t *Task, g *Graph, th *theme, outerW int, st cardState) string {
	lines := cardLines(t, g, th, cardInner(outerW))
	return th.cardStyle(st, g.IsDone(t.ID)).Width(outerW).Render(strings.Join(lines, "\n"))
}

// cardHeight MEASURES the card rather than predicting it. Predicting
// (len(lines)+2) is one lipgloss wrapping rule away from being wrong, and a
// wrong height is not a cosmetic bug: the layout hands those y positions
// straight to the mouse hit-test, so cards would take drops aimed at their
// neighbours. Measuring costs one render per card per frame for 24 cards.
func cardHeight(t *Task, g *Graph, th *theme, outerW int) int {
	return lg.Height(renderCard(t, g, th, outerW, cardNormal))
}
