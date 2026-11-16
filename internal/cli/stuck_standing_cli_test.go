package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Two boxes of the same shape — one open member, none actionable — differing
// only in the standing declaration. Every read that says "stuck" must agree
// with revisit about both: the plain box is stuck everywhere, the standing one
// nowhere.
func TestCLIStandingBoxIsStuckInNoRead(t *testing.T) {
	initStore(t)
	plain := addEpic(t, "plain box")
	addTask(t, "parked member", "-s", "backlog", "-e", plain)
	standing := addEpic(t, "standing box")
	addTask(t, "awaiting triage", "-s", "backlog", "-e", standing)
	if out, code := run(t, "epic", "set", standing, "--standing", "--pinned"); code != 0 {
		t.Fatalf("epic set --standing --pinned: exit %d:\n%s", code, out)
	}

	type flagged struct {
		ID    string `json:"id"`
		Stuck *bool  `json:"stuck"`
	}
	check := func(read, id string, got *bool) {
		t.Helper()
		if got == nil {
			t.Errorf("%s: %s carries no `stuck` key", read, id)
			return
		}
		if want := id == plain; *got != want {
			t.Errorf("%s: %s stuck = %v, want %v", read, id, *got, want)
		}
	}
	mustJSON := func(v any, args ...string) {
		t.Helper()
		out, code := run(t, append([]string{"--json"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d:\n%s", args, code, out)
		}
		if err := json.Unmarshal([]byte(out), v); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	var rows []flagged
	mustJSON(&rows, "epic", "ls")
	if len(rows) != 2 {
		t.Fatalf("epic ls: want both boxes, got %+v", rows)
	}
	for _, r := range rows {
		check("epic ls", r.ID, r.Stuck)
	}

	for _, id := range []string{plain, standing} {
		var d flagged
		mustJSON(&d, "epic", "show", id)
		check("epic show", id, d.Stuck)

		var viaShow []flagged
		mustJSON(&viaShow, "show", id)
		if len(viaShow) != 1 {
			t.Fatalf("show %s: want one element, got %+v", id, viaShow)
		}
		check("show <epic>", id, viaShow[0].Stuck)
	}

	var groups []struct {
		Epic  *struct{ ID string } `json:"epic"`
		Stuck *bool                `json:"stuck"`
	}
	mustJSON(&groups, "ls", "--tree")
	seen := 0
	for _, g := range groups {
		if g.Epic == nil {
			continue
		}
		seen++
		check("ls --tree", g.Epic.ID, g.Stuck)
	}
	if seen != 2 {
		t.Fatalf("ls --tree: want both boxes' groups, got %d", seen)
	}

	var boxes []flagged
	mustJSON(&boxes, "revisit", "--boxes")
	if len(boxes) != 1 || boxes[0].ID != plain {
		t.Errorf("revisit --boxes: want only %s, got %+v", plain, boxes)
	}
	// brief carries the flag twice: revisit's summary, and the pinned band's
	// rows (a mandate box is standing + pinned, the shape that showed it).
	var brief struct {
		Pinned  []flagged `json:"pinned"`
		Revisit struct {
			EpicStuck []string `json:"epic_stuck"`
		} `json:"revisit"`
	}
	mustJSON(&brief, "brief")
	if len(brief.Revisit.EpicStuck) != 1 || brief.Revisit.EpicStuck[0] != plain {
		t.Errorf("brief: revisit.epic_stuck = %v, want only %s", brief.Revisit.EpicStuck, plain)
	}
	if len(brief.Pinned) != 1 || brief.Pinned[0].ID != standing {
		t.Fatalf("brief: want the standing box in the pinned band, got %+v", brief.Pinned)
	}
	check("brief pinned", standing, brief.Pinned[0].Stuck)

	// The human renderings: the marker rides the plain box's line and no line
	// of the standing box's.
	for _, args := range [][]string{{"epic", "ls"}, {"ls", "--tree"}} {
		out, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d:\n%s", args, code, out)
		}
		for _, line := range strings.Split(out, "\n") {
			marked := strings.Contains(line, "⚠ stuck")
			switch {
			case strings.Contains(line, plain) && !marked:
				t.Errorf("%v: the plain box's line lost its marker: %q", args, line)
			case strings.Contains(line, standing) && marked:
				t.Errorf("%v: the standing box's line is marked stuck: %q", args, line)
			}
		}
	}
	for _, id := range []string{plain, standing} {
		out, code := run(t, "epic", "show", id)
		if code != 0 {
			t.Fatalf("epic show %s: exit %d:\n%s", id, code, out)
		}
		if got, want := strings.Contains(out, "⚠ stuck"), id == plain; got != want {
			t.Errorf("epic show %s: stuck marker present = %v, want %v:\n%s", id, got, want, out)
		}
	}
}

// The exemption is about the flag, not about explaining an empty read: a
// standing box that is the active focus hands `next` nothing just the same, so
// the note that says why still prints — without calling the box stuck.
func TestCLINextExplainsAnEmptyStandingFocus(t *testing.T) {
	for _, standing := range []bool{false, true} {
		initStore(t)
		box := addEpic(t, "the focus", "-r", "o/r")
		addTask(t, "not yet ready", "-s", "backlog", "-e", box, "-r", "o/r")
		if standing {
			mustRun(t, "epic", "set", box, "--standing")
		}
		mustRun(t, "epic", "activate", box)

		_, stderr, code := runSplit(t, "next", "-r", "o/r")
		if code != 0 {
			t.Fatalf("standing=%v: next exit %d:\n%s", standing, code, stderr)
		}
		want := "note: focus " + box + ` "the focus" is stuck — it has open tasks but none are actionable`
		if standing {
			want = "note: focus " + box + ` "the focus" has open tasks but none are actionable`
		}
		if !strings.Contains(stderr, want) {
			t.Errorf("standing=%v: next must say why the focus is empty; want %q in:\n%s", standing, want, stderr)
		}
	}
}
