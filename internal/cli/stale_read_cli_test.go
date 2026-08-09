package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// shownUpdated reads a task's current `updated` stamp the way a caller of the
// stale-read guard would: off a read's --json view.
func shownUpdated(t *testing.T, id string) string {
	t.Helper()
	out, code := run(t, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	var items []struct {
		Updated time.Time `json:"updated"`
	}
	if err := json.Unmarshal([]byte(out), &items); err != nil || len(items) != 1 {
		t.Fatalf("parse show --json (%v):\n%s", err, out)
	}
	return items[0].Updated.UTC().Format(time.RFC3339)
}

// A write guarded with the stamp the caller actually read is clean: no
// stale_read key, no stderr note — the key must not appear on the common path.
func TestCLIExpectUpdatedCleanWrite(t *testing.T) {
	initStore(t)
	id := addTask(t, "guarded")
	ts := shownUpdated(t, id)

	stdout, stderr, code := runSplit(t, "--json", "note", id, "progress", "--expect-updated", ts)
	if code != 0 {
		t.Fatalf("note exit = %d:\n%s\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "stale_read") {
		t.Errorf("clean write must not carry stale_read:\n%s", stdout)
	}
	if strings.Contains(stderr, "changed since your read") {
		t.Errorf("clean write must not warn:\n%s", stderr)
	}
}

// A +09:00 spelling of the same instant is the same read: instants are
// compared, not strings.
func TestCLIExpectUpdatedComparesInstants(t *testing.T) {
	initStore(t)
	id := addTask(t, "zoned")
	ts, err := time.Parse(time.RFC3339, shownUpdated(t, id))
	if err != nil {
		t.Fatal(err)
	}
	local := ts.In(time.FixedZone("JST", 9*3600)).Format(time.RFC3339)

	stdout, stderr, code := runSplit(t, "--json", "retitle", id, "still", "the", "same", "read", "--expect-updated", local)
	if code != 0 {
		t.Fatalf("retitle exit = %d:\n%s\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "stale_read") || strings.Contains(stderr, "changed since") {
		t.Errorf("same instant in another zone must be clean:\n%s\n%s", stdout, stderr)
	}
}

// A stamp older than the task's current `updated` means someone wrote in
// between: the mutation still goes through, and the envelope + stderr say so.
func TestCLIExpectUpdatedStaleWarnsAndWrites(t *testing.T) {
	initStore(t)
	id := addTask(t, "raced")

	stdout, stderr, code := runSplit(t, "--json", "note", id, "second session's edit", "--expect-updated", "2020-01-01T00:00:00Z")
	if code != 0 {
		t.Fatalf("guarded note must still write (warning, not refusal), exit = %d:\n%s\n%s", code, stdout, stderr)
	}
	var env struct {
		Appended  string `json:"appended"`
		StaleRead struct {
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
		} `json:"stale_read"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, stdout)
	}
	if env.Appended != "second session's edit" {
		t.Errorf("the write must have gone through, appended = %q", env.Appended)
	}
	if env.StaleRead.Expected != "2020-01-01T00:00:00Z" || env.StaleRead.Actual == "" {
		t.Errorf("stale_read = %+v, want expected echoed and actual filled", env.StaleRead)
	}
	if !strings.Contains(stderr, id+" changed since your read") {
		t.Errorf("missing stderr note:\n%s", stderr)
	}
}

// The guard rides set's one-element-array path too: the single envelope
// carries stale_read.
func TestCLIExpectUpdatedOnSetSingleID(t *testing.T) {
	initStore(t)
	id := addTask(t, "set target")

	stdout, _, code := runSplit(t, "--json", "set", id, "-s", "ready", "--expect-updated", "2020-01-01T00:00:00Z")
	if code != 0 {
		t.Fatalf("set exit = %d:\n%s", code, stdout)
	}
	var envs []struct {
		StaleRead *struct{} `json:"stale_read"`
	}
	if err := json.Unmarshal([]byte(stdout), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("set --json must stay a one-element array (%v):\n%s", err, stdout)
	}
	if envs[0].StaleRead == nil {
		t.Errorf("the one envelope must carry stale_read:\n%s", stdout)
	}
}

// One timestamp describes one read of one task: a several-id batch refuses the
// flag (the position-flag precedent), and a malformed stamp is exit 2 — an
// explicit argument is never quietly dropped.
func TestCLIExpectUpdatedRejections(t *testing.T) {
	initStore(t)
	a := addTask(t, "one")
	b := addTask(t, "two")

	if _, _, code := runSplit(t, "done", a, b, "--expect-updated", "2020-01-01T00:00:00Z"); code != 2 {
		t.Errorf("several ids + --expect-updated want exit 2, got %d", code)
	}
	if _, _, code := runSplit(t, "move", a, "ready", "--expect-updated", "yesterday-ish"); code != 2 {
		t.Errorf("malformed stamp want exit 2, got %d", code)
	}
}

// note's epic branch honours the guard too: both entities carry `updated`, so
// a box's progress record races between sessions exactly like a task's.
func TestCLIExpectUpdatedOnEpicNote(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "epic", "add", "the box")
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, out)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.ID == "" {
		t.Fatalf("parse epic add --json (%v):\n%s", err, out)
	}

	stdout, stderr, code := runSplit(t, "--json", "note", e.ID, "box progress", "--expect-updated", "2020-01-01T00:00:00Z")
	if code != 0 {
		t.Fatalf("epic note exit = %d:\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "stale_read") {
		t.Errorf("epic envelope must carry stale_read:\n%s", stdout)
	}
	if !strings.Contains(stderr, e.ID+" changed since your read") {
		t.Errorf("missing stderr note for the box:\n%s", stderr)
	}
}
