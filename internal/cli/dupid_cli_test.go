package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// The t-5xp5 repro, end to end: a hand-copied shard carrying the same inner id
// under another filename made the next ordinary write keep one task, delete
// the other's file, and exit 0. The write must instead refuse at exit 2,
// naming the id and the lint remedy, with both files intact — while reads stay
// open so the operator can diagnose what to repair.
func TestCLIWriteRefusesDuplicateIDBoard(t *testing.T) {
	initStore(t)
	id := addTask(t, "original")

	dir := os.Getenv(app.EnvDir)
	orig, err := os.ReadFile(filepath.Join(dir, "tasks", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	clone := bytes.Replace(orig, []byte("original"), []byte("CLONE"), 1)
	stray := filepath.Join(dir, "tasks", "t-zzzzz.json")
	if err := os.WriteFile(stray, clone, 0o644); err != nil {
		t.Fatal(err)
	}

	fe, _ := runErr(t, "add", "another")
	if fe == nil || fe.Code != core.CodeValidation || fe.Subject != id {
		t.Fatalf("add on a duplicate-id board = %+v, want exit 2 about %s", fe, id)
	}
	for _, want := range []string{id, "furrow lint"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("message %q should mention %q", fe.Msg, want)
		}
	}

	for path, want := range map[string][]byte{filepath.Join(dir, "tasks", id+".json"): orig, stray: clone} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s must survive the refused write: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s changed across a refused write", path)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("tasks/ holds %d files, want the same 2 — nothing created, nothing deleted", len(entries))
	}

	if _, code := run(t, "ls", "--json"); code != 0 {
		t.Errorf("reads must stay open on a duplicate-id board (ls exit %d)", code)
	}
}
