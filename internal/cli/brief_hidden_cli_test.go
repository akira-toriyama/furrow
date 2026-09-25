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

// t-fsvt: the lane tally above says WHICH LANE the cap dropped from, not
// WHICH ROW — and the row a session most needs to place is the overdue task
// the due band just flagged. Four drills in one run re-ran `next` to learn
// whether "1 hidden: 1 ready" was that task. The tally now carries the ids,
// per lane and in canonical order: `ids` on each next_hidden row, and one …
// row per lane under the human band (the header stays the lane tally, so a
// wide board cannot stretch it).
func TestCLIBriefNamesTheIdsTheCapHid(t *testing.T) {
	initStore(t)
	addTask(t, "first pick", "-s", "ready", "-r", "o/r")
	late := addTask(t, "promised and late", "-s", "ready", "-r", "o/r", "--due", "2020-01-01")
	third := addTask(t, "third in line", "-s", "ready", "-r", "o/r")
	wip := addTask(t, "already open", "-s", "in-progress", "-r", "o/r")

	out, code := run(t, "--json", "brief", "-n", "1")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	var b struct {
		NextHidden []struct {
			Lane  string   `json:"lane"`
			Count int      `json:"count"`
			IDs   []string `json:"ids"`
		} `json:"next_hidden"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	// Within a lane the ids keep canonical order: `late` precedes `third`
	// because it was added first, exactly as `next` would list them past the
	// fold.
	if len(b.NextHidden) != 2 ||
		b.NextHidden[0].Lane != "ready" || b.NextHidden[0].Count != 2 || strings.Join(b.NextHidden[0].IDs, ",") != late+","+third ||
		b.NextHidden[1].Lane != "in-progress" || b.NextHidden[1].Count != 1 || strings.Join(b.NextHidden[1].IDs, ",") != wip {
		t.Errorf("next_hidden = %+v, want the ids beside each lane's count, in canonical order: ready [%s %s], in-progress [%s]", b.NextHidden, late, third, wip)
	}

	human, code := run(t, "brief", "-n", "1")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, human)
	}
	// The due band names the late task; the next header must name it too, so
	// the reader can tell "the overdue one is below the fold" without a second
	// read.
	if !strings.Contains(human, "  ! "+late) {
		t.Fatalf("the due band must flag the late task:\n%s", human)
	}
	if !strings.Contains(human, "next (1/4 — 3 hidden by -n: 2 ready, 1 in-progress):") {
		t.Fatalf("the band header keeps the lane tally:\n%s", human)
	}
	for _, want := range []string{
		"  … ready: " + late + ", " + third + "\n",
		"  … in-progress: " + wip + "\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("the band must list the hidden ids per lane, in canonical order:\nwant %q in\n%s", want, human)
		}
	}
	// The rows sit under the picks and above the blocked band: the band's
	// contents, not a footnote.
	star, hidden, blocked := strings.Index(human, "  ★ "), strings.Index(human, "  … "), strings.Index(human, "blocked (")
	if star >= hidden || hidden >= blocked {
		t.Errorf("hidden rows belong under the picks and inside the next band:\n%s", human)
	}
}
