package app

import (
	"crypto/sha256"
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
	"github.com/akira-toriyama/furrow/v10/internal/store/fsstore"
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
			where := "different task in archive/"
			if verb == "unarchive" {
				where = "different task on the hot board"
			}
			if fe == nil || fe.Kind != core.KindValidation || fe.Subject != moving || !strings.Contains(fe.Msg, where) || !strings.Contains(fe.Msg, "duplicate-id") || !strings.Contains(fe.Msg, reIDRepair) {
				t.Fatalf("%s %s = %v; want a validation refusal naming it, the repair with its assets, and lint's duplicate-id", verb, moving, err)
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
// ways out differ: two tasks under one id need a hand repair that renames the
// re-id'd task's assets too, and one task in both is an interrupted move that
// either command finishes, or refuses until a hand merge in the cases the
// move's help names, which the finding points at.
func TestLintFlagsAnIDInBothStores(t *testing.T) {
	for _, c := range []struct {
		name string
		want []string
		hot  func(t *testing.T, arc plantable, id string) (core.Task, string)
	}{
		{"two tasks", []string{"two different tasks", "refuse it until it is repaired: " + reIDRepair}, func(t *testing.T, arc plantable, id string) (core.Task, string) {
			return impostor(findIn(t, arc, id)), "# another task\n"
		}},
		{"one task", []string{"stopped between its two writes", "either refuses instead, naming the hand merge, in the cases `furrow unarchive --help` names"}, func(t *testing.T, arc plantable, id string) (core.Task, string) {
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
			if len(dup) != 1 || dup[0].ID != b.archived || dup[0].Severity != core.SevError {
				t.Fatalf("duplicate-id = %+v; want one error for %s", dup, b.archived)
			}
			for _, want := range c.want {
				if !strings.Contains(dup[0].Msg, want) {
					t.Errorf("duplicate-id = %q; want it to say %q", dup[0].Msg, want)
				}
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

// The mint's archive/ read is a stat, never a parse, so a corrupt shard or a
// newer layout there, which lint and the moves refuse, cannot stop an add,
// and a retired id is still skipped. An archive/ that is not a directory
// holds no shard. Only a stat the OS refuses fails the add: a guard that
// cannot look must not guess.
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
		got, err := a.Add("new", AddOpts{})
		if err == nil {
			t.Fatalf("add minted %s; a probe that cannot look must fail the add, not guess", got.ID)
		}
		if fe := core.AsError(err); fe == nil || fe.Kind != core.KindInternal || fe.Subject != "archive" {
			t.Errorf("add = %v; want an internal error about the archive store, not about a discarded candidate id", err)
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
					if fe == nil || fe.Kind != core.KindValidation || fe.Subject != id || !strings.Contains(fe.Msg, "lacks lines") {
						t.Fatalf("%s = %v; want the validation refusal for %s naming the lines the board's body lacks", verb, err, id)
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

// A finished move's copy of an asset the task owns can meet a different file
// under its name: attach on the board's copy of a task in both stores checks
// only the board's assets, so it can reuse a name archive/ still holds. Both
// moves refuse that before writing. A difference git itself does not see is
// no clash: a text file's line endings, or a git-lfs pointer one copy is left
// as for the file the other holds. Otherwise a binary is compared byte for
// byte.
func TestMoveRefusesAnAssetClash(t *testing.T) {
	lfsPointer := func(content string) string {
		return fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%x\nsize %d\n", sha256.Sum256([]byte(content)), len(content))
	}
	for _, c := range []struct {
		name            string
		archived, board string
		refused         bool
	}{
		{"other content", "the archived bytes", "bytes attached on the board since", true},
		{"text whose line endings alone differ", "line one\nline two\n", "line one\r\nline two\r\n", false},
		{"binary whose line endings alone differ", "\x00\x01line\n", "\x00\x01line\r\n", true},
		{"a git-lfs pointer for the board's file", lfsPointer("\x00PNG board"), "\x00PNG board", false},
		{"a git-lfs pointer for another file of that size", lfsPointer("\x00PNG other"), "\x00PNG board", true},
	} {
		for _, verb := range []string{"archive", "unarchive"} {
			t.Run(c.name+"/"+verb, func(t *testing.T) {
				a := newFSApp(t)
				tk, err := a.Add("pictured", AddOpts{Status: "done"})
				if err != nil {
					t.Fatal(err)
				}
				att, err := a.Attach(tk.ID, "shot.png", []byte(c.archived))
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
				if err := a.Store.SaveAssetRaw(att.Name, []byte(c.board)); err != nil {
					t.Fatal(err)
				}
				before := boardBytes(t, a.Dir)

				if verb == "archive" {
					_, err = a.ArchiveIDs([]string{tk.ID}, false)
				} else {
					_, err = a.Unarchive([]string{tk.ID})
				}
				if !c.refused {
					if err != nil {
						t.Fatalf("%s = %v; a line-ending difference in a text asset is no clash", verb, err)
					}
					return
				}
				fe := core.AsError(err)
				if fe == nil || fe.Kind != core.KindValidation || fe.Subject != att.Name || !strings.Contains(fe.Msg, core.AssetPath(att.Name)) {
					t.Fatalf("%s = %v; want the validation refusal about %s naming %s", verb, err, att.Name, core.AssetPath(att.Name))
				}
				if !maps.Equal(before, boardBytes(t, a.Dir)) {
					t.Errorf("the refused %s changed the board", verb)
				}
			})
		}
	}
}

// While an id sits in both stores its name can hold two files, so the check
// follows the owner, not the call: a move of another task whose body shows
// the file would write one over the other just the same, fresh or finished,
// and so would one under an id that names two tasks. The refusal names the
// way out: the board's copy takes another name, or, under an id that names
// two tasks, one of them takes another id with its assets.
func TestMoveRefusesAClashWhileTheOwnerIsInBothStores(t *testing.T) {
	for _, c := range []struct {
		name     string
		verb     string
		viewerOn string // where the viewer sits before the call: "board", "archive" or "both"
		twoTasks bool   // the board's task under the owner's id is another task
	}{
		{"a fresh archive of a viewer", "archive", "board", false},
		{"a fresh unarchive of a viewer", "unarchive", "archive", false},
		{"a finished archive of a viewer", "archive", "both", false},
		{"a finished unarchive of a viewer", "unarchive", "both", false},
		{"a fresh unarchive of a viewer under an id that names two tasks", "unarchive", "archive", true},
		{"a fresh archive of a viewer under an id that names two tasks", "archive", "board", true},
		{"a finished archive of a viewer under an id that names two tasks", "archive", "both", true},
		{"a finished unarchive of a viewer under an id that names two tasks", "unarchive", "both", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			owner, err := a.Add("owner", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			att, err := a.Attach(owner.ID, "pic.png", []byte("PNG\x00the archived bytes"))
			if err != nil {
				t.Fatal(err)
			}
			viewer, err := a.Add("viewer", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Store.SaveBody(viewer.ID, "# viewer\n\n![pic]("+core.AssetRef(att.Name)+")\n"); err != nil {
				t.Fatal(err)
			}
			retire := []string{owner.ID, viewer.ID}
			if c.viewerOn == "board" {
				retire = retire[:1]
			}
			if _, err := a.ArchiveIDs(retire, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			back := findIn(t, arc, owner.ID) // the owner's shard, brought back by hand
			if c.twoTasks {
				back = impostor(back)
			}
			plantShard(t, a.Store, back)
			if c.viewerOn == "both" {
				body, err := arc.LoadBody(viewer.ID)
				if err != nil {
					t.Fatal(err)
				}
				plant(t, a.Store, findIn(t, arc, viewer.ID), body)
			}
			const since = "PNG\x00bytes attached on the board since"
			if c.viewerOn == "board" {
				// The board kept its copy for the viewer, so attach would pick a
				// new name: the board's copy is replaced by hand instead.
				if err := a.Store.SaveAssetRaw(att.Name, []byte(since)); err != nil {
					t.Fatal(err)
				}
			} else if again, err := a.Attach(owner.ID, "pic.png", []byte(since)); err != nil || again.Name != att.Name {
				t.Fatalf("attach = %+v, %v; want it to reuse %s, the name archive/ holds", again, err, att.Name)
			}
			before := boardBytes(t, a.Dir)

			if c.verb == "archive" {
				_, err = a.ArchiveIDs([]string{viewer.ID}, false)
			} else {
				_, err = a.Unarchive([]string{viewer.ID})
			}
			advice := "add a link to the new name beside the old one"
			if c.twoTasks {
				advice = owner.ID + " names two tasks (`furrow lint` flags it as duplicate-id): " + reIDRepair
			}
			fe := core.AsError(err)
			if fe == nil || fe.Kind != core.KindValidation || fe.Subject != att.Name || !strings.Contains(fe.Msg, advice) {
				t.Fatalf("%s = %v; want the validation refusal about %s advising %q", c.verb, err, att.Name, advice)
			}
			if !maps.Equal(before, boardBytes(t, a.Dir)) {
				t.Errorf("the refused %s changed the board", c.verb)
			}
		})
	}
}

// followClashAdvice does what the asset-clash refusal says for one task in
// both stores: give the board's copy of each name another name, and beside
// each board link to the old name add one to the new. It returns the new
// names.
func followClashAdvice(t *testing.T, a *App, names []string) map[string]string {
	t.Helper()
	renamed := map[string]string{}
	for _, name := range names {
		data, err := a.Store.LoadAsset(name)
		if err != nil {
			t.Fatal(err)
		}
		next := strings.TrimSuffix(name, ".png") + "-2.png"
		if err := a.Store.SaveAssetRaw(next, data); err != nil {
			t.Fatal(err)
		}
		if err := a.Store.DeleteAsset(name); err != nil {
			t.Fatal(err)
		}
		renamed[name] = next
	}
	ids, err := a.Store.ListBodyIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		body, err := a.Store.LoadBody(id)
		if err != nil {
			t.Fatal(err)
		}
		added := body
		for _, name := range names {
			if strings.Contains(body, core.AssetRef(name)) {
				added = strings.TrimRight(added, "\n") + "\n![renamed](" + core.AssetRef(renamed[name]) + ")\n"
			}
		}
		if added != body {
			if err := a.Store.SaveBody(id, added); err != nil {
				t.Fatal(err)
			}
		}
	}
	return renamed
}

// The refusal's way out takes one step, whichever move meets the clash:
// after followClashAdvice the re-run passes and the destination holds both
// files, each under its own name. A viewer's archive then meets a board link
// to the old name with no file behind it, which it has nothing to carry for.
func TestAssetClashAdviceLetsTheReRunKeepBoth(t *testing.T) {
	for _, c := range []struct {
		name   string
		verb   string
		viewer bool // a viewer moves, not the owner
		files  int  // clashing files the owner holds
	}{
		{"a finished archive of the owner", "archive", false, 1},
		{"a finished unarchive of the owner", "unarchive", false, 1},
		{"a finished archive of the owner with two clashing files", "archive", false, 2},
		{"a fresh archive of a viewer", "archive", true, 1},
		{"a fresh unarchive of a viewer", "unarchive", true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			owner, err := a.Add("owner", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for i := range c.files {
				att, err := a.Attach(owner.ID, fmt.Sprintf("f%d.png", i), []byte(fmt.Sprintf("PNG\x00archived %d", i)))
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, att.Name)
			}
			viewer, err := a.Add("viewer", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if c.viewer {
				if err := a.Store.SaveBody(viewer.ID, "# viewer\n\n![f]("+core.AssetRef(names[0])+")\n"); err != nil {
					t.Fatal(err)
				}
			}
			retire := []string{owner.ID}
			if c.viewer && c.verb == "unarchive" {
				retire = append(retire, viewer.ID)
			}
			if _, err := a.ArchiveIDs(retire, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			back := findIn(t, arc, owner.ID) // the owner's shard, brought back by hand
			if c.viewer {
				plantShard(t, a.Store, back)
			} else {
				body, err := arc.LoadBody(owner.ID)
				if err != nil {
					t.Fatal(err)
				}
				plant(t, a.Store, back, body)
			}
			for i, name := range names {
				since := []byte(fmt.Sprintf("PNG\x00board %d", i))
				if c.viewer && c.verb == "archive" {
					// The board kept its copy for the viewer: replaced by hand.
					if err := a.Store.SaveAssetRaw(name, since); err != nil {
						t.Fatal(err)
					}
				} else if again, err := a.Attach(owner.ID, fmt.Sprintf("f%d.png", i), since); err != nil || again.Name != name {
					t.Fatalf("attach = %+v, %v; want it to reuse %s", again, err, name)
				}
			}
			mover := owner.ID
			if c.viewer {
				mover = viewer.ID
			}
			move := func() error {
				if c.verb == "archive" {
					_, err := a.ArchiveIDs([]string{mover}, false)
					return err
				}
				_, err := a.Unarchive([]string{mover})
				return err
			}

			err = move()
			fe := core.AsError(err)
			advice := "rename the board's copy by hand and, in each board body that links it, add a link to the new name beside the old one"
			if c.files > 1 {
				advice = "rename the board's copies by hand and, in each board body that links one, add a link to its new name beside the old one"
			}
			if fe == nil || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, advice) {
				t.Fatalf("%s = %v; want the validation refusal advising %q", c.verb, err, advice)
			}
			renamed := followClashAdvice(t, a, names)
			if err := move(); err != nil {
				t.Fatalf("the re-run after the advice: %v", err)
			}
			var dst assetSide = arc
			if c.verb == "unarchive" {
				dst = a.Store
			}
			for i, name := range names {
				for file, want := range map[string]string{name: fmt.Sprintf("PNG\x00archived %d", i), renamed[name]: fmt.Sprintf("PNG\x00board %d", i)} {
					if got, err := dst.LoadAsset(file); err != nil || string(got) != want {
						t.Errorf("destination's %s = %q (%v); want %q", file, got, err, want)
					}
				}
			}
		})
	}
}

// The asset-clash refusal's advice per owner kind, worded once per owner id,
// both when one call meets both kinds — the one-task advice then naming its
// files: an advice that left one kind out would refuse the re-run again with
// the other.
func TestClashRefusalAdvice(t *testing.T) {
	const one, two, other = "t-aaaaa", "t-bbbbb", "t-ccccc"
	shared := map[string]bool{one: true, two: false, other: false}
	oneTask := "rename the board's copy by hand and, in each board body that links it, add a link to the new name beside the old one"
	for _, c := range []struct {
		name   string
		clash  []string
		want   []string
		reject []string
	}{
		{"one file of one task", []string{one + "-a.png"},
			[]string{"bodies/assets/" + one + "-a.png differs between the board and archive/ under one name", "; " + oneTask + " (a finished move refuses a body that drops a line), then re-run"},
			[]string{"names two tasks", "for bodies/assets/"}},
		{"two files of one task", []string{one + "-a.png", one + "-b.png"},
			[]string{"differ between the board and archive/, each under one name", "rename the board's copies by hand and, in each board body that links one, add a link to its new name beside the old one"},
			[]string{"names two tasks"}},
		{"two files under an id that names two tasks", []string{two + "-a.png", two + "-b.png"},
			[]string{"; " + two + " names two tasks (`furrow lint` flags it as duplicate-id): " + reIDRepair + ", then re-run"},
			[]string{"each name two tasks", "rename the board's cop"}},
		{"two ids that name two tasks", []string{two + "-a.png", other + "-a.png"},
			[]string{"; " + two + ", " + other + " each name two tasks (`furrow lint` flags them as duplicate-id): for each, " + reIDRepair},
			[]string{"rename the board's cop"}},
		{"one task and two tasks in one call", []string{one + "-a.png", two + "-a.png"},
			[]string{"; for bodies/assets/" + one + "-a.png, " + oneTask, "; and " + two + " names two tasks (`furrow lint` flags it as duplicate-id): " + reIDRepair + ", then re-run"},
			nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			fe := core.AsError(clashRefusal(c.clash, shared, "restored"))
			if fe == nil || fe.Kind != core.KindValidation || fe.Subject != c.clash[0] || !strings.Contains(fe.Msg, "so nothing was restored") {
				t.Fatalf("clashRefusal = %v; want a validation refusal about %s", fe, c.clash[0])
			}
			for _, w := range c.want {
				if !strings.Contains(fe.Msg, w) {
					t.Errorf("message lacks %q:\n%s", w, fe.Msg)
				}
			}
			for _, r := range c.reject {
				if strings.Contains(fe.Msg, r) {
					t.Errorf("message has %q:\n%s", r, fe.Msg)
				}
			}
		})
	}
}

// The id repair names everything a re-id has to carry; each part is a way a
// shorter repair failed (reIDRepair's doc), so the wording itself is pinned.
// Several clashing ids get it once, for each.
func TestIDRepairWording(t *testing.T) {
	for _, w := range []string{
		"give one of them its own id by hand, carrying everything that names it:",
		"its shard, body and `<id>-` assets",
		"every dep, `[[id]]` link and asset link that means it",
		"in both copies of a task that sits in both stores",
	} {
		if !strings.Contains(reIDRepair, w) {
			t.Errorf("reIDRepair lacks %q", w)
		}
	}
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	held := []core.Task{{ID: "t-aaaaa", Created: now}, {ID: "t-bbbbb", Created: now}}
	moving := []core.Task{{ID: "t-bbbbb", Created: now.Add(time.Hour)}, {ID: "t-aaaaa", Created: now.Add(time.Hour)}}
	fe := core.AsError(refuseIDClash(moving, &core.Index{Tasks: held}, "in archive/", "archived"))
	want := "task ids t-aaaaa, t-bbbbb already name a different task in archive/ — the move would write one over the other, so nothing was archived; for each, " + reIDRepair + " — `furrow lint` flags them as duplicate-id"
	if fe == nil || fe.Kind != core.KindValidation || fe.Subject != "t-aaaaa" || fe.Msg != want {
		t.Errorf("refuseIDClash = %v;\nwant %q", fe, want)
	}
}

// The body-loss refusal is worded for one id or several.
func TestBodyLossRefusalWording(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{[]string{"t-aaaaa"}, "the board's body of t-aaaaa lacks lines archive/'s copy has, its title aside — "},
		{[]string{"t-bbbbb", "t-aaaaa"}, "the board's bodies of t-aaaaa, t-bbbbb lack lines archive/'s copies have, their titles aside — "},
	} {
		if fe := core.AsError(refuseBodyLoss(c.ids, "archived")); fe == nil || !strings.Contains(fe.Msg, c.want) {
			t.Errorf("refuseBodyLoss(%v) = %v; want it to say %q", c.ids, fe, c.want)
		}
	}
}

// reIDArchived does the repair reIDRepair asks for, on archive/'s side: the
// task under old takes the id next, carrying its shard, body and old- assets,
// and every archive/ body's link to them or to [[old]].
func reIDArchived(t *testing.T, arc *fsstore.Store, old, next string) {
	t.Helper()
	idx, err := arc.Load()
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := idx.Find(old)
	if tk == nil {
		t.Fatalf("%s is not in archive/", old)
	}
	tk.ID = next
	if err := arc.Save(idx); err != nil {
		t.Fatal(err)
	}
	body, err := arc.LoadBody(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := arc.SaveBody(next, body); err != nil {
		t.Fatal(err)
	}
	if err := arc.DeleteBody(old); err != nil {
		t.Fatal(err)
	}
	assets, err := arc.ListAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, as := range assets {
		if !strings.HasPrefix(as.Name, old+"-") {
			continue
		}
		data, err := arc.LoadAsset(as.Name)
		if err != nil {
			t.Fatal(err)
		}
		if err := arc.SaveAssetRaw(next+strings.TrimPrefix(as.Name, old), data); err != nil {
			t.Fatal(err)
		}
		if err := arc.DeleteAsset(as.Name); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := arc.ListBodyIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		b, err := arc.LoadBody(id)
		if err != nil {
			t.Fatal(err)
		}
		if nb := strings.NewReplacer("assets/"+old+"-", "assets/"+next+"-", "[["+old+"]]", "[["+next+"]]").Replace(b); nb != b {
			if err := arc.SaveBody(id, nb); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Under an id that names two tasks, the repair the refusal asks for keeps
// both files: once archive/'s task carries its own id, its file goes with it,
// the guard has nothing left to refuse, and the viewer's restore brings that
// file back under the new name beside the board task's own.
func TestTwoTaskAdviceLetsTheReRunKeepBoth(t *testing.T) {
	a := newFSApp(t)
	owner, err := a.Add("owner", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := a.Attach(owner.ID, "pic.png", []byte("PNG\x00the archived task's"))
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := a.Add("viewer", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody(viewer.ID, "# viewer\n\n![pic]("+core.AssetRef(att.Name)+") of [["+owner.ID+"]]\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{owner.ID, viewer.ID}, false); err != nil {
		t.Fatal(err)
	}
	arc, err := a.archiveStore()
	if err != nil {
		t.Fatal(err)
	}
	plantShard(t, a.Store, impostor(findIn(t, arc, owner.ID)))
	if again, err := a.Attach(owner.ID, "pic.png", []byte("PNG\x00the board task's")); err != nil || again.Name != att.Name {
		t.Fatalf("attach = %+v, %v; want it to reuse %s", again, err, att.Name)
	}
	if _, err := a.Unarchive([]string{viewer.ID}); !strings.Contains(fmt.Sprint(err), reIDRepair) {
		t.Fatalf("unarchive = %v; want the refusal asking for the id repair", err)
	}

	const next = "t-zzzz9"
	reIDArchived(t, arc, owner.ID, next)
	if dup := problemsWithCode(mustLint(t, a), "duplicate-id"); len(dup) != 0 {
		t.Fatalf("duplicate-id after the repair = %+v", dup)
	}
	if _, err := a.Unarchive([]string{viewer.ID}); err != nil {
		t.Fatalf("the re-run after the repair: %v", err)
	}
	moved := next + strings.TrimPrefix(att.Name, owner.ID)
	for file, want := range map[string]string{att.Name: "PNG\x00the board task's", moved: "PNG\x00the archived task's"} {
		if got, err := a.Store.LoadAsset(file); err != nil || string(got) != want {
			t.Errorf("the board's %s = %q (%v); want %q", file, got, err, want)
		}
	}
	if body, err := a.Store.LoadBody(viewer.ID); err != nil || !strings.Contains(body, core.AssetRef(moved)) || !strings.Contains(body, "[["+next+"]]") {
		t.Errorf("the viewer's body = %q (%v); want its links on %s", body, err, next)
	}
}

// An asset only the source holds is no clash, so a finished move copies it
// across: one attached on the board's copy since, or one left behind when the
// shard and body were copied back by hand.
//
// bite-exempt: the base copied it too; it pins the asset check's pass over a
// destination that lacks the file.
func TestFinishedMoveCopiesAnAssetOnlyTheSourceHolds(t *testing.T) {
	for _, verb := range []string{"archive", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			a := newFSApp(t)
			tk, err := a.Add("pictured", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			want, name := "the archived bytes", ""
			if verb == "unarchive" {
				att, err := a.Attach(tk.ID, "shot.png", []byte(want))
				if err != nil {
					t.Fatal(err)
				}
				name = att.Name
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
			plant(t, a.Store, findIn(t, arc, tk.ID), body) // copied back by hand, the assets left behind
			var dst assetSide = a.Store
			if verb == "archive" {
				want, dst = "bytes attached on the board since", arc
				att, err := a.Attach(tk.ID, "shot.png", []byte(want))
				if err != nil {
					t.Fatal(err)
				}
				name = att.Name
			}

			if verb == "archive" {
				_, err = a.ArchiveIDs([]string{tk.ID}, false)
			} else {
				_, err = a.Unarchive([]string{tk.ID})
			}
			if err != nil {
				t.Fatalf("a finished %s of %s: %v", verb, tk.ID, err)
			}
			if got, err := dst.LoadAsset(name); err != nil || string(got) != want {
				t.Errorf("destination's %s = %q (%v); want %q", name, got, err, want)
			}
		})
	}
}

// A finished move of a task whose body shows another task's file writes over
// the destination's copy as a fresh move does, while that owner sits in one
// store: the pair is one file twice, whatever a hand did to one copy.
//
// bite-exempt: it pins the base's behavior, which the asset check briefly
// refused on every move.
func TestFinishedMoveCopiesAHeldAssetAsBefore(t *testing.T) {
	for _, verb := range []string{"archive", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			a := newFSApp(t)
			owner, err := a.Add("owner", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			att, err := a.Attach(owner.ID, "pic.png", []byte("the first bytes"))
			if err != nil {
				t.Fatal(err)
			}
			viewer, err := a.Add("viewer", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Store.SaveBody(viewer.ID, "# viewer\n\n![pic]("+core.AssetRef(att.Name)+")\n"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.ArchiveIDs([]string{viewer.ID}, false); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			body, err := arc.LoadBody(viewer.ID)
			if err != nil {
				t.Fatal(err)
			}
			plant(t, a.Store, findIn(t, arc, viewer.ID), body) // the viewer's interrupted move
			var src, dst assetSide = a.Store, arc
			if verb == "unarchive" {
				src, dst = arc, a.Store
			}
			const replaced = "bytes replaced by hand"
			if err := src.SaveAssetRaw(att.Name, []byte(replaced)); err != nil {
				t.Fatal(err)
			}

			if verb == "archive" {
				_, err = a.ArchiveIDs([]string{viewer.ID}, false)
			} else {
				_, err = a.Unarchive([]string{viewer.ID})
			}
			if err != nil {
				t.Fatalf("the owner sits in one store, so nothing can clash: %v", err)
			}
			if got, err := dst.LoadAsset(att.Name); err != nil || string(got) != replaced {
				t.Errorf("destination's %s = %q (%v); want the moving side's %q", att.Name, got, err, replaced)
			}
		})
	}
}

// A fresh move meets an asset already in the destination only as the copy
// another body there holds, and keeps writing over it as it always did, even
// when one side's file was replaced by hand: its owner sits in one store, so
// the pair is one file twice.
//
// bite-exempt: it pins the base's behavior, which the asset check briefly
// refused on every move.
func TestFreshMoveCopiesAHeldAssetAsBefore(t *testing.T) {
	for _, verb := range []string{"archive", "unarchive"} {
		t.Run(verb, func(t *testing.T) {
			a := newFSApp(t)
			owner, err := a.Add("owner", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			att, err := a.Attach(owner.ID, "shot.png", []byte("the first bytes"))
			if err != nil {
				t.Fatal(err)
			}
			viewer, err := a.Add("viewer", AddOpts{Status: "done"})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Store.SaveBody(viewer.ID, "# viewer\n\n![shot]("+core.AssetRef(att.Name)+")\n"); err != nil {
				t.Fatal(err)
			}
			arc, err := a.archiveStore()
			if err != nil {
				t.Fatal(err)
			}
			// One store keeps the owner's file for the viewer's body; the other
			// takes a copy. Then the moving side's copy is replaced by hand.
			first, moving := viewer.ID, owner.ID
			var src assetSide = a.Store
			if verb == "unarchive" {
				first, src = owner.ID, arc
			}
			if _, err := a.ArchiveIDs([]string{first}, false); err != nil {
				t.Fatal(err)
			}
			const replaced = "bytes replaced by hand"
			if err := src.SaveAssetRaw(att.Name, []byte(replaced)); err != nil {
				t.Fatal(err)
			}

			var dst assetSide = arc
			if verb == "archive" {
				_, err = a.ArchiveIDs([]string{moving}, false)
			} else {
				dst = a.Store
				_, err = a.Unarchive([]string{moving})
			}
			if err != nil {
				t.Fatalf("a fresh %s of %s: %v", verb, moving, err)
			}
			if got, err := dst.LoadAsset(att.Name); err != nil || string(got) != replaced {
				t.Errorf("destination's %s = %q (%v); want the moving side's %q", att.Name, got, err, replaced)
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

// The body rule a finished move applies: the board's body wins while it keeps
// every line of archive/'s, its title heading aside, as often as archive/'s
// has it, wherever it sits; a blank board body yields archive/'s. An
// occurrence's back-link is its only tie to its series and counts.
func TestFinishedBodyRule(t *testing.T) {
	const prose = "# prosy\n\nline one\nline two\n"
	const occurrence = "previous: [[t-aaaaa]]\n\n# weekly review\n"
	for _, c := range []struct {
		name, board, archived string
		holds                 bool
	}{
		{"the same prose", prose, prose, true},
		{"CRLF and trailing spaces", "# prosy\r\n\r\nline one  \r\nline two\t\r\n", prose, true},
		{"CRLF and trailing spaces on archive/'s side", prose, "# prosy\r\n\r\nline one  \r\nline two\t\r\n", true},
		{"a reorder", "# prosy\n\nline two\nline one\n", prose, true},
		{"a note and an attach link added", "# prosy\n\n- a later note\n\nline one\nline two\n![shot](assets/t-x-shot.png)\n", prose, true},
		{"a retitle", "# renamed\n\nline one\nline two\n", prose, true},
		{"a retitled occurrence keeps its back-link", "previous: [[t-aaaaa]]\n\n# renamed\n", occurrence, true},
		{"a note on a body-less occurrence drops its back-link", "a later note\n", occurrence, false},
		{"a repeated line held once", "# prosy\n\n- same\n", "# prosy\n\n- same\n- same\n", false},
		{"a dropped line", "# prosy\n\nline one\n", prose, false},
		{"no heading, so every line counts", "line two\n", "line one\nline two\n", false},
		{"an H1 that is not the first line counts", "intro\n", "intro\n# Section\n", false},
		{"an H1 below the title counts", "# prosy\n\nship it\n", "# prosy\n\n# Decision\nship it\n", false},
		{"a ## first line is no title", "line\n", "## Context\nline\n", false},
		{"a # with no space is no title", "line\n", "#123 follow-up\nline\n", false},
		{"an indented line is another line", "# prosy\n\n- item\n", "# prosy\n\n  - item\n", false},
		{"an indent on the board's side is another line too", "# prosy\n\n  - item\n", "# prosy\n\n- item\n", false},
		{"a heading-only archive/ body", "anything at all\n", "# prosy\n", true},
	} {
		if got := holdsProse(c.board, c.archived); got != c.holds {
			t.Errorf("%s: holdsProse = %v, want %v", c.name, got, c.holds)
		}
	}
	for _, c := range []struct {
		name, board  string
		useBoard, ok bool
	}{
		{"a missing or empty board body", "", false, true},
		{"a whitespace-only board body", " \n\t\n", false, true},
		{"a board body lacking archive/'s lines", "a later note\n", true, false},
		{"a board body holding them", "# renamed\n\nline one\nline two\n", true, true},
	} {
		if useBoard, ok := pickFinishedBody(c.board, prose); useBoard != c.useBoard || ok != c.ok {
			t.Errorf("%s: pickFinishedBody = (%v, %v), want (%v, %v)", c.name, useBoard, ok, c.useBoard, c.ok)
		}
	}
}
