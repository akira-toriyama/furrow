package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// t-xzxm: brief's -n cap follows canonical order, whose lane order puts
// in-progress after ready — so with -n or more ready tasks the one task a
// session already started is ALWAYS the row that falls off the band. "3/5"
// disclosed the count; it did not say the hidden two held the board's only
// in-progress task, and a drill session that stopped at brief would have
// started a second one. The band now tallies what it dropped, per lane.
func TestCLIBriefNamesTheLanesTheCapHid(t *testing.T) {
	initStore(t)
	addTask(t, "ready a", "-s", "ready", "-r", "o/r")
	addTask(t, "ready b", "-s", "ready", "-r", "o/r")
	addTask(t, "wip task", "-s", "in-progress", "-r", "o/r")

	out, code := run(t, "--json", "brief", "-n", "1")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	var b struct {
		Next []struct {
			ID string `json:"id"`
		} `json:"next"`
		NextTotal  int `json:"next_total"`
		NextHidden []struct {
			Lane  string `json:"lane"`
			Count int    `json:"count"`
		} `json:"next_hidden"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	if len(b.Next) != 1 || b.NextTotal != 3 {
		t.Fatalf("next = %+v total %d, want the -n 1 cap over 3 actionable", b.Next, b.NextTotal)
	}
	// Lane order, not alphabetical: ready before in-progress as [lanes].order has it.
	if len(b.NextHidden) != 2 ||
		b.NextHidden[0].Lane != "ready" || b.NextHidden[0].Count != 1 ||
		b.NextHidden[1].Lane != "in-progress" || b.NextHidden[1].Count != 1 {
		t.Errorf("next_hidden = %+v, want [{ready 1} {in-progress 1}] in lane order", b.NextHidden)
	}

	human, code := run(t, "brief", "-n", "1")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, human)
	}
	if !strings.Contains(human, "next (1/3 — 2 hidden by -n: 1 ready, 1 in-progress):") {
		t.Errorf("the band header must name the hidden lanes:\n%s", human)
	}

	// An uncapped read hides nothing: the key stays off the object and the
	// header keeps its plain shape, so a board the cap never bites is unchanged.
	out, _ = run(t, "--json", "brief", "-n", "0")
	if strings.Contains(out, "next_hidden") {
		t.Errorf("next_hidden must be omitted when the cap did not bite:\n%s", out)
	}
	human, _ = run(t, "brief", "-n", "0")
	if !strings.Contains(human, "next (3/3):") {
		t.Errorf("an uncapped header keeps its plain shape:\n%s", human)
	}
}
