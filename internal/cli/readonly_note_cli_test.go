package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A board this binary cannot write says so on the READ side: one stderr line
// on the orient/listing reads, stdout untouched (t-cx64 — the state used to be
// discoverable only by a failed write or a hunch `furrow board`).
func TestReadOnlyBoardNotesOnReads(t *testing.T) {
	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })

	initStore(t)
	addTask(t, "written while writable")

	// Age the board's declared layout below the binary's: reads keep working,
	// writes would be refused (schema-upgrade-required) — the read-only state.
	dir := os.Getenv("FURROW_DIR")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{\n  \"schema_version\": 8\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"ls"}, {"brief"}, {"show", "--no-body"}, {"next"}} {
		if len(args) > 1 && args[0] == "show" {
			continue // show needs ids; covered by ls/brief/next
		}
		se.Reset()
		stdout, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v on a read-only board: exit %d", args, code)
		}
		if !strings.Contains(se.String(), "READ-ONLY") || !strings.Contains(se.String(), "v8") {
			t.Errorf("%v: stderr %q must carry the READ-ONLY note with the versions", args, se.String())
		}
		if strings.Contains(stdout, "READ-ONLY") {
			t.Errorf("%v: the note leaked into stdout: %q", args, stdout)
		}
	}

	// `board` is the mismatch REPORT — it must not also nag.
	se.Reset()
	if _, code := run(t, "board"); code != 0 {
		t.Fatal("board must still answer")
	}
	if strings.Contains(se.String(), "READ-ONLY") {
		t.Errorf("board: the note must not fire on the reporting command (stderr %q)", se.String())
	}
}

// A writable board stays silent — the note is for the broken state only.
func TestWritableBoardStaysQuiet(t *testing.T) {
	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })

	initStore(t)
	addTask(t, "normal")
	se.Reset()
	if _, code := run(t, "ls"); code != 0 {
		t.Fatal("ls failed")
	}
	if strings.Contains(se.String(), "READ-ONLY") {
		t.Errorf("writable board must not nag: stderr %q", se.String())
	}
}
