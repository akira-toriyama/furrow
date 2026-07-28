package fsstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func epicStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".furrow")
	return New(root, gateLanes, "t-", "e-", 5), root
}

func sampleEpic(id, title string) *core.Epic {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	return &core.Epic{
		ID:      id,
		Title:   title,
		Goal:    "done when the pamphlet is printed",
		Labels:  []string{"travel"},
		Repos:   []string{"akira-toriyama/furrow"},
		Meta:    map[string]string{"place": "Hokkaido"},
		Created: now,
		Updated: now,
		Body:    "bodies/" + id + ".md",
	}
}

// A saved epic round-trips by id, and its shard lands at epics/<id>.json — the
// path the store owns and callers never assemble.
func TestSaveAndLoadEpic(t *testing.T) {
	s, root := epicStore(t)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}); err != nil {
		t.Fatal(err) // stamp meta.json so the write gate is satisfied
	}
	want := sampleEpic("e-k3m9", "旅行の準備")
	if err := s.SaveEpic(want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "epics", "e-k3m9.json")); err != nil {
		t.Fatalf("epic shard not at epics/<id>.json: %v", err)
	}
	got, ok, err := s.LoadEpic("e-k3m9")
	if err != nil || !ok {
		t.Fatalf("LoadEpic ok=%v err=%v", ok, err)
	}
	if got.Title != "旅行の準備" || got.Goal != want.Goal || got.Meta["place"] != "Hokkaido" {
		t.Errorf("round-trip lost data: %+v", got)
	}
}

// An absent epic is ok=false, not an error — the same contract LoadRepo has, so
// "never declared" is a state the caller branches on rather than an exception.
func TestLoadEpicAbsentIsNotAnError(t *testing.T) {
	s, _ := epicStore(t)
	e, ok, err := s.LoadEpic("e-nope")
	if err != nil {
		t.Fatalf("absent epic must not error: %v", err)
	}
	if ok || e != nil {
		t.Errorf("absent epic must be ok=false, got %v %+v", ok, e)
	}
}

// A board with no epics/ dir yields nil, not an error: a board that has never
// declared a box is legitimate, and LoadEpics runs on every read.
func TestLoadEpicsOnBoardWithoutEpicsDir(t *testing.T) {
	s, _ := epicStore(t)
	epics, err := s.LoadEpics()
	if err != nil {
		t.Fatalf("missing epics/ must not error: %v", err)
	}
	if epics != nil {
		t.Errorf("want nil, got %+v", epics)
	}
	ids, err := s.ListEpicIDs()
	if err != nil || ids != nil {
		t.Errorf("ListEpicIDs on a boxless board = %v, %v; want nil, nil", ids, err)
	}
}

// LoadEpics and ListEpicIDs are both sorted by id, so every read that folds them
// (epic ls, next's active lookup, lint) is deterministic regardless of the
// filesystem's directory order.
func TestLoadEpicsSortedByID(t *testing.T) {
	s, _ := epicStore(t)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e-zzz9", "e-aaa1", "e-mmm5"} {
		if err := s.SaveEpic(sampleEpic(id, id)); err != nil {
			t.Fatal(err)
		}
	}
	epics, err := s.LoadEpics()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(epics))
	for i, e := range epics {
		got[i] = e.ID
	}
	if strings.Join(got, ",") != "e-aaa1,e-mmm5,e-zzz9" {
		t.Errorf("LoadEpics not sorted by id: %v", got)
	}
	ids, err := s.ListEpicIDs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "e-aaa1,e-mmm5,e-zzz9" {
		t.Errorf("ListEpicIDs not sorted: %v", ids)
	}
}

// Re-saving an untouched epic must not rewrite the file: fsstore's zero-churn
// byte comparison has to cover the newest shard kind too, or every `furrow sync`
// would churn every epic shard in git.
func TestSaveEpicIsZeroChurn(t *testing.T) {
	s, root := epicStore(t)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	e := sampleEpic("e-k3m9", "box")
	if err := s.SaveEpic(e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "epics", "e-k3m9.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A re-save of identical content must leave the file — and its mtime — alone.
	time.Sleep(10 * time.Millisecond)
	if err := s.SaveEpic(sampleEpic("e-k3m9", "box")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("unchanged epic was rewritten (mtime moved %v -> %v)", before.ModTime(), after.ModTime())
	}
}

// The write gate covers epics. A board declaring an older layout is READABLE but
// read-only, and that has to include the newest shard kind — a gate with a hole
// in it would let a stale board be mutated through epics/ instead of tasks/.
func TestSaveEpicRefusedOnOutdatedBoard(t *testing.T) {
	s, root := epicStore(t)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"), []byte("{\n  \"schema_version\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := s.SaveEpic(sampleEpic("e-k3m9", "box"))
	if err == nil {
		t.Fatal("SaveEpic on an outdated board must be refused")
	}
	var fe *core.Error
	if !errors.As(err, &fe) || fe.ID != "schema-upgrade-required" {
		t.Errorf("SaveEpic error = %v, want id schema-upgrade-required", err)
	}
	// And reading still works — read-only means read-ONLY, not unusable.
	if _, err := s.LoadEpics(); err != nil {
		t.Errorf("an outdated board must still be readable: %v", err)
	}
}

// NextEpicID uses the epic prefix, not the task one. That is the property the
// shared bodies/ directory rests on: an id names its entity kind on sight.
func TestNextEpicIDUsesTheEpicPrefix(t *testing.T) {
	s, _ := epicStore(t)
	id, err := s.NextEpicID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "e-") {
		t.Errorf("NextEpicID = %q, want an e- prefix", id)
	}
	if len(id) != len("e-")+5 {
		t.Errorf("NextEpicID = %q, want %d chars of suffix", id, 5)
	}
	other, err := s.NextEpicID()
	if err != nil {
		t.Fatal(err)
	}
	if id == other {
		t.Errorf("two NextEpicID calls returned the same id %q", id)
	}
}
