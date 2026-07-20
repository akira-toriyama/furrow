package main

import (
	"strings"
	"testing"

	lg "charm.land/lipgloss/v2"
)

// Every glyph the UI draws must be single-width. An East-Asian-AMBIGUOUS glyph
// (★ and ⊘ are both ambiguous) measures 1 here and 2 in a CJK-configured
// terminal, which shears the right border of every card by one column — and
// this fixture is entirely Japanese titles, so it would show up immediately.
func TestGlyphsAreSingleWidth(t *testing.T) {
	glyphs := map[string]string{
		"actionable": glyphActionable, "blocked": glyphBlocked, "epic": glyphEpic,
		"stuck": glyphStuck, "done": glyphDone, "open": glyphOpen,
		"unknown": glyphUnknown, "wipOver": glyphWIPOver, "drop": glyphDrop,
		"lift": glyphLift,
	}
	for name, g := range glyphs {
		if w := lg.Width(g); w != 1 {
			t.Errorf("glyph %s (%q) measures %d cells, want 1", name, g, w)
		}
	}
}

// The geometry is handed straight to the mouse hit-test, so a card whose
// measured height differs from its rendered height would make cards accept
// drops aimed at their neighbours.
func TestCardGeometryMatchesTheRender(t *testing.T) {
	b, g := fixtureGraph(t)
	th := newTheme(true)

	for _, task := range b.Tasks() {
		for _, st := range []cardState{cardNormal, cardSelected, cardLifted, cardShadow, cardGhost} {
			out := renderCard(task, g, th, colOuterW, st)
			if w := lg.Width(out); w != colOuterW {
				t.Errorf("%s state=%d rendered width %d, want %d", task.ID, st, w, colOuterW)
			}
			if h := lg.Height(out); h != cardHeight(task, g, th, colOuterW) {
				t.Errorf("%s state=%d rendered height %d, cardHeight says %d",
					task.ID, st, h, cardHeight(task, g, th, colOuterW))
			}
			// Every individual line must be exactly the card width: a
			// double-width CJK glyph landing on the boundary would produce a
			// short line and a ragged border.
			for i, line := range strings.Split(out, "\n") {
				if w := lg.Width(line); w != colOuterW {
					t.Errorf("%s line %d is %d cells wide, want %d: %q",
						task.ID, i, w, colOuterW, ansiStrip(line))
				}
			}
		}
	}
}

func TestCardTitleIsCapped(t *testing.T) {
	g := NewGraph(NewBoard([]*Task{{ID: "x", Status: "backlog"}}))
	th := newTheme(true)
	long := &Task{ID: "x", Status: "backlog", Title: strings.Repeat("非常に長い日本語のタイトル", 20)}
	lines := cardLines(long, g, th, cardInner(colOuterW))
	// maxTitleLines of title + the meta line; no labels so no chip line.
	if len(lines) != maxTitleLines+1 {
		t.Errorf("card has %d lines, want %d", len(lines), maxTitleLines+1)
	}
	if !strings.Contains(lines[maxTitleLines-1], "…") {
		t.Error("a truncated title must say so")
	}
}

func boardModel(t *testing.T, w, h int) *Model {
	t.Helper()
	m := New(newMockProvider())
	m.w, m.h = w, h
	m.recompute()
	m.relayout()
	return m
}

func TestLayoutHitTestRoundTrips(t *testing.T) {
	m := boardModel(t, 140, 40)
	col := m.lay.Col("backlog")
	if col == nil || len(col.Cards) < 2 {
		t.Fatal("expected a populated backlog column")
	}

	for _, box := range col.Cards {
		// Every cell inside a card box must resolve back to that card.
		for _, y := range []int{box.Y, box.Y + box.H/2, box.Y + box.H - 1} {
			lane, idx, ok := m.lay.cardAt(box.X+1, y)
			if !ok || lane != "backlog" || idx != box.Idx {
				t.Errorf("cardAt(%d,%d) = (%s,%d,%v), want (backlog,%d,true)",
					box.X+1, y, lane, idx, ok, box.Idx)
			}
		}
	}

	// The gap between two columns belongs to neither. Column width is
	// NEGOTIATED per frame (columns share the terminal, GitHub-style), so the
	// gutter is measured off the layout rather than assumed to be at 28.
	if len(m.lay.Cols) < 2 {
		t.Fatal("expected at least two visible columns at 140 cells")
	}
	first := m.lay.Cols[0]
	for x := first.X + first.W; x < m.lay.Cols[1].X; x++ {
		if lane, ok := m.lay.laneAtX(x); ok {
			t.Errorf("x=%d is in the inter-column gutter but resolved to %q", x, lane)
		}
	}
	// The gutter BETWEEN two cards is dead space too — a pointer there means
	// "between these two", which is what idxAtY answers, not "on a card".
	if len(col.Cards) >= 2 {
		gap := col.Cards[0].Y + col.Cards[0].H
		if gap >= col.Cards[1].Y {
			t.Fatalf("cards are butt-jointed: card0 ends at %d, card1 starts at %d", gap, col.Cards[1].Y)
		}
		if _, _, ok := m.lay.cardAt(col.X+1, gap); ok {
			t.Error("the gutter between two cards must not hit-test as a card")
		}
	}
	// Above the first card is the column chrome, not a card.
	if _, _, ok := m.lay.cardAt(col.X+1, col.Top-1); ok {
		t.Error("the column header must not hit-test as a card")
	}
}

