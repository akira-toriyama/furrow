package main

import (
	"strings"
	"testing"
)

func frame(m *Model) string { return ansiStrip(m.View().Content) }

func TestMoveModeRendersALiftedCardAndADropIndicator(t *testing.T) {
	m := boardModel(t, 140, 30)
	if err := m.demoState("move"); err != nil {
		t.Fatal(err)
	}
	out := frame(m)

	// The lifted card switches to a double border — the card is visibly
	// detached, which is the whole affordance of GitHub's move mode.
	if !strings.Contains(out, "╔") {
		t.Error("the lifted card should be drawn with a distinct border")
	}
	// The drop indicator is drawn in the DESTINATION lane.
	if !strings.Contains(out, strings.Repeat(glyphDrop, 8)) {
		t.Error("no drop indicator in the frame")
	}
	dropCol := m.lay.Col(m.dropLane)
	y, ok := m.lay.dropY(m.dropLane, m.dropIdx)
	if !ok {
		t.Fatal("dropY failed")
	}
	line := strings.Split(out, "\n")[y]
	if !strings.Contains(line[minInt(dropCol.X, len(line)):], glyphDrop) {
		t.Errorf("the drop indicator is not on row %d of the %s column: %q", y, m.dropLane, line)
	}
	// The status bar has to say what is happening and how to get out.
	if !strings.Contains(out, "MOVE "+m.moveID) || !strings.Contains(out, "esc restore") {
		t.Error("move mode must announce itself and its exit in the status bar")
	}
	// And the footer must switch to the move keymap, not lie about the arrows.
	if strings.Contains(out, "jump to blocker") {
		t.Error("the footer still shows the normal keymap during a move")
	}
}

func TestDragRendersAGhostAndLeavesAShadow(t *testing.T) {
	m := boardModel(t, 140, 30)
	if err := m.demoState("drag"); err != nil {
		t.Fatal(err)
	}
	out := frame(m)
	id := m.drag.id
	if id == "" {
		t.Fatal("no drag in flight")
	}

	// The card appears TWICE: once as the shadow left in its original slot (so
	// nothing reflows under the cursor) and once as the ghost under the pointer.
	if n := strings.Count(out, id); n < 2 {
		t.Errorf("%s appears %d times; want a shadow AND a ghost", id, n)
	}
	if !strings.Contains(out, "DRAG "+id) {
		t.Error("the status bar must name the dragged card")
	}
	if !strings.Contains(out, "⟨DRAG⟩") {
		t.Error("the mode badge must show the drag")
	}

	// The ghost is a top-z layer: it must be drawn over the lane it is above.
	g := m.ghostLayer()
	if g == nil {
		t.Fatal("no ghost layer")
	}
	if g.GetZ() != zGhost {
		t.Errorf("ghost z = %d, want %d", g.GetZ(), zGhost)
	}
	// The grab offset must be preserved, or the card snaps under the cursor.
	if m.drag.grabDX == 0 && m.drag.grabDY == 0 {
		t.Error("the grab offset was not recorded")
	}
	if g.GetX() != clamp(m.drag.x-m.drag.grabDX, 0, maxInt(0, m.w-colOuterW)) {
		t.Error("the ghost is not offset by the grab point")
	}
}

// Requirement 1 of dependency legibility: the peek resolves ids to titles and
// lanes, in BOTH directions. A raw id list is what the shard already gives you.
func TestPeekResolvesDependenciesBothWays(t *testing.T) {
	m := boardModel(t, 140, 40)
	m.peekOpen = true
	if !m.selectID("t-jv3j", false) {
		t.Fatal("could not select t-jv3j")
	}
	m.syncPeek()
	out := ansiStrip(m.peekContent(64))

	if !strings.Contains(out, "blocked by") || !strings.Contains(out, "blocks") {
		t.Fatal("both dependency directions must have a section")
	}
	// Forward edge, resolved to a title and a lane — not just "t-ehk7".
	if !strings.Contains(out, "t-ehk7") || !strings.Contains(out, "[backlog]") {
		t.Error("the forward dep is not resolved to a title + lane")
	}
	// A done dep is marked done rather than being silently dropped.
	if !strings.Contains(out, "t-t38k") || !strings.Contains(out, "[done]") {
		t.Error("a satisfied dep must still be listed, marked done")
	}
	// Reverse edges exist nowhere on disk; they have to be computed.
	for _, id := range m.g.Blocks("t-jv3j") {
		if !strings.Contains(out, id) {
			t.Errorf("reverse edge %s is missing from the peek", id)
		}
	}
	// Titles, not bare ids.
	if !strings.Contains(out, "typed query") {
		t.Error("dep lines must carry the blocker's title")
	}
}

