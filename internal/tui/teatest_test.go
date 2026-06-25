package tui

import (
	"bytes"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// TestTUIProgramEndToEnd drives the REAL bubbletea Program through a simulated
// terminal (teatest): it boots the model, waits for the first rendered frame,
// navigates, marks a task done, and quits — then asserts BOTH that the frame
// rendered the panes AND that the 'd' keypress persisted through internal/app
// to the store. This is the headless way to verify the interactive UI (it is
// what `furrow ui` runs), complementing the model-level unit tests.
func TestTUIProgramEndToEnd(t *testing.T) {
	a := newTestApp(t) // t-0001 "first" (ready), t-0002 "second" (backlog)
	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 32))

	// Wait until the first real frame is painted (the list title appears).
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("furrow")) && bytes.Contains(b, []byte("first"))
	}, teatest.WithDuration(3*time.Second))

	// Select t-0001 ("first"): canonical order is backlog(second) then
	// ready(first), so move down one, then mark done.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	// Wait for the status line to confirm the mutation rendered.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("done t-0001"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	// The mutation must have persisted through the app to the store.
	tk, _, err := a.Get("t-0001")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "done" || tk.Closed == nil {
		t.Errorf("'d' in the real Program should have marked t-0001 done+closed, got %+v", tk)
	}
}

// TestTUIReorderEndToEnd drives the REAL Program: with two ready-lane tasks it
// selects the first and presses J, then asserts the priority swap persisted
// through internal/app to the store (the headless e2e counterpart to the
// model-level TestTUIReorderKeys).
func TestTUIReorderEndToEnd(t *testing.T) {
	a := newTestApp(t) // t-0001 "first" (ready), t-0002 "second" (backlog)
	if _, err := a.Add("third", app.AddOpts{Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	t1, _, _ := a.Get("t-0001")
	t3, _, _ := a.Get("t-0003")
	p1, p3 := t1.Priority, t3.Priority

	m, err := newModel(a)
	if err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 32))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("first")) && bytes.Contains(b, []byte("third"))
	}, teatest.WithDuration(3*time.Second))

	// Canonical order: t-0002 (backlog), t-0001 (ready), t-0003 (ready). Move
	// down once to select t-0001, then J pushes it below t-0003.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("reordered t-0001"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	gt1, _, _ := a.Get("t-0001")
	gt3, _, _ := a.Get("t-0003")
	if gt1.Priority != p3 || gt3.Priority != p1 {
		t.Errorf("J in the real Program should swap priorities: t-0001=%d (want %d), t-0003=%d (want %d)",
			gt1.Priority, p3, gt3.Priority, p1)
	}
}