// idxAtY is midpoint-based: the top half of a card means "insert before me",
// the bottom half means "insert after me". That is what makes a drop land where
// the eye expects with variable-height cards.
func TestIdxAtYIsMidpointBased(t *testing.T) {
	m := boardModel(t, 140, 40)
	col := m.lay.Col("backlog")
	if len(col.Cards) < 3 {
		t.Fatalf("need >= 3 cards, got %d", len(col.Cards))
	}

	first := col.Cards[0]
	if got := m.lay.idxAtY("backlog", first.Y); got != 0 {
		t.Errorf("top of the first card => insert at 0, got %d", got)
	}
	if got := m.lay.idxAtY("backlog", first.Y+first.H-1); got != 1 {
		t.Errorf("bottom of the first card => insert at 1, got %d", got)
	}

	second := col.Cards[1]
	if got := m.lay.idxAtY("backlog", second.Y+1); got != 1 {
		t.Errorf("top of the second card => insert at 1, got %d", got)
	}

	// Below every rendered card is the end of the LANE, not the end of the
	// visible run. The two coincide when nothing is below the fold; when cards
	// ARE hidden they diverge, and the old "lastVisible+1" answer is what made
	// the bottom of a long column unreachable by any gesture — a 13-card backlog
	// whose deepest droppable slot was 2.
	last := col.Cards[len(col.Cards)-1]
	if got := m.lay.idxAtY("backlog", last.Y+last.H+5); got != len(col.Tasks) {
		t.Errorf("past the last card => the end of the lane (%d), got %d", len(col.Tasks), got)
	}
	if col.Hidden == 0 {
		t.Fatal("this assertion is only interesting while backlog has cards below the fold")
	}

	// A column that fits entirely must give the same answer both ways.
	short := m.lay.Col("ready")
	if short != nil && short.Hidden == 0 && len(short.Cards) > 0 {
		lastShort := short.Cards[len(short.Cards)-1]
		if got := m.lay.idxAtY("ready", lastShort.Y+lastShort.H+2); got != lastShort.Idx+1 {
			t.Errorf("with nothing below the fold, past-the-last-card => %d, got %d",
				lastShort.Idx+1, got)
		}
	}

	// An empty lane always offers exactly slot 0 — you must be able to drop
	// into a column that has nothing in it.
	if got := m.lay.idxAtY("inbox", 20); got != 0 {
		t.Errorf("empty lane => 0, got %d", got)
	}
}

func TestHorizontalLaneScrolling(t *testing.T) {
	m := boardModel(t, 140, 40)
	if m.lay.Visible >= len(m.b.Lanes()) {
		t.Fatalf("140 columns should not fit all %d lanes", len(m.b.Lanes()))
	}
	if m.lay.Col("done") != nil {
		t.Error("the done lane should be off-screen at the left-most offset")
	}

	// Walking the cursor right must pan the strip until the far lane is visible.
	for i := 0; i < len(m.b.Lanes()); i++ {
		m.moveCursor(+1, 0)
		m.ensureVisible()
		m.relayout()
	}
	if m.lay.Col("icebox") == nil {
		t.Error("walking to the last lane must scroll it into view")
	}
}

func TestFrameIsExactlyTheTerminalSize(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {80, 24}, {60, 20}} {
		m := boardModel(t, size[0], size[1])
		for _, name := range []string{"board", "peek", "table", "help"} {
			switch name {
			case "peek":
				m.peekOpen, m.treeOpen = true, true
				m.syncPeek()
			case "table":
				m.peekOpen, m.view = false, viewTable
			case "help":
				m.view, m.fullHelp = viewBoard, true
			}
			out := m.View().Content
			if h := lg.Height(out); h != size[1] {
				t.Errorf("%s at %dx%d: %d rows, want %d", name, size[0], size[1], h, size[1])
			}
			for i, line := range strings.Split(out, "\n") {
				if w := lg.Width(line); w > size[0] {
					t.Errorf("%s at %dx%d: line %d is %d cells (overflows)", name, size[0], size[1], i, w)
				}
			}
		}
	}
}
