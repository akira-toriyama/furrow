package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// schema-outdated names the store that fell behind: id `meta` for the board,
// `archive` for its archive store — one row said "board is schema v3" for
// either, so an archive left behind read as the whole board being read-only
// (t-rns9).
func TestLintSchemaOutdatedNamesTheStore(t *testing.T) {
	a := newFSApp(t)
	tk, err := a.Add("retired", AddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil { // creates archive/
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "archive", "meta.json"), []byte("{\n  \"schema_version\": 3\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps {
		if p.Code == "schema-outdated" {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) != 1 || ids[0] != "archive" {
		t.Errorf("schema-outdated ids = %v, want [archive] alone (the board itself is current)", ids)
	}
	if fe := core.AsError(err); fe != nil {
		t.Fatal(fe)
	}
}
