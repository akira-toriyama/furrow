package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A load failure is the error it is — a board newer than this binary is
// schema-too-new, exit 3 — never "task not found" for every directive
// (t-xypx: exists() swallowed the Load error).
func TestApplySurfacesTheLoadErrorInsteadOfNotFound(t *testing.T) {
	a := newFSApp(t)
	tk, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "meta.json"), []byte("{\n  \"schema_version\": 99\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = a.ApplyDirectives("SetStatus-task: "+tk.ID+" done\n", "#1", OnMerge, "")
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSchemaTooNew {
		t.Fatalf("want schema-too-new from apply, got %v", err)
	}
}

// A directive naming an archived task is a per-directive miss that says the
// task is archived, and the run goes on to the next directive.
func TestApplyNamesAnArchivedTaskAndContinues(t *testing.T) {
	a := newFSApp(t)
	gone, err := a.Add("gone", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	live, err := a.Add("live", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{gone.ID}, false); err != nil {
		t.Fatal(err)
	}
	res, err := a.ApplyDirectives("SetStatus-task: "+gone.ID+" done\nSetStatus-task: "+live.ID+" done\n", "#1", OnMerge, "")
	if err != nil {
		t.Fatalf("a per-directive miss must not abort the run: %v", err)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("outcomes = %+v; want both directives judged", res.Outcomes)
	}
	if res.Outcomes[0].Error == "" || !strings.Contains(res.Outcomes[0].Error, "archived") {
		t.Errorf("the archived miss must say so: %+v", res.Outcomes[0])
	}
	if res.Outcomes[1].Action != "moved" {
		t.Errorf("the next directive must still apply: %+v", res.Outcomes[1])
	}
}
