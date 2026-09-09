package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The waiting state reaches every box surface: epic ls / epic show / brief's
// focus line, JSON as `waiting: {until, task}` and the human rows as
// `⏳ waiting until <due> (<task>)` — and revisit's epic_all_done stays quiet.
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
	if _, ok := b.Revisit["epic_all_done"]; ok {
		t.Errorf("a waiting box must not raise epic_all_done:\n%s", out)
	}
}
