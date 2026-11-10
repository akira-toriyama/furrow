package app

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// scriptedIDs is a store whose NextID hands out ids in the given order, so a
// test can make a mint draw a chosen id; fsstore's draw is random.
type scriptedIDs struct {
	Store
	ids []string
}

func (s *scriptedIDs) NextID() (string, error) {
	if len(s.ids) == 0 {
		return "", errors.New("scriptedIDs: no ids left")
	}
	id := s.ids[0]
	s.ids = s.ids[1:]
	return id, nil
}

// plantable is the slice of a store that plant writes through: the hot
// store and archive/'s fsstore both have it.
type plantable interface {
	Load() (*core.Index, error)
	Save(*core.Index) error
	LoadBody(id string) (string, error)
	SaveBody(id, body string) error
}

// plant writes task and its body straight into s — the state an interrupted
// move or a reused id leaves, which no command makes on purpose.
func plant(t *testing.T, s plantable, task core.Task, body string) {
	t.Helper()
	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(task)
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}
}

// plantShard writes task's shard into s with no body — a shard brought back
// by hand, or kept when resolving a sync conflict, without its prose.
func plantShard(t *testing.T, s plantable, task core.Task) {
	t.Helper()
	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(task)
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
}

func findIn(t *testing.T, s plantable, id string) core.Task {
	t.Helper()
	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := idx.Find(id)
	if tk == nil {
		t.Fatalf("%s is not in the store", id)
	}
	return *tk
}

// impostor is a different task under of's id: another title and another
// creation instant, as a mint that skipped archive/ produced.
func impostor(of core.Task) core.Task {
	tk := of
	tk.Title = "another task"
	tk.Created = of.Created.Add(-48 * time.Hour)
	tk.Updated = tk.Created
	return tk
}

// boardBytes reads every file under dir, keyed by its path below dir.
func boardBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A retired id is still taken (t-erd6): the projects board's t-ksee was a new
// task drawing an archived task's id, which lint did not see and the next
// archive of it would have written over the archived task's body. Every mint
// path draws through one helper; each is pinned with the draw scripted to
// hit the retired id first.
func TestMintSkipsARetiredID(t *testing.T) {
	const fresh = "t-fresh"
	cases := []struct {
		name string
		pre  []string // ids the setup draws before the mint under test
		mint func(t *testing.T, a *App) string
	}{
		{"add", nil, func(t *testing.T, a *App) string {
			tk, err := a.Add("new", AddOpts{})
			if err != nil {
				t.Fatal(err)
			}
			return tk.ID
		}},
		{"add --batch", nil, func(t *testing.T, a *App) string {
			tasks, _, err := a.AddBatch([]BatchSpec{{AddSpec: AddSpec{Title: "new"}}})
			if err != nil {
				t.Fatal(err)
			}
			return tasks[0].ID
		}},
		{"repeat successor", []string{"t-pred1"}, func(t *testing.T, a *App) string {
			pred := mustAddRepeating(t, a, "chore", "2026-03-01", "monthly", AddOpts{})
			_, rep, err := a.moveOne(pred.ID, a.Cfg.DoneLane)
			if err != nil {
				t.Fatal(err)
			}
			if rep == nil || rep.Created == nil {
				t.Fatalf("closing %s minted no successor: %+v", pred.ID, rep)
			}
			return *rep.Created
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			tk, err := a.Add("retired", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
				t.Fatal(err)
			}
			a.Clock = &fixedClock{t: time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)}
			a.Store = &scriptedIDs{Store: a.Store, ids: append(append([]string{}, c.pre...), tk.ID, fresh)}
			if got := c.mint(t, a); got != fresh {
				t.Fatalf("minted %s; want %s, since %s is retired to archive/", got, fresh, tk.ID)
			}
		})
	}
}

