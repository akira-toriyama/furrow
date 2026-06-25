package tui

import (
	"os"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
	tea "github.com/charmbracelet/bubbletea"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

func newTestApp(t *testing.T) *app.App {
	t.Helper()
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	a := app.NewWithStore(st, cfg, fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})
	if _, err := a.Add("first", app.AddOpts{Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("second", app.AddOpts{Status: "backlog"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func keyMsg(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func sizeMsg(m model) model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm.(model)
}

func send(m model, msg tea.Msg) model {
	mm, _ := m.Update(msg)
	return mm.(model)
}

// prio reads a task's current priority straight from the store (the source of
// truth) so reorder assertions don't depend on the list's cached snapshot.
func prio(t *testing.T, a *app.App, id string) int {
	t.Helper()
	tk, _, err := a.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return tk.Priority
}

// selectID moves the list cursor onto the row with the given task id.
func selectID(t *testing.T, m *model, id string) {
	t.Helper()
	for i, it := range m.list.Items() {
		if it.(taskItem).t.ID == id {
			m.list.Select(i)
			return
		}
	}
	t.Fatalf("task %s not in the list", id)
}

func TestTUILoadsAndRenders(t *testing.T) {
	a := newTestApp(t)
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	m = sizeMsg(m)
	if !m.ready {
		t.Fatal("model not ready after window size")
	}
	if len(m.list.Items()) != 2 {
		t.Fatalf("expected 2 list items, got %d", len(m.list.Items()))
	}
	// View must not panic and must include something from the detail pane.
	if out := m.View(); out == "" {
		t.Error("View() returned empty")
	}
}

func TestTUIDoneKey(t *testing.T) {
	a := newTestApp(t)
	m, _ := newModel(a)
	m = sizeMsg(m)

	// canonical order: backlog(second) before ready(first). Select the ready one.
	// Find index of t-0001 and select it.
	for i, it := range m.list.Items() {
		if it.(taskItem).t.ID == "t-0001" {
			m.list.Select(i)
		}
	}
	m = send(m, keyMsg("d"))

	tk, _, err := a.Get("t-0001")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "done" || tk.Closed == nil {
		t.Errorf("pressing d should mark t-0001 done+closed, got %+v", tk)
	}
}

func TestTUIMoveLaneKey(t *testing.T) {
	a := newTestApp(t)
	m, _ := newModel(a)
	m = sizeMsg(m)

	// select t-0002 (backlog) and move lane forward: backlog -> ready.
	for i, it := range m.list.Items() {
		if it.(taskItem).t.ID == "t-0002" {
			m.list.Select(i)
		}
	}
	m = send(m, keyMsg("]"))

	tk, _, _ := a.Get("t-0002")
	if tk.Status != "ready" {
		t.Errorf("] should move t-0002 backlog -> ready, got %q", tk.Status)
	}
	_ = m
}

func TestTUIQuit(t *testing.T) {
	a := newTestApp(t)
	m, _ := newModel(a)
	m = sizeMsg(m)
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q should return a quit command")
	}
	// the quit cmd should produce a QuitMsg.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q command did not yield tea.QuitMsg")
	}
}

func TestCycleLane(t *testing.T) {
	a := newTestApp(t)
	m, _ := newModel(a)
	lanes := a.Cfg.Lanes // inbox backlog ready in-progress done icebox
	if got := m.cycleLane("backlog", +1); got != "ready" {
		t.Errorf("cycleLane(backlog,+1) = %q, want ready", got)
	}
	if got := m.cycleLane(lanes[0], -1); got != lanes[len(lanes)-1] {
		t.Errorf("cycleLane wraps backwards from first to last, got %q", got)
	}
	_ = core.SchemaVersion
}

// TestTUIDetailCaching locks the navigation-lag fix: the glamour renderer is
// built once and reused across cursor moves (rebuilt only on width change), and
// bodies are cached so moving the cursor doesn't re-read files every keystroke.
func TestTUIDetailCaching(t *testing.T) {
	a := newTestApp(t)
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	m = sizeMsg(m)

	if m.renderer == nil {
		t.Fatal("renderer should be built after the first render")
	}
	r1, w1 := m.renderer, m.rendererWidth
	if sel, ok := m.selected(); ok {
		if _, cached := m.bodies[sel.ID]; !cached {
			t.Errorf("body for %s should be cached after render", sel.ID)
		}
	}

	// Navigate to the other task and back: the renderer must be the SAME
	// instance (reused, not rebuilt per move — that reuse is the fix).
	m = send(m, keyMsg("j"))
	m = send(m, keyMsg("k"))
	if m.renderer != r1 {
		t.Error("renderer was rebuilt during navigation; it should be cached")
	}
	if m.rendererWidth != w1 {
		t.Errorf("rendererWidth changed during navigation: %d -> %d", w1, m.rendererWidth)
	}
	if len(m.bodies) < 2 {
		t.Errorf("both visited bodies should be cached, got %d", len(m.bodies))
	}

	// A width change must rebuild the renderer so content re-wraps.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = mm.(model)
	if m.renderer == r1 {
		t.Error("renderer should be rebuilt when the width changes")
	}
}

// TestTUIChecklistToggle: tab into the detail pane, move the checklist cursor,
// space toggles the focused item, and the cursor stays put across the reload.
func TestTUIChecklistToggle(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.AddCheck("t-0001", "step one"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddCheck("t-0001", "step two"); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	m = sizeMsg(m)

	for i, it := range m.list.Items() {
		if it.(taskItem).t.ID == "t-0001" {
			m.list.Select(i)
		}
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyTab}) // focus the detail pane
	if !m.focusDetail {
		t.Fatal("tab should focus the detail pane")
	}
	m = send(m, keyMsg("j")) // checklist cursor: item 1 -> 2
	if m.checkIdx != 1 {
		t.Fatalf("down should move the checklist cursor to index 1, got %d", m.checkIdx)
	}
	m = send(m, keyMsg(" ")) // space toggles item 2

	tk, _, err := a.Get("t-0001")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Checklist[1].Done {
		t.Errorf("space should mark checklist item 2 done, got %+v", tk.Checklist)
	}
	if tk.Checklist[0].Done {
		t.Errorf("item 1 must be untouched, got %+v", tk.Checklist)
	}
	if m.checkIdx != 1 {
		t.Errorf("cursor should stay on index 1 after toggle, got %d", m.checkIdx)
	}
}

// TestTUIReorderKeys: with two tasks in one lane, J/K swap the selected task's
// priority with its same-lane neighbor, the cursor follows the moved task, and K
// at the top of a lane is a no-op that never reaches across the lane boundary.
func TestTUIReorderKeys(t *testing.T) {
	a := newTestApp(t) // t-0001 (ready), t-0002 (backlog)
	if _, err := a.Add("third", app.AddOpts{Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("fourth", app.AddOpts{Status: "in-progress"}); err != nil {
		t.Fatal(err)
	}
	// canonical order: t-0002 (backlog), t-0001 (ready p100), t-0003 (ready
	// p110), t-0004 (in-progress) — reorder must stay inside the ready lane.
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	m = sizeMsg(m)

	p1, p3 := prio(t, a, "t-0001"), prio(t, a, "t-0003")
	if p1 == p3 {
		t.Fatalf("test needs distinct priorities, both are %d", p1)
	}

	selectID(t, &m, "t-0001")
	m = send(m, keyMsg("J")) // push t-0001 below t-0003

	if got := prio(t, a, "t-0001"); got != p3 {
		t.Errorf("after J, t-0001 priority = %d, want %d (swapped with t-0003)", got, p3)
	}
	if got := prio(t, a, "t-0003"); got != p1 {
		t.Errorf("after J, t-0003 priority = %d, want %d (swapped with t-0001)", got, p1)
	}
	if sel, _ := m.selected(); sel.ID != "t-0001" {
		t.Errorf("the cursor should follow the moved task, got %q", sel.ID)
	}

	m = send(m, keyMsg("K")) // bring t-0001 back up
	if got := prio(t, a, "t-0001"); got != p1 {
		t.Errorf("after K, t-0001 priority = %d, want %d (swapped back)", got, p1)
	}
	if got := prio(t, a, "t-0003"); got != p3 {
		t.Errorf("after K, t-0003 priority = %d, want %d (swapped back)", got, p3)
	}

	// t-0001 is now at the top of the ready lane. K must be a no-op — it must
	// not swap across the boundary into the backlog task t-0002.
	before1, before2 := prio(t, a, "t-0001"), prio(t, a, "t-0002")
	m = send(m, keyMsg("K"))
	if prio(t, a, "t-0001") != before1 || prio(t, a, "t-0002") != before2 {
		t.Errorf("K at the lane top must be a no-op, got t-0001=%d (want %d) t-0002=%d (want %d)",
			prio(t, a, "t-0001"), before1, prio(t, a, "t-0002"), before2)
	}

	// Symmetric boundary: t-0003 is the bottom of the ready lane. J must be a
	// no-op — it must not reach across into the in-progress task t-0004.
	selectID(t, &m, "t-0003")
	before3, before4 := prio(t, a, "t-0003"), prio(t, a, "t-0004")
	m = send(m, keyMsg("J"))
	if prio(t, a, "t-0003") != before3 || prio(t, a, "t-0004") != before4 {
		t.Errorf("J at the lane bottom must be a no-op, got t-0003=%d (want %d) t-0004=%d (want %d)",
			prio(t, a, "t-0003"), before3, prio(t, a, "t-0004"), before4)
	}
}

// TestTUIReorderDetailFocus: K/J are list-only — while the detail pane is
// focused they must not reorder (they fall through to the viewport).
func TestTUIReorderDetailFocus(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.Add("third", app.AddOpts{Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	m = sizeMsg(m)

	selectID(t, &m, "t-0001")
	p1, p3 := prio(t, a, "t-0001"), prio(t, a, "t-0003")

	m = send(m, tea.KeyMsg{Type: tea.KeyTab}) // focus the detail pane
	if !m.focusDetail {
		t.Fatal("tab should focus the detail pane")
	}
	m = send(m, keyMsg("J")) // must NOT reorder while the detail pane is focused

	if prio(t, a, "t-0001") != p1 || prio(t, a, "t-0003") != p3 {
		t.Errorf("J in detail focus must not reorder, got t-0001=%d (want %d) t-0003=%d (want %d)",
			prio(t, a, "t-0001"), p1, prio(t, a, "t-0003"), p3)
	}
}

// TestDumpView writes a rendered frame to a file when TUI_DUMP is set, for
// eyeballing the layout during development (no-op in normal CI runs).
func TestDumpView(t *testing.T) {
	if os.Getenv("TUI_DUMP") == "" {
		t.Skip("set TUI_DUMP=1 to dump a rendered frame")
	}
	a := newTestApp(t)
	m, _ := newModel(a)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(model)
	if err := os.WriteFile("/tmp/tui-view.txt", []byte(m.View()), 0o644); err != nil {
		t.Fatal(err)
	}
}