func TestPeekMarksAnUnknownDepRatherThanHidingIt(t *testing.T) {
	b := NewBoard([]*Task{{ID: "a", Status: "ready", Title: "A", Deps: []string{"ghost"}}})
	m := New(&mockProvider{b: b})
	m.w, m.h = 120, 30
	m.peekOpen = true
	m.recompute()
	out := ansiStrip(m.peekContent(60))

	if !strings.Contains(out, glyphUnknown) || !strings.Contains(out, "not on this board") {
		t.Errorf("an unresolvable dep must be marked, not dropped:\n%s", out)
	}
}

func TestBlockedCardsAreMarkedNotHidden(t *testing.T) {
	m := boardModel(t, 140, 40)
	out := frame(m)

	// t-jv3j is blocked and must carry the glyph and a blocker count.
	if !strings.Contains(out, "t-jv3j") {
		t.Fatal("t-jv3j is not on the board")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "t-jv3j") {
			if !strings.Contains(line, glyphBlocked+"1") {
				t.Errorf("the blocked card must show its blocker count: %q", line)
			}
			return
		}
	}
}

func TestActionableAndEpicGlyphs(t *testing.T) {
	m := boardModel(t, 140, 40)
	out := frame(m)

	// t-n2fc is the one actionable task; t-fw2m is the epic.
	for id, want := range map[string]string{"t-n2fc": glyphActionable, "t-fw2m": glyphEpic} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, id) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not on the board", id)
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("the %s glyph (%q) is missing from the frame", id, want)
		}
	}
	// The epic shows rolled-up child progress, not its own (absent) checklist.
	d, tot := m.g.Progress("t-fw2m", false)
	if tot != 18 {
		t.Fatalf("epic has %d children", tot)
	}
	if !strings.Contains(out, "6/18") && !strings.Contains(out, "0/18") {
		t.Errorf("the epic card should show %d/%d child progress", d, tot)
	}
}

// The WIP limit is RENDERED, never enforced — GitHub Projects parity.
func TestWIPLimitIsShownButNotEnforced(t *testing.T) {
	m := boardModel(t, 140, 40)
	// ready has WIP 2. Pile four tasks into it.
	var moved []string
	for _, task := range m.b.LaneTasks("backlog")[:4] {
		moved = append(moved, task.ID)
	}
	for _, id := range moved {
		if _, err := m.prov.Move(id, "ready", 0); err != nil {
			t.Fatal(err)
		}
	}
	m.reload()
	m.relayout()

	if n := len(m.b.LaneTasks("ready")); n < 5 {
		t.Fatalf("ready has %d tasks; the move should not have been refused", n)
	}
	out := frame(m)
	if !strings.Contains(out, "5/2"+glyphWIPOver) {
		t.Errorf("an over-limit column must be badged, got:\n%s", strings.Split(out, "\n")[rowColHdr])
	}
}

func TestFilterProblemsAreSurfacedWithoutDiscardingTheQuery(t *testing.T) {
	m := boardModel(t, 140, 30)
	m.applyFilter("nope:x lane:ready")
	m.relayout()
	out := frame(m)

	if !strings.Contains(out, "unknown key") {
		t.Error("a bad token must be reported in the filter bar")
	}
	// The valid half still applies.
	if n := m.countVisible(); n != 1 {
		t.Errorf("the good half of the query must still filter: %d visible", n)
	}
}

func TestTableViewListsEveryVisibleTask(t *testing.T) {
	m := boardModel(t, 160, 40)
	m.view = viewTable
	out := frame(m)
	rows := m.tableRows()
	if len(rows) != 24 {
		t.Fatalf("expected all 24 tasks, got %d", len(rows))
	}
	// The cursor is a glyph, not only colour, so -plain can see it.
	if !strings.Contains(out, "▌ "+rows[0].ID) {
		t.Error("the table cursor must be visible without colour")
	}
}

func TestEmptyLaneIsFocusableSoItCanReceiveADrop(t *testing.T) {
	m := boardModel(t, 140, 40)
	if len(m.cols["inbox"]) != 0 {
		t.Skip("inbox is not empty in this fixture")
	}
	m.curLane = m.b.LaneIndex("backlog")
	m.moveCursor(-1, 0)
	if m.curLaneName() != "inbox" {
		t.Errorf("focus = %s; an empty lane must be reachable, or it can never be a drop target", m.curLaneName())
	}
	if m.curTask() != nil {
		t.Error("an empty lane has no selection")
	}
	// Move mode into the empty lane must work.
	m.curLane = m.b.LaneIndex("backlog")
	m.setPos(0)
	id := m.curTask().ID
	m.enterMove()
	m.shiftDropLane(-1)
	if _, err := m.commitMove(id, "backlog", m.dropLane, m.moveFromIdx, m.dropIdx); err != nil {
		t.Fatal(err)
	}
	if m.b.Task(id).Status != "inbox" {
		t.Errorf("%s went to %s, want inbox", id, m.b.Task(id).Status)
	}
}
