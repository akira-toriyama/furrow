package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLINoteAppendsAndReportsEffect(t *testing.T) {
	initStore(t)
	id := addTask(t, "note target")

	// note via positional arg, machine mode: the envelope surfaces the appended
	// text (metadata `changed` is [] because only body + updated moved).
	out, code := run(t, "--json", "note", id, "検証完了。次: アダプタ選定。")
	if code != 0 {
		t.Fatalf("note exit = %d:\n%s", code, out)
	}
	var env struct {
		Changed  []string `json:"changed"`
		Appended string   `json:"appended"`
		After    struct {
			Updated string `json:"updated"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse note --json: %v\n%s", err, out)
	}
	if env.Appended != "検証完了。次: アダプタ選定。" {
		t.Errorf("appended = %q", env.Appended)
	}
	if env.After.Updated == "" {
		t.Errorf("after.updated should be present:\n%s", out)
	}

	// note via stdin (`-`): reads the whole of stdin as one note.
	out, code = runIn(t, "line A\nline B\n", "note", id, "-")
	if code != 0 {
		t.Fatalf("note - exit = %d:\n%s", code, out)
	}

	body := readBody(t, id)
	want := "# note target\n\n検証完了。次: アダプタ選定。\n\nline A\nline B\n"
	if body != want {
		t.Errorf("body =\n%q\nwant\n%q", body, want)
	}
}

func TestCLINoteErrors(t *testing.T) {
	initStore(t)
	id := addTask(t, "task")

	if _, code := run(t, "note", id, "   "); code != 2 {
		t.Errorf("empty note want exit 2, got %d", code)
	}
	if _, code := run(t, "note", "t-nope0", "x"); code != 1 {
		t.Errorf("unknown id want exit 1, got %d", code)
	}
}

// readBody reads a task's body file straight from the store the CLI wrote to
// (initStore points FURROW_DIR at that store).
func readBody(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("FURROW_DIR"), "bodies", id+".md"))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