// Two different tasks under one id across the stores passed every refusal —
// each index alone is unique — and the move then kept one task's shard
// beside the other's body and deleted the source, at exit 0 (t-erd6). It is
// refused now before anything is written, in each direction and on the age
// sweep, and the refusal points at lint, which names the id.
func TestMoveRefusesADifferentTaskUnderTheID(t *testing.T) {
	for _, verb := range []string{"archive", "sweep", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			b := newMoveBoard(t)
			arc, err := b.a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			moving, src, dst := b.live, plantable(b.a.Store), plantable(arc)
			if verb == "unarchive" {
				moving, src, dst = b.archived, arc, b.a.Store
			}
			plant(t, dst, impostor(findIn(t, src, moving)), "# another task\n")
			before := boardBytes(t, b.a.Dir)

			switch verb {
			case "archive":
				_, err = b.a.ArchiveIDs([]string{moving}, false)
			case "sweep": // every done task closed over a day ago: live alone
				b.a.Clock = &fixedClock{t: b.a.Clock.Now().Add(48 * time.Hour)}
				_, err = b.a.Archive(1, false)
			case "unarchive":
				_, err = b.a.Unarchive([]string{moving})
			}
			fe := core.AsError(err)
			if fe == nil || fe.Kind != core.KindValidation || fe.Subject != moving || !strings.Contains(fe.Msg, "duplicate-id") {
				t.Fatalf("%s %s = %v; want a validation refusal naming it and pointing at lint's duplicate-id", verb, moving, err)
			}
			if after := boardBytes(t, b.a.Dir); !maps.Equal(before, after) {
				t.Errorf("the refused %s changed the board", verb)
			}
			if !hasProblem(mustLint(t, b.a), "duplicate-id", moving, core.SevError) {
				t.Errorf("lint does not name %s as a duplicate-id error", moving)
			}
		})
	}
}

// An archive stopped between its two saves leaves the task in both indexes,
// and an edit can reach the hot copy before the re-run. The re-run writes the
// hot copy over archive/'s, shard and body; it used to keep archive/'s shard
// beside the hot body, losing the retitle while the edited body landed.
func TestArchiveRetryFinishesWithTheHotCopy(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	live := findIn(t, arc, b.archived)
	live.Title = "retitled after the interruption"
	const edited = "# edited after the interruption\n"
	plant(t, b.a.Store, live, edited)

	if _, err := b.a.ArchiveIDs([]string{b.archived}, false); err != nil {
		t.Fatalf("the re-run must finish the move: %v", err)
	}
	if got := findIn(t, arc, b.archived); got.Title != live.Title {
		t.Errorf("archive/ kept %q; want the hot copy's %q", got.Title, live.Title)
	}
	if body, err := arc.LoadBody(b.archived); err != nil || body != edited {
		t.Errorf("archive/ body = %q (%v); want the hot copy's", body, err)
	}
	hot, err := b.a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if hot.Has(b.archived) || b.a.Store.BodyExists(b.archived) {
		t.Error("the hot copy must be gone once the move is finished")
	}
	if hasProblem(mustLint(t, b.a), "duplicate-id", b.archived, "") {
		t.Error("lint still sees the id in both stores")
	}
}

// An unarchive stopped between its two saves leaves the task in both indexes
// with the restored body on the board, where an edit can land before the
// re-run. The re-run keeps the hot copy as it stands, shard and body, and
// drops archive/'s; it used to write archive/'s body over the edit. An edit
// furrow did not make is not journaled for a plain sync to publish.
func TestUnarchiveRetryKeepsTheHotCopy(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	live := findIn(t, arc, b.archived)
	live.Title = "retitled after the interruption"
	const edited = "# edited after the interruption\n"
	plant(t, b.a.Store, live, edited)
	b.a.bodiesTouched = nil

	rep, err := b.a.Unarchive([]string{b.archived})
	if err != nil {
		t.Fatalf("the re-run must finish the move: %v", err)
	}
	if len(rep.Tasks) != 1 || rep.Tasks[0].Title != live.Title {
		t.Errorf("report = %+v; want the hot copy", rep.Tasks)
	}
	if got := findIn(t, b.a.Store, b.archived); got.Title != live.Title {
		t.Errorf("the board holds %q; want the hot copy's %q", got.Title, live.Title)
	}
	if body, err := b.a.Store.LoadBody(b.archived); err != nil || body != edited {
		t.Errorf("hot body = %q (%v); want the edit kept", body, err)
	}
	if b.a.bodiesTouched[b.archived] {
		t.Error("the kept edit is journaled, so a plain sync would publish a body furrow did not write")
	}
	arcIdx, err := arc.Load()
	if err != nil {
		t.Fatal(err)
	}
	if arcIdx.Has(b.archived) || arc.BodyExists(b.archived) {
		t.Error("archive/'s copy must be gone once the move is finished")
	}
}

