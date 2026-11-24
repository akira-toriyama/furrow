package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The waiting state reaches every box surface: epic ls / epic show / brief's
// focus line, JSON as `waiting: {until, task}` and the human rows as
// `⏳ waiting until <due> (<task>)` — and revisit raises no member signal on
// the box, active as it is.
func TestCLIEpicWaitingUntil(t *testing.T) {
	initStore(t)
	run(t, "epic", "add", "feather sku", "-r", "o/r")
	done := addTask(t, "ship it", "-s", "ready", "-r", "o/r", "-e", "feather")
	run(t, "done", done)
	watch := addTask(t, "confirm zero incidents", "-s", "waiting", "-r", "o/r", "-e", "feather", "--due", "2126-10-01")
	if _, code := run(t, "epic", "activate", "feather"); code != 0 {
		t.Fatal("activate")
	}

	out, code := run(t, "--json", "epic", "ls")
	if code != 0 {
		t.Fatalf("epic ls exit = %d:\n%s", code, out)
	}
	var rows []struct {
		Waiting *struct {
			Until string `json:"until"`
			Task  string `json:"task"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse epic ls --json: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Waiting == nil || rows[0].Waiting.Task != watch || !strings.HasPrefix(rows[0].Waiting.Until, "2126-10-01T") {
		t.Fatalf("epic ls --json must carry waiting {until, task}:\n%s", out)
	}

	for _, args := range [][]string{{"epic", "ls"}, {"epic", "show", "feather"}, {"brief"}} {
		out, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v exit = %d:\n%s", args, code, out)
		}
		if !strings.Contains(out, "⏳ waiting until 2126-10-01") || !strings.Contains(out, "("+watch+")") {
			t.Errorf("%v must print the wait with its carrier:\n%s", args, out)
		}
	}

	out, _ = run(t, "--json", "epic", "show", "feather")
	var d struct {
		Waiting *struct {
			Task string `json:"task"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.Waiting == nil || d.Waiting.Task != watch {
		t.Errorf("epic show --json must carry waiting (err %v):\n%s", err, out)
	}

	out, _ = run(t, "--json", "brief")
	var b struct {
		Revisit map[string]any `json:"revisit"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	for _, key := range []string{"epic_all_done", "epic_stuck"} {
		if _, ok := b.Revisit[key]; ok {
			t.Errorf("a waiting box must not raise %s:\n%s", key, out)
		}
	}
}

// stuck's second shape — a box next reads with no open member left and the
// rest parked outside the done lane — says so in `epic show` and in revisit's
// detail, instead of "all N members done".
func TestCLIStuckOnParkedLeftovers(t *testing.T) {
	initStore(t)
	box := addEpic(t, "leftovers", "-r", "o/r")
	mustRun(t, "done", addTask(t, "shipped", "-s", "ready", "-r", "o/r", "-e", box))
	addTask(t, "shelved", "-s", "icebox", "-r", "o/r", "-e", box)

	// Not read by next: no signal, no flag.
	if out := mustRun(t, "revisit", "--boxes"); strings.Contains(out, box) {
		t.Errorf("a box next does not read must raise no signal:\n%s", out)
	}
	if out := mustRun(t, "epic", "show", box, "--no-body"); strings.Contains(out, "⚠ stuck") {
		t.Errorf("a box next does not read must not be marked stuck:\n%s", out)
	}

	mustRun(t, "epic", "activate", box)
	out := mustRun(t, "revisit", "--boxes")
	if want := "← epic_stuck: no open members, and 1 of 2 parked rather than done"; !strings.Contains(out, want) {
		t.Errorf("revisit --boxes: want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "epic_all_done") {
		t.Errorf("a member parked outside the done lane is not done:\n%s", out)
	}
	out = mustRun(t, "epic", "show", box, "--no-body")
	if want := "⚠ stuck — no open members, the rest parked rather than done"; !strings.Contains(out, want) {
		t.Errorf("epic show: want %q in:\n%s", want, out)
	}
}
