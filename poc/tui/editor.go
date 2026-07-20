package main

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

type editorDoneMsg struct {
	id   string
	body string
	err  error
}

// editCmd suspends the TUI for $EDITOR, the way furrow's `edit` does.
func (m *Model) editCmd(t *Task) tea.Cmd {
	f, err := os.CreateTemp("", "furrow-poc-"+t.ID+"-*.md")
	if err != nil {
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	path := f.Name()
	if _, err := f.WriteString(t.Body); err != nil {
		f.Close()
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	f.Close()

	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vi"
	}
	id := t.ID
	return tea.ExecProcess(exec.Command(ed, path), func(runErr error) tea.Msg {
		defer os.Remove(path)
		if runErr != nil {
			return editorDoneMsg{id: id, err: runErr}
		}
		b, err := os.ReadFile(path)
		return editorDoneMsg{id: id, body: string(b), err: err}
	})
}
