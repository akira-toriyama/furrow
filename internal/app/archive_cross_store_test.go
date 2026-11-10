package app

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
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
