package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// moveBoard is a board with two done tasks archived, one done task hot and
// one open task hot, so each direction of a move, the age sweep included, has
// a task to move and a task staying in either store.
type moveBoard struct {
	a                      *App
	archived, stayArchived string
	live, stayLive         string
}

func newMoveBoard(t *testing.T) moveBoard {
	t.Helper()
	b := moveBoard{a: newFSApp(t)}
	for _, n := range []struct {
		id     *string
		status string
	}{{&b.archived, "done"}, {&b.stayArchived, "done"}, {&b.live, "done"}, {&b.stayLive, "backlog"}} {
		tk, err := b.a.Add("thing", AddOpts{Status: n.status})
		if err != nil {
			t.Fatal(err)
		}
		*n.id = tk.ID
	}
	if _, err := b.a.ArchiveIDs([]string{b.archived, b.stayArchived}, false); err != nil {
		t.Fatal(err)
	}
	return b
}

// copyShard plants a misnamed copy of id's shard, named as, in the store at
// dir: the duplicate a hand copy or a merge leaves.
func copyShard(t *testing.T, dir, id, as string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, core.TaskPath(id)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, core.TaskPath(as)), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// outdate rewrites the layout version the store at dir declares to the one
// before this binary's.
func outdate(t *testing.T, dir string) {
	t.Helper()
	old := fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion-1)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A move between the hot store and archive/ writes both and commits the
// destination first, so it raises every refusal either store's Save would
// raise before the first byte lands (t-paax): both write gates and both
// duplicate-id checks, for unarchive, archive by id and the age sweep alike.
// Raised by Save itself, each came late: an archive/ on an older layout
// refused an unarchive after the task was back on the hot board, and a
// duplicate id, the moving task's or one staying behind, refused after the
// other store had taken the task — or, when the moving task's copy was the
// duplicate's first, passed and deleted the body the remaining copy still
// names.
func TestArchiveMoveRefusalIsTotal(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		spoil func(t *testing.T, b moveBoard)
	}{
		{"outdated board", core.KindSchemaUpgradeRequired, func(t *testing.T, b moveBoard) {
			outdate(t, b.a.Dir)
		}},
		{"outdated archive", core.KindSchemaUpgradeRequired, func(t *testing.T, b moveBoard) {
			outdate(t, filepath.Join(b.a.Dir, "archive"))
		}},
		{"duplicate of the archived task", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, filepath.Join(b.a.Dir, "archive"), b.archived, "t-zzzzz")
		}},
		{"duplicate of a task staying archived", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, filepath.Join(b.a.Dir, "archive"), b.stayArchived, "t-zzzzz")
		}},
		{"duplicate of the hot task", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, b.a.Dir, b.live, "t-zzzzz")
		}},
		{"duplicate of a task staying hot", core.KindValidation, func(t *testing.T, b moveBoard) {
			copyShard(t, b.a.Dir, b.stayLive, "t-zzzzz")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, verb := range []string{"unarchive", "archive", "sweep"} {
				t.Run(verb, func(t *testing.T) {
					b := newMoveBoard(t)
					c.spoil(t, b)

					moving, from, to := b.live, b.a.Dir, filepath.Join(b.a.Dir, "archive")
					var err error
					switch verb {
					case "unarchive":
						moving, from, to = b.archived, to, from
						_, err = b.a.Unarchive([]string{moving})
					case "archive":
						_, err = b.a.ArchiveIDs([]string{moving}, false)
					case "sweep": // every done task closed over a day ago: live alone
						b.a.Clock = &fixedClock{t: b.a.Clock.Now().Add(48 * time.Hour)}
						_, err = b.a.Archive(1, false)
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
// archive/ that pointer led nowhere — lint judged archive/ for open tasks,
// parked keys and its layout, never for a duplicate — so lint names it there
// as it does on the hot board, as errors: the id once however many copies
// carry it, and each misnamed file to fix.
func TestLintFlagsADuplicateInTheArchive(t *testing.T) {
	b := newMoveBoard(t)
	arc := filepath.Join(b.a.Dir, "archive")
	copyShard(t, arc, b.stayArchived, "t-zzzzy")
	copyShard(t, arc, b.stayArchived, "t-zzzzz")

	_, err := b.a.Unarchive([]string{b.archived})
	if fe := core.AsError(err); fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "furrow lint") {
		t.Fatalf("unarchive = %v; want the duplicate-id refusal pointing at furrow lint", err)
	}
	ps := mustLint(t, b.a)
	dup := problemsWithCode(ps, "duplicate-id")
	if len(dup) != 1 || dup[0].ID != b.stayArchived || dup[0].Severity != core.SevError || !strings.Contains(dup[0].Msg, "archive/") {
		t.Errorf("duplicate-id = %+v; want one error, for %s, naming archive/", dup, b.stayArchived)
	}
	misnamed := problemsWithCode(ps, "shard-misnamed")
	if len(misnamed) != 2 {
		t.Fatalf("shard-misnamed = %+v; want the two extra files", misnamed)
	}
	for i, stem := range []string{"t-zzzzy", "t-zzzzz"} {
		if p := misnamed[i]; p.ID != stem || p.Severity != core.SevError || !strings.Contains(p.Msg, "archive/"+core.TaskPath(stem)) {
			t.Errorf("shard-misnamed[%d] = %+v; want an error naming archive/%s", i, p, core.TaskPath(stem))
		}
	}
}
