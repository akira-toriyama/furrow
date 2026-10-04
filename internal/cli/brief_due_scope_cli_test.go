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

	row := func(out, id string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, id) && strings.Contains(l, "(overdue ") {
				return l
			}
		}
		t.Fatalf("no due row for %s:\n%s", id, out)
		return ""
	}
	out := mustRun(t, "brief")
	if r := row(out, outside); !strings.HasSuffix(r, "outside next: "+other) {
		t.Errorf("a row in a box next leaves out must name it:\n%s", r)
	}
	for _, id := range []string{inFocus, pinned, unfiled} {
		if r := row(out, id); strings.Contains(r, "outside next") {
			t.Errorf("a row next can hand out must stay unmarked:\n%s", r)
		}
	}

	mustRun(t, "epic", "deactivate", focus)
	out = mustRun(t, "brief")
	if r := row(out, inFocus); !strings.HasSuffix(r, "outside next: "+focus) {
		t.Errorf("with nothing active, a filed row is outside next:\n%s", r)
	}
	if r := row(out, unfiled); !strings.HasSuffix(r, "outside next: unfiled") {
		t.Errorf("with nothing active, the unfiled pile is outside next too:\n%s", r)
	}
	if r := row(out, pinned); strings.Contains(r, "outside next") {
		t.Errorf("a pinned box passes through even with nothing active:\n%s", r)
	}
}
