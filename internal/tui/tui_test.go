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
