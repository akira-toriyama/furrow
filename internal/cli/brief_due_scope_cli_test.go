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
