package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// moveBoard is a board with two done tasks archived and two left hot, so each
// direction of a move has a task to move and a task staying in either store.
type moveBoard struct {
	a                      *App
	archived, stayArchived string
	live, stayLive         string
}

func newMoveBoard(t *testing.T) moveBoard {
	t.Helper()
	b := moveBoard{a: newFSApp(t)}
	for _, id := range []*string{&b.archived, &b.stayArchived, &b.live, &b.stayLive} {
		tk, err := b.a.Add("done thing", AddOpts{Status: "done"})
		if err != nil {
			t.Fatal(err)
		}
		*id = tk.ID
	}
	if _, err := b.a.ArchiveIDs([]string{b.archived, b.stayArchived}, false); err != nil {
		t.Fatal(err)
	}
	return b
}

// copyShard plants a misnamed copy of id's shard in the store at dir, the
// duplicate a hand copy or a merge leaves.
func copyShard(t *testing.T, dir, id string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, core.TaskPath(id)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, core.TaskPath("t-zzzzz")), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A move between the hot store and archive/ writes both and commits the
// destination first, so it raises every refusal either store's Save would
// raise before the first byte lands (t-paax). Raised by Save itself, each came
// late: an archive/ on an older layout refused an unarchive after the task was
// back on the hot board, and a duplicate id, the moving task's or one staying
// behind, refused after the other store had taken the task — or, when the
// moving task's copy was the duplicate's first, passed and deleted the body
// the remaining copy still names.
func TestArchiveMoveRefusalIsTotal(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		spoil func(t *testing.T, b moveBoard)
	}{
		{"outdated archive", core.KindSchemaUpgradeRequired, func(t *testing.T, b moveBoard) {
			old := fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion-1)
			if err := os.WriteFile(filepath.Join(b.a.Dir, "archive", "meta.json"), []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate of the archived task", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, filepath.Join(b.a.Dir, "archive"), b.archived)
		}},
		{"duplicate of a task staying archived", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, filepath.Join(b.a.Dir, "archive"), b.stayArchived)
		}},
		{"duplicate of the hot task", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, b.a.Dir, b.live)
		}},
		{"duplicate of a task staying hot", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, b.a.Dir, b.stayLive)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, verb := range []string{"unarchive", "archive"} {
				t.Run(verb, func(t *testing.T) {
					b := newMoveBoard(t)
					c.spoil(t, b)

					moving, from, to := b.archived, filepath.Join(b.a.Dir, "archive"), b.a.Dir
					var err error
					if verb == "unarchive" {
						_, err = b.a.Unarchive([]string{moving})
					} else {
						moving, from, to = b.live, b.a.Dir, filepath.Join(b.a.Dir, "archive")
						_, err = b.a.ArchiveIDs([]string{moving}, false)
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

// The duplicate-id refusal tells the operator to repair the extra shard by
// hand and that `furrow lint` flags every duplicate. For a duplicate inside
// archive/ that pointer led nowhere — lint judged archive/ only for open tasks
// and parked keys — so lint names it there as it does on the hot board: the
// id, and the misnamed file to fix.
func TestLintFlagsADuplicateInTheArchive(t *testing.T) {
	b := newMoveBoard(t)
	copyShard(t, filepath.Join(b.a.Dir, "archive"), b.stayArchived)

	_, err := b.a.Unarchive([]string{b.archived})
	if fe := core.AsError(err); fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "furrow lint") {
		t.Fatalf("unarchive = %v; want the duplicate-id refusal pointing at furrow lint", err)
	}
	ps := mustLint(t, b.a)
	dup := problemsWithCode(ps, "duplicate-id")
	if len(dup) != 1 || dup[0].ID != b.stayArchived || !strings.Contains(dup[0].Msg, "archive/") {
		t.Errorf("duplicate-id = %+v; want one, for %s, naming archive/", dup, b.stayArchived)
	}
	misnamed := problemsWithCode(ps, "shard-misnamed")
	if len(misnamed) != 1 || misnamed[0].ID != "t-zzzzz" || !strings.Contains(misnamed[0].Msg, "archive/"+core.TaskPath("t-zzzzz")) {
		t.Errorf("shard-misnamed = %+v; want the extra file archive/%s", misnamed, core.TaskPath("t-zzzzz"))
	}
}
