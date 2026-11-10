package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// A move between the hot store and archive/ writes both and commits the
// destination first, so it raises every refusal either store's Save would
// raise before the first byte lands (t-paax). Raised by Save itself, each came
// late: an archive/ on an older layout refused an unarchive after the task was
// back on the hot board, and a duplicate id refused after the other store had
// taken the task — or, when the moving task's copy was the duplicate's first,
// passed and deleted the body the remaining copy still names.
func TestArchiveMoveRefusalIsTotal(t *testing.T) {
	copyShard := func(t *testing.T, from, to string) {
		t.Helper()
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		kind  string
		spoil func(t *testing.T, dir, archived, live string)
	}{
		{"outdated archive", core.KindSchemaUpgradeRequired, func(t *testing.T, dir, _, _ string) {
			old := fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion-1)
			if err := os.WriteFile(filepath.Join(dir, "archive", "meta.json"), []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate archived id", core.KindValidation, func(t *testing.T, dir, archived, _ string) {
			copyShard(t, filepath.Join(dir, "archive", core.TaskPath(archived)), filepath.Join(dir, "archive", core.TaskPath("t-zzzzz")))
		}},
		{"duplicate hot id", core.KindValidation, func(t *testing.T, dir, _, live string) {
			copyShard(t, filepath.Join(dir, core.TaskPath(live)), filepath.Join(dir, core.TaskPath("t-zzzzz")))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, verb := range []string{"unarchive", "archive"} {
				t.Run(verb, func(t *testing.T) {
					a := newFSApp(t)
					archived, err := a.Add("archived", AddOpts{Status: "done"})
					if err != nil {
						t.Fatal(err)
					}
					live, err := a.Add("live", AddOpts{Status: "done"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := a.ArchiveIDs([]string{archived.ID}, false); err != nil {
						t.Fatal(err)
					}
					c.spoil(t, a.Dir, archived.ID, live.ID)

					moving, from, to := archived.ID, filepath.Join(a.Dir, "archive"), a.Dir
					if verb == "unarchive" {
						_, err = a.Unarchive([]string{moving})
					} else {
						moving, from, to = live.ID, a.Dir, filepath.Join(a.Dir, "archive")
						_, err = a.ArchiveIDs([]string{moving}, false)
					}
					if fe := core.AsError(err); fe == nil || fe.Kind != c.kind {
						t.Fatalf("%s %s = %v, want kind %s", verb, moving, err, c.kind)
					}
					for _, f := range []string{core.TaskPath(moving), core.BodyPath(moving)} {
						if !fileExists(filepath.Join(from, f)) {
							t.Errorf("the refused %s took %s from its store", verb, f)
						}
						if fileExists(filepath.Join(to, f)) {
							t.Errorf("the refused %s wrote %s into the other store", verb, f)
						}
					}
				})
			}
		})
	}
}
