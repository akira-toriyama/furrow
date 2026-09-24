package cli

import (
	"strings"
	"testing"
)

// t-ga1k: the active-epic scope is `next`'s definition, but the root contract
// (a read never narrows silently) still holds for it — on the furrow-test
// board the scope hid four actionable tasks, one of them the best pick, and
// two independent sessions read the scoped rows as the whole actionable set.
// The count rides stderr; stdout stays the scoped rows.
func TestCLINextDisclosesActionableHiddenByFocus(t *testing.T) {
	initStore(t)
	focus := epicID(t, "focus box", "--repo", "o/r")
	other := epicID(t, "other box", "--repo", "o/r")
	mustRun(t, "epic", "activate", focus)
	in := addTask(t, "in focus", "-e", focus, "-s", "ready")
	out1 := addTask(t, "elsewhere one", "-e", other, "-s", "ready")
	out2 := addTask(t, "elsewhere two", "-e", other, "-s", "ready")
	addTask(t, "elsewhere but not actionable", "-e", other) // default lane: not in [next].lanes

	stdout, stderr, code := runSplit(t, "next")
	if code != 0 {
		t.Fatalf("next exit %d:\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, in) || strings.Contains(stdout, out1) || strings.Contains(stdout, out2) {
		t.Fatalf("stdout must stay the scoped rows:\n%s", stdout)
	}
	if !strings.Contains(stderr, "note: 2 actionable task(s) in other box(es) hidden by the active-epic scope — furrow next --all-epics") {
		t.Errorf("stderr must count what the focus hid and name the escape, got:\n%s", stderr)
	}

	// The escape itself hides nothing, so it says nothing; -n on top of it
	// still discloses its own cap through the ordinary note.
	stdout, stderr, code = runSplit(t, "next", "--all-epics")
	if code != 0 {
		t.Fatalf("next --all-epics exit %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, out1) || !strings.Contains(stdout, out2) {
		t.Errorf("--all-epics must list the hidden rows:\n%s", stdout)
	}
	if strings.Contains(stderr, "hidden by the active-epic scope") {
		t.Errorf("--all-epics hides nothing, so no note:\n%s", stderr)
	}

	// -e reads one box explicitly: the scope is off, the note with it.
	_, stderr, code = runSplit(t, "next", "-e", other)
	if code != 0 || strings.Contains(stderr, "hidden by the active-epic scope") {
		t.Errorf("-e bypasses the scope, so no note (exit %d):\n%s", code, stderr)
	}

	// A focus that hides nothing stays quiet — the classic read is byte-identical.
	mustRun(t, "move", out1, "done")
	mustRun(t, "move", out2, "done")
	_, stderr, _ = runSplit(t, "next")
	if strings.Contains(stderr, "hidden by the active-epic scope") {
		t.Errorf("nothing hidden, so no note:\n%s", stderr)
	}
}

// With nothing active the existing "deliberately empty" note carries the same
// count, so a session sees at once that the board has work and that a box
// must be opened to reach it.
func TestCLINextNoActiveEpicCountsHiddenWork(t *testing.T) {
	initStore(t)
	box := epicID(t, "closed for now", "--repo", "o/r")
	addTask(t, "waiting in the box", "-e", box, "-s", "ready")

	_, stderr, code := runSplit(t, "next")
	if code != 0 {
		t.Fatalf("next exit %d:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "deliberately empty (1 actionable in other box(es) hidden)") {
		t.Errorf("the no-focus note must carry the hidden count:\n%s", stderr)
	}
}
