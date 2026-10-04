package cli

import (
	"strings"
	"testing"
)

// A write that leaves the board in a lint error it did not have says so on
// stderr in the same command (t-7vgb): before, the writer learned it from the
// board's pre-push lint, one round trip later. The note is the lint finding
// itself — same code, same message — and the exit stays 0.
func TestWriteNamesTheLintErrorItCreates(t *testing.T) {
	freezeClock(t)
	initStore(t)
	blocker := addTask(t, "blocker")
	waiter := addTask(t, "waiter", "--dep", blocker)

	_, se, code := runSplit(t, "set", waiter, "-s", "ready")
	if code != 0 || !strings.Contains(se, "note: this write leaves "+waiter+" in lint error ready-blocked: ") || !strings.Contains(se, blocker) {
		t.Errorf("set -s ready over an open dep: exit %d, want the ready-blocked note naming %s:\n%s", code, blocker, se)
	}
	// Touching a task that was already in error is not a new finding.
	if _, se, _ = runSplit(t, "value", waiter, "3"); strings.Contains(se, "lint error") {
		t.Errorf("a write on an already-blocked task must stay quiet:\n%s", se)
	}

	edge := addTask(t, "edge", "-s", "ready")
	if _, se, _ = runSplit(t, "dep", edge, blocker); !strings.Contains(se, "leaves "+edge+" in lint error ready-blocked") {
		t.Errorf("a dep edge onto a ready task must name the error it creates:\n%s", se)
	}

	_, se, _ = runSplit(t, "add", "late", "--due", dayOffset(t, -3))
	if !strings.Contains(se, "in lint error due-overdue: ") {
		t.Errorf("add --due in the past must name due-overdue:\n%s", se)
	}

	// The error can land on a task the command never named: reopening a done
	// dep puts the task that sat ready on it back in ready-blocked.
	dep := addTask(t, "dep")
	dependent := addTask(t, "dependent", "--dep", dep)
	mustRun(t, "done", dep)
	mustRun(t, "set", dependent, "-s", "ready")
	if _, se, _ = runSplit(t, "move", dep, "backlog"); !strings.Contains(se, "leaves "+dependent+" in lint error ready-blocked") {
		t.Errorf("reopening a done dep must name the dependent it blocks:\n%s", se)
	}

	// Reads never note.
	if _, se, _ = runSplit(t, "ls"); strings.Contains(se, "lint error") {
		t.Errorf("a read must not note lint errors:\n%s", se)
	}
}

// The note follows the board's lint policy: an error the board re-levels to a
// warning is not an error to note, so the note never calls something an error
// that `furrow lint` does not.
func TestWriteLintNoteFollowsTheBoardPolicy(t *testing.T) {
	freezeClock(t)
	initStore(t)
	mustRun(t, "config", "set", "lint.severity.due-overdue", "warn")
	if _, se, _ := runSplit(t, "add", "late", "--due", dayOffset(t, -3)); strings.Contains(se, "lint error") {
		t.Errorf("a re-leveled due-overdue must not be noted as an error:\n%s", se)
	}
}
