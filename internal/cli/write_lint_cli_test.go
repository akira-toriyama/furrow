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
	if code != 0 || !strings.Contains(se, "note: this write raised lint error ready-blocked on "+waiter+": ") || !strings.Contains(se, blocker) {
		t.Errorf("set -s ready over an open dep: exit %d, want the ready-blocked note naming %s:\n%s", code, blocker, se)
	}
	// Touching a task that was already in error is not a new finding.
	if _, se, _ = runSplit(t, "value", waiter, "3"); strings.Contains(se, "lint error") {
		t.Errorf("a write on an already-blocked task must stay quiet:\n%s", se)
	}

	edge := addTask(t, "edge", "-s", "ready")
	if _, se, _ = runSplit(t, "dep", edge, blocker); !strings.Contains(se, "raised lint error ready-blocked on "+edge) {
		t.Errorf("a dep edge onto a ready task must name the error it creates:\n%s", se)
	}

	_, se, _ = runSplit(t, "add", "late", "--due", dayOffset(t, -3))
	if !strings.Contains(se, "raised lint error due-overdue on ") {
		t.Errorf("add --due in the past must name due-overdue:\n%s", se)
	}

	// The error can land on a task the command never named: reopening a done
	// dep puts the task that sat ready on it back in ready-blocked.
	dep := addTask(t, "dep")
	dependent := addTask(t, "dependent", "--dep", dep)
	mustRun(t, "done", dep)
	mustRun(t, "set", dependent, "-s", "ready")
	if _, se, _ = runSplit(t, "move", dep, "backlog"); !strings.Contains(se, "raised lint error ready-blocked on "+dependent) {
		t.Errorf("reopening a done dep must name the dependent it blocks:\n%s", se)
	}

	// Reads never note.
	if _, se, _ = runSplit(t, "ls"); strings.Contains(se, "lint error") {
		t.Errorf("a read must not note lint errors:\n%s", se)
	}
}

// The note follows the board's lint policy: an error the board re-levels to a
// warning or ignores is not an error to note, so the note never calls
// something an error that `furrow lint` does not. The ready-blocked note is the
// positive control — the hook is live under the same policy.
func TestWriteLintNoteFollowsTheBoardPolicy(t *testing.T) {
	freezeClock(t)
	initStore(t)
	mustRun(t, "config", "set", "lint.severity.due-overdue", "warn")
	if _, se, _ := runSplit(t, "add", "late", "--due", dayOffset(t, -3)); strings.Contains(se, "lint error") {
		t.Errorf("a re-leveled due-overdue must not be noted as an error:\n%s", se)
	}
	blocker := addTask(t, "blocker")
	waiter := addTask(t, "waiter", "--dep", blocker)
	if _, se, _ := runSplit(t, "set", waiter, "-s", "ready"); !strings.Contains(se, "raised lint error ready-blocked on "+waiter) {
		t.Errorf("an error the policy leaves alone is still noted:\n%s", se)
	}
	mustRun(t, "config", "set", "lint.ignore_codes", "ready-blocked")
	other := addTask(t, "other", "--dep", blocker)
	if _, se, _ := runSplit(t, "set", other, "-s", "ready"); strings.Contains(se, "lint error") {
		t.Errorf("an ignored code must not be noted:\n%s", se)
	}
}

// Board-level errors count too: the first repeating task on a shared board
// with no [due].timezone raises repeat-no-timezone, which lint files under the
// id `config`.
func TestWriteLintNoteCoversBoardRules(t *testing.T) {
	freezeClock(t)
	initStore(t)
	if _, se, _ := runSplit(t, "add", "weekly", "--due", dayOffset(t, 30), "--repeat", "weekly"); !strings.Contains(se, "raised lint error repeat-no-timezone on config") {
		t.Errorf("the first series on a zone-less shared board must name repeat-no-timezone:\n%s", se)
	}
	if _, se, _ := runSplit(t, "add", "daily", "--due", dayOffset(t, 30), "--repeat", "daily"); strings.Contains(se, "lint error") {
		t.Errorf("a second series leaves the same (code, id) and must stay quiet:\n%s", se)
	}
}

// A command that fails after one of its writes landed still names the error
// that write created; across several writes the "before" is the board as the
// command found it, so the first write's error is not lost.
func TestWriteLintNoteSurvivesAFailedCommand(t *testing.T) {
	freezeClock(t)
	initStore(t)
	blocker := addTask(t, "blocker")
	first := addTask(t, "first", "--dep", blocker)
	second := addTask(t, "second", "--dep", blocker)

	_, se, code := runSplitIn(t, "SetStatus-task: "+first+" ready\nSetStatus-task: "+second+" ready\n", "apply", "--on", "merge")
	if code != 0 || !strings.Contains(se, "raised lint error ready-blocked on "+first) || !strings.Contains(se, "raised lint error ready-blocked on "+second) {
		t.Errorf("two writes in one command: both errors named (exit %d):\n%s", code, se)
	}

	third := addTask(t, "third", "--dep", blocker)
	_, se, code = runSplitIn(t, "SetStatus-task: "+third+" ready\nSetStatus-task: t-nope0 ready\n", "apply", "--on", "merge")
	if code == 0 || !strings.Contains(se, "raised lint error ready-blocked on "+third) {
		t.Errorf("a failed command whose first write landed must still name its error (exit %d):\n%s", code, se)
	}
}