// The pure remnant — the hot body still holds the bytes the interrupted run
// wrote — is rewritten through saveBody as before, so the restored body rides
// the journal and a plain sync publishes it (t-gqe6).
//
// bite-exempt: it pins what the base already did, so the converge path that
// now skips some rewrites keeps journaling this one.
func TestUnarchiveRetryJournalsTheRemnantBody(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	body, err := arc.LoadBody(b.archived)
	if err != nil {
		t.Fatal(err)
	}
	plant(t, b.a.Store, findIn(t, arc, b.archived), body)
	b.a.bodiesTouched = nil

	if _, err := b.a.Unarchive([]string{b.archived}); err != nil {
		t.Fatalf("the re-run must finish the move: %v", err)
	}
	if !b.a.bodiesTouched[b.archived] {
		t.Errorf("the remnant body is not journaled: %v", b.a.bodiesTouched)
	}
}

// duplicate-id spans the two stores (t-erd6), worded for the state, since the
// ways out differ: two tasks under one id need a hand repair, one task in
// both is an interrupted move that either command finishes.
func TestLintFlagsAnIDInBothStores(t *testing.T) {
	for _, c := range []struct {
		name, want string
		hot        func(t *testing.T, arc plantable, id string) (core.Task, string)
	}{
		{"two tasks", "two different tasks", func(t *testing.T, arc plantable, id string) (core.Task, string) {
			return impostor(findIn(t, arc, id)), "# another task\n"
		}},
		{"one task", "stopped between its two writes", func(t *testing.T, arc plantable, id string) (core.Task, string) {
			return findIn(t, arc, id), "# thing\n"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newMoveBoard(t)
			arc, err := b.a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			tk, body := c.hot(t, arc, b.archived)
			plant(t, b.a.Store, tk, body)

			dup := problemsWithCode(mustLint(t, b.a), "duplicate-id")
			if len(dup) != 1 || dup[0].ID != b.archived || dup[0].Severity != core.SevError || !strings.Contains(dup[0].Msg, c.want) {
				t.Fatalf("duplicate-id = %+v; want one error for %s saying %q", dup, b.archived, c.want)
			}
		})
	}
}

// A shard can come back to the board without its body — restored by hand, or
// kept when resolving the sync conflict an archive crossing a reopen raises.
// That missing file is no newer copy of the prose. archive used to write it
// over archive/'s body as an empty file, the remedy lint names for the state.
func TestArchiveRetryKeepsArchivesBodyWhenTheHotOneIsMissing(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	prose, err := arc.LoadBody(b.archived)
	if err != nil || prose == "" {
		t.Fatalf("archive/ body = %q (%v); the fixture needs prose", prose, err)
	}
	plantShard(t, b.a.Store, findIn(t, arc, b.archived))

	if _, err := b.a.ArchiveIDs([]string{b.archived}, false); err != nil {
		t.Fatalf("the re-run must finish the move: %v", err)
	}
	if body, err := arc.LoadBody(b.archived); err != nil || body != prose {
		t.Errorf("archive/ body = %q (%v); want its own prose %q kept", body, err, prose)
	}
	hot, err := b.a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if hot.Has(b.archived) {
		t.Error("the hot shard must be gone once the move is finished")
	}
}

// The unarchive twin: a finished move restores a missing hot body from
// archive/'s and journals it, and only then does archive/'s copy go — it is
// the last copy of the prose.
//
// bite-exempt: the base restored this body too; it pins that the converge path
// added since still does.
func TestUnarchiveRetryRestoresAMissingHotBody(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	prose, err := arc.LoadBody(b.archived)
	if err != nil || prose == "" {
		t.Fatalf("archive/ body = %q (%v); the fixture needs prose", prose, err)
	}
	plantShard(t, b.a.Store, findIn(t, arc, b.archived))
	b.a.bodiesTouched = nil

	if _, err := b.a.Unarchive([]string{b.archived}); err != nil {
		t.Fatalf("the re-run must finish the move: %v", err)
	}
	if body, err := b.a.Store.LoadBody(b.archived); err != nil || body != prose {
		t.Errorf("hot body = %q (%v); want archive/'s prose %q restored", body, err, prose)
	}
	if !b.a.bodiesTouched[b.archived] {
		t.Error("the restored body is not journaled for a plain sync to publish")
	}
	if arc.BodyExists(b.archived) {
		t.Error("archive/'s copy must go once the hot board holds the body")
	}
}

