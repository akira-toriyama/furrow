package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
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

// One command, either entity: an `e-` id routes to the box, whose body lives in
// the SAME bodies/ dir, and whose shard is the one that stamps updated. The
// envelope keeps the task path's shape (`appended` beside {before,after,changed})
// so a caller reads one thing whichever id it passed.
func TestCLINoteOnEpicWritesTheBoxBodyAndEnvelope(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "-r", "o/r")

	out, code := run(t, "--json", "note", epic, "方針: pinned に寄せる。")
	if code != 0 {
		t.Fatalf("note on epic exit = %d:\n%s", code, out)
	}
	var env struct {
		Changed  []string `json:"changed"`
		Appended string   `json:"appended"`
		After    struct {
			ID      string `json:"id"`
			Updated string `json:"updated"`
			// A box has no lane/priority: the epic envelope, not the task one.
			Goal string `json:"goal"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse note --json: %v\n%s", err, out)
	}
	if env.After.ID != epic {
		t.Errorf("after.id = %q, want the epic %q", env.After.ID, epic)
	}
	if env.Appended != "方針: pinned に寄せる。" {
		t.Errorf("appended = %q", env.Appended)
	}
	if len(env.Changed) != 0 {
		t.Errorf("changed tracks metadata only, want [], got %v", env.Changed)
	}
	if env.After.Updated == "" {
		t.Errorf("after.updated should be present:\n%s", out)
	}

	// Human mode carries the verb, exactly like a task's note.
	out, code = run(t, "note", epic, "止まった所: dispatch まで。")
	if code != 0 {
		t.Fatalf("note on epic (human) exit = %d:\n%s", code, out)
	}
	if want := "noted " + epic + "  a box\n"; out != want {
		t.Errorf("human line = %q, want %q", out, want)
	}

	// `-` reads stdin here too — one convention across both entities.
	if out, code := runIn(t, "line A\nline B\n", "note", epic, "-"); code != 0 {
		t.Fatalf("note - on epic exit = %d:\n%s", code, out)
	}

	body := readBody(t, epic)
	want := "# a box\n\n方針: pinned に寄せる。\n\n止まった所: dispatch まで。\n\nline A\nline B\n"
	if body != want {
		t.Errorf("body =\n%q\nwant\n%q", body, want)
	}
}

func TestCLINoteOnEpicErrors(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "-r", "o/r")

	if _, code := run(t, "note", epic, "   "); code != 2 {
		t.Errorf("empty note want exit 2, got %d", code)
	}
	// An unknown BOX is the epic resolver's exit 2 + candidates, not the task
	// path's exit 1 — the divergence the command's help documents.
	fe, _ := runErr(t, "note", "e-nope0", "x")
	if fe == nil || int(fe.Code) != 2 {
		t.Fatalf("unknown box want exit 2, got %v", fe)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown box should carry candidates: %+v", fe)
	}
	// A ref naming NOTHING in either store falls to the side its shape suggests:
	// no epic prefix, so the task path's exit 1.
	if _, code := run(t, "note", "nothing like this", "x"); code != 1 {
		t.Errorf("unresolvable non-id ref want the task path's exit 1, got %d", code)
	}
}

// Routing is by MEMBERSHIP, not by the id's prefix. The regression: [ids]
// accepts an epic_prefix that extends the task prefix, and ids are prefix +
// random base32, so on such a board some real TASK ids are shaped like epic
// ids. A prefix guess sent those to the box path and `furrow note` stopped
// working on an ordinary task.
func TestCLINoteRoutesByMembershipNotPrefix(t *testing.T) {
	dir := t.TempDir()
	if _, err := app.Init(dir); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, app.DirName)
	t.Setenv(app.EnvDir, store)
	cfgPath := filepath.Join(store, "config.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// epic_prefix "t-e" extends the task prefix "t-": both patterns match an id
	// like t-e….
	patched := strings.Replace(string(cfg), `epic_prefix = "e-"`, `epic_prefix = "t-e"`, 1)
	if patched == string(cfg) {
		t.Fatal("config template no longer carries the epic_prefix line this test patches")
	}
	if err := os.WriteFile(cfgPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mint ids until one task lands in the overlapping shape (~1 in 32).
	var overlapping string
	for i := 0; i < 400 && overlapping == ""; i++ {
		if id := addTask(t, "task"); strings.HasPrefix(id, "t-e") {
			overlapping = id
		}
	}
	if overlapping == "" {
		t.Skip("no task id landed in the overlapping shape")
	}
	out, code := run(t, "note", overlapping, "progress")
	if code != 0 {
		t.Fatalf("note on the task %s exit = %d (a real task must never route to the box path):\n%s", overlapping, code, out)
	}
	if !strings.Contains(readBody(t, overlapping), "progress") {
		t.Errorf("the note did not land in %s's body", overlapping)
	}
}

// readBody reads a task's or epic's body file straight from the store the CLI
// wrote to (initStore points FURROW_DIR at that store; the two entities share
// the bodies/ directory).
func readBody(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("FURROW_DIR"), "bodies", id+".md"))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
