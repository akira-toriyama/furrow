package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIDoneNoteClosesAndReportsAppended(t *testing.T) {
	initStore(t)
	id := addTask(t, "close me")

	out, code := run(t, "--json", "done", id, "--note", "→ continued in t-xxxxx")
	if code != 0 {
		t.Fatalf("done --note exit = %d:\n%s", code, out)
	}
	var env struct {
		Changed  []string `json:"changed"`
		Appended string   `json:"appended"`
		After    struct {
			Status string  `json:"status"`
			Closed *string `json:"closed"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse done --note --json: %v\n%s", err, out)
	}
	if env.Appended != "→ continued in t-xxxxx" {
		t.Errorf("appended = %q", env.Appended)
	}
	if env.After.Status != "done" || env.After.Closed == nil {
		t.Errorf("after = %q/closed %v; want done lane with closed stamped:\n%s", env.After.Status, env.After.Closed, out)
	}
	if join := strings.Join(env.Changed, ","); !strings.Contains(join, "status") {
		t.Errorf("changed = %q, want status among the moved fields", join)
	}

	body := readBody(t, id)
	if want := "# close me\n\n→ continued in t-xxxxx\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestCLIDoneNoteStdin(t *testing.T) {
	initStore(t)
	id := addTask(t, "stdin close")

	// `--note -` reads the note from stdin, the note command's convention — the
	// same spelling must not silently mean a literal "-" here.
	out, code := runIn(t, "line A\nline B\n", "done", id, "--note", "-")
	if code != 0 {
		t.Fatalf("done --note - exit = %d:\n%s", code, out)
	}
	body := readBody(t, id)
	if want := "# stdin close\n\nline A\nline B\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestCLIDoneNoteBatch(t *testing.T) {
	initStore(t)
	a := addTask(t, "one")
	b := addTask(t, "two")

	out, code := run(t, "--json", "done", a, b, "--note", "shipped in furrow#152")
	if code != 0 {
		t.Fatalf("batch done --note exit = %d:\n%s", code, out)
	}
	var envs []struct {
		Appended string `json:"appended"`
		After    struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil {
		t.Fatalf("parse batch --json (want an array of envelopes): %v\n%s", err, out)
	}
	if len(envs) != 2 || envs[0].After.ID != a || envs[1].After.ID != b {
		t.Fatalf("envelopes = %+v; want both tasks in input order", envs)
	}
	for _, e := range envs {
		if e.After.Status != "done" || e.Appended != "shipped in furrow#152" {
			t.Errorf("%s: status %q appended %q", e.After.ID, e.After.Status, e.Appended)
		}
	}
	for _, id := range []string{a, b} {
		if body := readBody(t, id); !strings.Contains(body, "\n\nshipped in furrow#152\n") {
			t.Errorf("%s body missing the note:\n%q", id, body)
		}
	}
}

func TestCLIDoneNoteErrors(t *testing.T) {
	initStore(t)
	id := addTask(t, "task")

	// An empty note is bad usage, never a silent plain close.
	if _, code := run(t, "done", id, "--note", "   "); code != 2 {
		t.Errorf("empty note want exit 2, got %d", code)
	}
	if out, _ := run(t, "show", id); strings.Contains(out, "status:   done") {
		t.Errorf("rejected note must not close the task:\n%s", out)
	}

	// A batch miss closes nothing and appends nothing (all-or-nothing).
	if _, code := run(t, "done", id, "t-nope0", "--note", "x"); code != 1 {
		t.Errorf("batch miss want exit 1, got %d", code)
	}
	if body := readBody(t, id); strings.Contains(body, "x") {
		t.Errorf("failed batch appended the note:\n%q", body)
	}
}
