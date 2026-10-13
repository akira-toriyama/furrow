package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// `note --prepend` puts the paragraph right after the `# title` line, keeps
// the rest of the body, advances updated, and names the placement in the
// envelope (`prepended`, not `appended`).
func TestCLINotePrependWritesFirstAndReportsEffect(t *testing.T) {
	initStore(t)
	id := addTask(t, "note target")
	mustRun(t, "note", id, "older progress")

	out, code := run(t, "--json", "note", id, "--prepend", "決定: B 案")
	if code != 0 {
		t.Fatalf("note --prepend exit = %d:\n%s", code, out)
	}
	var env struct {
		Changed   []string `json:"changed"`
		Appended  *string  `json:"appended"`
		Prepended string   `json:"prepended"`
		After     struct {
			Updated string `json:"updated"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse note --prepend --json: %v\n%s", err, out)
	}
	if env.Prepended != "決定: B 案" || env.Appended != nil {
		t.Errorf("envelope must carry prepended (not appended):\n%s", out)
	}
	if env.After.Updated == "" {
		t.Errorf("after.updated should be present:\n%s", out)
	}

	// stdin (`-`) works with --prepend too, and the newest paragraph lands first.
	if out, code := runIn(t, "line A\nline B\n", "note", id, "-", "--prepend"); code != 0 {
		t.Fatalf("note - --prepend exit = %d:\n%s", code, out)
	}
	body := readBody(t, id)
	want := "# note target\n\nline A\nline B\n\n決定: B 案\n\nolder progress\n"
	if body != want {
		t.Errorf("body =\n%q\nwant\n%q", body, want)
	}

	// Human mode names the effect too.
	out, code = run(t, "note", id, "--prepend", "x")
	if code != 0 || !strings.Contains(out, "noted") {
		t.Errorf("human output: exit %d\n%s", code, out)
	}
	if _, code := run(t, "note", id, "--prepend", "  "); code != 2 {
		t.Errorf("an empty prepend must be exit 2, got %d", code)
	}
}

// Membership routes --prepend to the box exactly as it routes a plain note.
func TestCLINotePrependOnEpic(t *testing.T) {
	initStore(t)
	box := addEpic(t, "box", "-r", "o/r")
	mustRun(t, "note", box, "older")
	out, code := run(t, "--json", "note", box, "--prepend", "newest")
	if code != 0 {
		t.Fatalf("note --prepend on epic exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"prepended": "newest"`) && !strings.Contains(out, `"prepended":"newest"`) {
		t.Errorf("epic envelope must carry prepended:\n%s", out)
	}
	if body := readBody(t, box); body != "# box\n\nnewest\n\nolder\n" {
		t.Errorf("epic body = %q", body)
	}
}
