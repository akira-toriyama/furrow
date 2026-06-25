package tui

import (
	"bytes"
	"testing"
	"time"

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
