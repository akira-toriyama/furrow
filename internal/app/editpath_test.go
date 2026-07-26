package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// EditPath is the agent-facing half of `furrow edit`: with no TTY the CLI prints
// this path instead of launching $EDITOR, and CLAUDE.md tells agents to edit that
// file directly. It had no test at all, so nothing caught a regression in the one
// path an agent actually uses. (The happy path needs a file-backed store and is
// covered end-to-end in internal/cli; these are the two failure branches, which
// only the app layer can reach.)

// An unknown id is exit 1 — a specifically requested id that is not there — and
// must NOT be a path to a file that a later write would then create.
//
// bite-exempt: characterization. EditPath had zero coverage, so this pins the
// contract CLAUDE.md already documents rather than accompanying a change.
func TestEditPathUnknownIDIsNotFound(t *testing.T) {
	if _, err := newApp().EditPath("t-nope1"); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("EditPath on an unknown id should be exit 1, got %v", err)
	}
}

// A store with no file backing (memstore, and any future in-memory adapter)
// cannot hand out a path. That is exit 3 with a message saying why, never an
// empty string the caller would go on to open.
//
// bite-exempt: characterization, as above.
func TestEditPathNonFileBackedStoreIsInternal(t *testing.T) {
	a := newApp()
	task, err := a.Add("in memory", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.EditPath(task.ID)
	if core.ExitCode(err) != int(core.CodeInternal) {
		t.Errorf("a non-file-backed store should be exit 3, got %v", err)
	}
	if got != "" {
		t.Errorf("a failed EditPath must not return a path, got %q", got)
	}
}