// A finished move adds no edge to the board: its hot copy is already there.
// The dep check used to judge archive/'s copy, so once the edge was dropped
// on the board, the remedy lint names was refused over an edge only archive/
// still had, which no command can drop. Nor is an edge the hot copy still
// has the call's to refuse: it strands nothing that is not stranded already.
func TestUnarchiveFinishingAMoveJudgesNoEdge(t *testing.T) {
	for _, c := range []struct {
		name string
		deps func(z string) []string // the hot copy's deps
	}{
		{"edge dropped on the board", func(string) []string { return []string{} }},
		{"edge still on the board", func(z string) []string { return []string{z} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			z, err := a.Add("dependency", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := a.Add("dependent", AddOpts{Status: "done", Deps: []string{z.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{dep.ID, z.ID}, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			live := findIn(t, arc, dep.ID) // the interrupted unarchive's hot copy
			live.Deps = c.deps(z.ID)
			body, err := arc.LoadBody(dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			plant(t, a.Store, live, body)

			if _, err := a.Unarchive([]string{dep.ID}); err != nil {
				t.Fatalf("finishing the move strands no edge: %v", err)
			}
			if got := findIn(t, a.Store, dep.ID); !slices.Equal(got.Deps, live.Deps) {
				t.Errorf("deps = %v; want the hot copy's %v", got.Deps, live.Deps)
			}
		})
	}
}

// The mint's archive/ read is a stat, never a parse, so a board whose
// archive/ lint and the moves refuse to read can still add — and still skips
// a retired id where one can be seen. An archive/ that is not a directory
// holds no shard.
func TestMintProbeSurvivesABrokenArchive(t *testing.T) {
	const fresh = "t-fresh"
	for _, c := range []struct {
		name  string
		spoil func(t *testing.T, a *App)
	}{
		{"newer layout", func(t *testing.T, a *App) {
			meta := fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion+1)
			if err := os.WriteFile(filepath.Join(a.Dir, "archive", "meta.json"), []byte(meta), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt shard", func(t *testing.T, a *App) {
			if err := os.WriteFile(filepath.Join(a.Dir, "archive", core.TaskPath("t-bad00")), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			tk, err := a.Add("retired", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
				t.Fatal(err)
			}
			c.spoil(t, a)
			a.Store = &scriptedIDs{Store: a.Store, ids: []string{tk.ID, fresh}}
			got, err := a.Add("new", AddOpts{})
			if err != nil {
				t.Fatalf("add on a board with a %s in archive/: %v", c.name, err)
			}
			if got.ID != fresh {
				t.Errorf("minted %s; want %s, since %s is retired to archive/", got.ID, fresh, tk.ID)
			}
		})
	}
	t.Run("archive/tasks refuses the stat", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs POSIX permissions enforced on this user")
		}
		a := newFSApp(t)
		tk, err := a.Add("retired", AddOpts{Status: "done"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
			t.Fatal(err)
		}
		tasks := filepath.Join(a.Dir, "archive", "tasks")
		if err := os.Chmod(tasks, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(tasks, 0o755) })
		a.Store = &scriptedIDs{Store: a.Store, ids: []string{tk.ID, fresh}}
		if got, err := a.Add("new", AddOpts{}); err == nil {
			t.Fatalf("add minted %s; a probe that cannot look must fail the add, not guess", got.ID)
		}
	})
	t.Run("archive/ is a file", func(t *testing.T) {
		a := newFSApp(t)
		if err := os.WriteFile(filepath.Join(a.Dir, "archive"), []byte("not a store"), 0o644); err != nil {
			t.Fatal(err)
		}
		a.Store = &scriptedIDs{Store: a.Store, ids: []string{fresh}}
		if got, err := a.Add("new", AddOpts{}); err != nil || got.ID != fresh {
			t.Fatalf("add = %v, %v; want %s", got, err, fresh)
		}
	})
}

// lint reports the cross-store duplicate once per id, however many copies
// archive/ holds; archive/'s own duplicate is its separate finding.
func TestLintReportsAnIDInBothStoresOnce(t *testing.T) {
	b := newMoveBoard(t)
	arc, err := b.a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	copyShard(t, filepath.Join(b.a.Dir, "archive"), b.archived, "t-zzzzz")
	plant(t, b.a.Store, findIn(t, arc, b.archived), "# thing\n")

	var across, inArchive int
	for _, p := range problemsWithCode(mustLint(t, b.a), "duplicate-id") {
		switch {
		case p.ID != b.archived:
		case strings.Contains(p.Msg, "across the hot board"):
			across++
		case strings.Contains(p.Msg, "duplicate id in archive/"):
			inArchive++
		}
	}
	if across != 1 || inArchive != 1 {
		t.Errorf("duplicate-id rows for %s: %d across the stores, %d in archive/; want one each", b.archived, across, inArchive)
	}
}

// archived-not-done tells the operator to unarchive the open task. The move
// refuses that while the board holds a different task under the id, so the
// advice puts the repair first then; under the same task the unarchive
// finishes an interrupted move, and the advice stands.
func TestArchivedNotDoneAdvisesTheRepairFirst(t *testing.T) {
	for _, c := range []struct {
		name, want string
		board      func(open core.Task) core.Task
		restores   bool
	}{
		{"a different task on the board", "repair the duplicate-id first", impostor, false},
		{"the same task on the board", "brings it back to the board", func(open core.Task) core.Task { return open }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newMoveBoard(t)
			arc, err := b.a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			arcIdx, err := arc.Load()
			if err != nil {
				t.Fatal(err)
			}
			open, _ := arcIdx.Find(b.archived)
			open.Status, open.Closed = "backlog", nil
			if err := arc.Save(arcIdx); err != nil {
				t.Fatal(err)
			}
			body, err := arc.LoadBody(b.archived)
			if err != nil {
				t.Fatal(err)
			}
			plant(t, b.a.Store, c.board(findIn(t, arc, b.archived)), body)

			nd := problemsWithCode(mustLint(t, b.a), "archived-not-done")
			if len(nd) != 1 || nd[0].ID != b.archived || !strings.Contains(nd[0].Msg, c.want) {
				t.Fatalf("archived-not-done = %+v; want one finding for %s saying %q", nd, b.archived, c.want)
			}
			if _, err := b.a.Unarchive([]string{b.archived}); (err == nil) != c.restores {
				t.Errorf("unarchive = %v; want it to succeed: %v", err, c.restores)
			}
		})
	}
}

// boardWithProse archives one done task whose body carries prose below its
// heading, returning the app, archive/'s store and the task's id and body.
func boardWithProse(t *testing.T) (*App, plantable, string, string) {
	t.Helper()
	a := newFSApp(t)
	tk, err := a.Add("prosy", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	const prose = "# prosy\n\nthe archived prose, written before the archive\n"
	if err := a.Store.SaveBody(tk.ID, prose); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
		t.Fatal(err)
	}
	arc, err := a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	return a, arc, tk.ID, prose
}

// A finished move keeps the board's body only while it still holds archive/'s
// prose. Once the board's shard came back without its body, a body furrow
// writes there — edit's empty placeholder, a note, retitle's seeded heading —
// is composed without that prose: the blank one yields archive/'s, and the
// others refuse the call, since keeping either body drops lines. A note added
// to the restored prose is an edit of it, and the board's body wins.
func TestFinishedMoveKeepsArchivesProse(t *testing.T) {
	const noted = "# prosy\n\n- a later note\n\nthe archived prose, written before the archive\n"
	for _, c := range []struct {
		name, board string
		want        string // the body the move ends with; "" = refused
	}{
		{"edit's empty placeholder", "", "archived"},
		{"a note on the body-less copy", "- a later note\n", ""},
		{"retitle's seeded heading", "# a new title\n", ""},
		{"a note in the restored prose", noted, "board"},
	} {
		for _, verb := range []string{"archive", "unarchive"} {
			t.Run(c.name+"/"+verb, func(t *testing.T) {
				a, arc, id, prose := boardWithProse(t)
				plant(t, a.Store, findIn(t, arc, id), c.board)
				before := boardBytes(t, a.Dir)

				var err error
				if verb == "archive" {
					_, err = a.ArchiveIDs([]string{id}, false)
				} else {
					_, err = a.Unarchive([]string{id})
				}
				if c.want == "" {
					fe := core.AsError(err)
					if fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "lacks lines") {
						t.Fatalf("%s = %v; want the validation refusal naming the lines the board's body lacks", verb, err)
					}
					if !maps.Equal(before, boardBytes(t, a.Dir)) {
						t.Errorf("the refused %s changed the board", verb)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s must finish the move: %v", verb, err)
				}
				want := prose
				if c.want == "board" {
					want = c.board
				}
				side, other := arc, plantable(a.Store)
				if verb == "unarchive" {
					side, other = a.Store, arc
				}
				got, err := side.LoadBody(id)
				if err != nil || got != want {
					t.Errorf("body after %s = %q (%v); want %q", verb, got, err, want)
				}
				idx, err := other.Load()
				if err != nil {
					t.Fatal(err)
				}
				if idx.Has(id) {
					t.Errorf("the %s left the task in both stores", verb)
				}
			})
		}
	}
}

// An asset the board and archive/ hold under one name with other bytes is a
// clash the copy would settle by writing one over the other: attach on the
// board's copy of a task in both stores checks only the board's assets, so it
// can reuse a name archive/ still holds. Both moves refuse it before writing.
func TestMoveRefusesAnAssetClash(t *testing.T) {
	for _, verb := range []string{"archive", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			a := newFSApp(t)
			tk, err := a.Add("pictured", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			att, err := a.Attach(tk.ID, "shot.png", []byte("the archived bytes"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			body, err := arc.LoadBody(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			plant(t, a.Store, findIn(t, arc, tk.ID), body)
			if err := a.Store.SaveAssetRaw(att.Name, []byte("bytes attached on the board since")); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, a.Dir)

			if verb == "archive" {
				_, err = a.ArchiveIDs([]string{tk.ID}, false)
			} else {
				_, err = a.Unarchive([]string{tk.ID})
			}
			fe := core.AsError(err)
			if fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, core.AssetPath(att.Name)) {
				t.Fatalf("%s = %v; want the validation refusal naming %s", verb, err, core.AssetPath(att.Name))
			}
			if !maps.Equal(before, boardBytes(t, a.Dir)) {
				t.Errorf("the refused %s changed the board", verb)
			}
		})
	}
}

// A finished unarchive keeps the board's copy, so that copy's repos are the
// ones the session guard judges: refused when only the board's copy carries
// a busy repo, let through when only archive/'s does.
func TestUnarchiveFinishingAMoveGuardsTheHotCopy(t *testing.T) {
	for _, c := range []struct {
		name          string
		archived, hot string
		busy          bool
	}{
		{"the board's copy carries the busy repo", "o/other", "o/glyph", true},
		{"only archive/'s copy carries it", "o/glyph", "o/other", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			tk, err := a.Add("retired", AddOpts{Status: "done", Repos: []string{c.archived}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			live := findIn(t, arc, tk.ID)
			live.Repos = []string{c.hot}
			body, err := arc.LoadBody(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			plant(t, a.Store, live, body)

			now := a.Clock.Now()
			glyph := fakeCheckout(t, t.TempDir(), "glyph", "o/glyph")
			a.Sessions = fakeRegistry{sessions: []core.Session{
				{PID: 100, ID: "self", CWD: t.TempDir(), StartedAt: now.Add(-10 * time.Minute), LastActive: now},
				{PID: 1, ID: "occ", Name: "glyph-ab", CWD: glyph, StartedAt: now.Add(-time.Hour), LastActive: now.Add(-time.Minute)},
			}}
			a.Self = SessionRef{PID: 100, ID: "self"}

			_, err = a.Unarchive([]string{tk.ID})
			if c.busy {
				wantSessionBusy(t, err, tk.ID)
			} else if err != nil {
				t.Fatalf("the board's copy touches no busy repo: %v", err)
			}
		})
	}
}
