package cli

import (
	"strings"
	"testing"
)

// The relocation a lane change performs on a priority tie reaches the operator
// as a stderr note, in human and --json mode alike, and the write's after row
// carries the new priority.
func TestMoveNotesRelocatedPriority(t *testing.T) {
	initStore(t)
	addTask(t, "A", "-s", "backlog")
	mover := addTask(t, "C", "-s", "ready", "-p", "100")
	_, stderr, code := runSplit(t, "move", mover, "backlog")
	if code != 0 {
		t.Fatalf("move exit = %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "note: priority 100 was already taken in lane \"backlog\"") {
		t.Fatalf("stderr = %q, want the relocation note", stderr)
	}
	stdout, stderr, code := runSplit(t, "--json", "add", "Y", "-s", "backlog", "-p", "100")
	if code != 0 {
		t.Fatalf("add exit = %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "note: priority 100 in lane \"backlog\" is now shared with") {
		t.Fatalf("stderr = %q, want the shared-priority note", stderr)
	}
	if strings.Contains(stdout, "note:") {
		t.Fatalf("stdout must stay pure JSON: %q", stdout)
	}
}
