package cli

import (
	"strings"
	"testing"
	"time"
)

// t-kvsa: brief's due band is board-wide while its next band obeys the
// active-epic scope, and a drill session read the two as one scope when a
// due-today task appeared above and not below. On a board with boxes the
// due header now says so; a board with no boxes has no focus to differ from.
func TestCLIBriefDueBandNamesItsScopeOnABoardWithBoxes(t *testing.T) {
	initStore(t)
	today := time.Now().Format("2006-01-02")
	addTask(t, "dated, no boxes yet", "-s", "ready", "-r", "o/r", "--due", today)
	out, code := run(t, "brief")
	if code != 0 || !strings.Contains(out, "due (1):") {
		t.Fatalf("no boxes: the plain header (exit %d):\n%s", code, out)
	}

	focus := epicID(t, "focus", "--repo", "o/r")
	other := epicID(t, "elsewhere", "--repo", "o/r")
	mustRun(t, "epic", "activate", focus)
	outside := addTask(t, "due today, outside the focus", "-s", "ready", "-e", other, "--due", today)
	out, code = run(t, "brief")
	if code != 0 {
		t.Fatalf("brief exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "due (2, every epic):") {
		t.Errorf("with boxes the due header must name its scope:\n%s", out)
	}
	due, next := strings.Index(out, "due ("), strings.Index(out, "next (")
	if !strings.Contains(out[due:next], outside) || strings.Contains(out[next:], outside) {
		t.Errorf("the task outside the focus belongs in the due band and not in next:\n%s", out)
	}
}

// t-b8dg: the header alone did not settle it — a later drill still took a
// due-band pick, found it missing from next, and re-ran `next -n 0` to learn
// why. A due row next cannot hand out now names the box that keeps it out;
// rows next can hand out (the focus, a pinned box, the unfiled pile while a box
// is active) stay unmarked, and with nothing active every non-pinned row is out.
func TestCLIBriefDueRowNamesTheBoxNextLeavesOut(t *testing.T) {
	freezeClock(t)
	initStore(t)
	focus := epicID(t, "focus", "--repo", "o/r")
	other := epicID(t, "elsewhere", "--repo", "o/r")
	channel := epicID(t, "channel", "--repo", "o/r")
	mustRun(t, "epic", "activate", focus)
	mustRun(t, "epic", "set", channel, "--pinned")
	late := dayOffset(t, -2)
	inFocus := addTask(t, "in the focus", "-s", "ready", "-r", "o/r", "-e", focus, "--due", late)
	outside := addTask(t, "in another box", "-s", "ready", "-r", "o/r", "-e", other, "--due", late)
	pinned := addTask(t, "in the pinned box", "-s", "ready", "-r", "o/r", "-e", channel, "--due", late)
	unfiled := addTask(t, "unfiled", "-s", "ready", "-r", "o/r", "-e", "", "--due", late)

	row := dueRow(t)
	out := mustRun(t, "brief")
	if r := row(out, outside); !strings.HasSuffix(r, "outside next: box "+other) {
		t.Errorf("a row in a box next leaves out must name it:\n%s", r)
	}
	for _, id := range []string{inFocus, pinned, unfiled} {
		if r := row(out, id); strings.Contains(r, "outside next") {
			t.Errorf("a row next can hand out must stay unmarked:\n%s", r)
		}
	}

	mustRun(t, "epic", "deactivate", focus)
	out = mustRun(t, "brief")
	if r := row(out, inFocus); !strings.HasSuffix(r, "outside next: box "+focus) {
		t.Errorf("with nothing active, a filed row is outside next:\n%s", r)
	}
	if r := row(out, unfiled); !strings.HasSuffix(r, "outside next: unfiled") {
		t.Errorf("with nothing active, the unfiled pile is outside next too:\n%s", r)
	}
	if r := row(out, pinned); strings.Contains(r, "outside next") {
		t.Errorf("a pinned box passes through even with nothing active:\n%s", r)
	}
}

// dueRow finds a task's row in brief's due band (overdue or today).
func dueRow(t *testing.T) func(out, id string) string {
	return func(out, id string) string {
		t.Helper()
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, id) && (strings.Contains(l, "(overdue ") || strings.Contains(l, "(today ")) {
				return l
			}
		}
		t.Fatalf("no due row for %s:\n%s", id, out)
		return ""
	}
}

// The due band ignores the board's automatic repo scope too, while next obeys
// it: a row from another repo, or a draft, is out of next whatever its box —
// so the row names the repo first, and naming its box (even an active one)
// would send the reader to a box that does not bring it in. The box is
// judged by the scope next applies for THIS repo: a box active only for
// another repo still keeps a task of this repo out. Today rows carry the mark
// as overdue rows do.
func TestCLIBriefDueRowNamesTheRepoBeforeTheBox(t *testing.T) {
	freezeClock(t)
	initStore(t)
	boxA := epicID(t, "a's focus", "--repo", "o/a")
	boxB := epicID(t, "b's focus", "--repo", "o/b")
	mustRun(t, "epic", "activate", boxA)
	mustRun(t, "epic", "activate", boxB)
	late, today := dayOffset(t, -2), dayOffset(t, 0)
	mine := addTask(t, "mine", "-s", "ready", "-r", "o/a", "-e", boxA, "--due", late)
	theirs := addTask(t, "theirs, in b's ACTIVE box", "-s", "ready", "-r", "o/b", "-e", boxB, "--due", late)
	draft := addTask(t, "a dated draft", "-s", "ready", "--draft", "-e", boxA, "--due", today)
	crossed := addTask(t, "this repo, b's box", "-s", "ready", "-r", "o/a", "-e", boxB, "--due", today)
	// Scoped only now: with a default_repo set first, add would union o/a into
	// every task's repos.
	mustRun(t, "config", "set", "default_repo", "o/a")

	row := dueRow(t)
	out := mustRun(t, "brief")
	if r := row(out, mine); strings.Contains(r, "outside next") {
		t.Errorf("a row next lists must stay unmarked:\n%s", r)
	}
	if r := row(out, theirs); !strings.HasSuffix(r, "outside next: repo o/b") {
		t.Errorf("another repo's row names the repo, not its (active) box:\n%s", r)
	}
	if r := row(out, draft); !strings.HasSuffix(r, "outside next: draft") {
		t.Errorf("a draft is outside a repo-scoped next:\n%s", r)
	}
	if r := row(out, crossed); !strings.HasSuffix(r, "outside next: box "+boxB) {
		t.Errorf("a box active only for another repo keeps this repo's task out:\n%s", r)
	}
}
