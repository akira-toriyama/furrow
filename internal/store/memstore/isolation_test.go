package memstore

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Load's isolation promise, made TRUE: mutating what Load returned — including
// in place through slice backing arrays and pointer fields — must not change
// the store. The old shallow copy shared every slice, so the routine
// Load -> Canonicalize (sortDedup sorts IN PLACE) rewrote the store with no
// Save — the double promising less than fsstore delivers.
func TestLoadIsDeeplyIsolated(t *testing.T) {
	s := New("t-", "e-", 5)
	v := 4
	closed := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	seed := &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{{
		ID: "t-00001", Title: "seeded", Status: "inbox",
		Labels: []string{"b", "a"}, Repos: []string{"me/x"}, Deps: []string{"t-00002"},
		Refs: []string{"a.go:1"}, Checklist: []core.ChecklistItem{{Text: "one"}},
		Value: &v, Closed: &closed,
	}}}
	if err := s.Save(seed); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	tk := &got.Tasks[0]
	// The original probe was an in-place SORT (Load -> Canonicalize is the routine
	// that regressed). That is vacuous now that Save canonicalizes: the store
	// already holds ["a","b"], so sorting the loaded copy writes nothing back even
	// through a shared array. Keep the call — it asserts the loaded set really is
	// canonical — and write an ELEMENT for the leak probe: same class, still
	// detectable.
	sort.Strings(tk.Labels)
	tk.Labels[0] = "evil"
	tk.Repos[0] = "evil/overwrite"
	tk.Deps = append(tk.Deps[:0], "t-99999")
	tk.Checklist[0].Done = true
	*tk.Value = 1
	*tk.Closed = closed.AddDate(1, 0, 0)

	again, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	fresh := again.Tasks[0]
	// ["b","a"] went IN; Save canonicalized it, so the store holds ["a","b"] and
	// the caller's element write must not have reached it.
	if !reflect.DeepEqual(fresh.Labels, []string{"a", "b"}) {
		t.Errorf("labels leaked a caller's in-place write: %v", fresh.Labels)
	}
	if fresh.Repos[0] != "me/x" || fresh.Deps[0] != "t-00002" || fresh.Checklist[0].Done {
		t.Errorf("slice mutation leaked into the store: %+v", fresh)
	}
	if *fresh.Value != 4 || !fresh.Closed.Equal(closed) {
		t.Errorf("pointer mutation leaked into the store: value=%d closed=%v", *fresh.Value, fresh.Closed)
	}
	// Nil-ness is preserved: the clone must not turn an absent set into [] or
	// vice versa (DeepEqual-based tests would notice).
	if fresh.Effort != nil || fresh.Due != nil {
		t.Errorf("clone invented values: %+v", fresh)
	}
}

// Save is isolated in the other direction too (fsstore serializes, so it is):
// a caller that keeps mutating the saved index must not reach the store.
func TestSaveIsDeeplyIsolated(t *testing.T) {
	s := New("t-", "e-", 5)
	idx := &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{{
		ID: "t-00001", Title: "seeded", Status: "inbox", Labels: []string{"keep"},
	}}}
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	idx.Tasks[0].Labels[0] = "mutated-after-save"

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks[0].Labels[0] != "keep" {
		t.Errorf("post-Save mutation leaked into the store: %v", got.Tasks[0].Labels)
	}
}

// The epic reads carry the same promise (labels/repos/deps slices + the Meta
// map + the closed pointer).
func TestEpicLoadsAreDeeplyIsolated(t *testing.T) {
	s := New("t-", "e-", 5)
	e := &core.Epic{ID: "e-00001", Title: "box", Labels: []string{"l"},
		Repos: []string{"me/x"}, Deps: []string{}, Meta: map[string]string{"k": "v"},
		Body: "bodies/e-00001.md"}
	if err := s.SaveEpic(e); err != nil {
		t.Fatal(err)
	}

	one, ok, err := s.LoadEpic("e-00001")
	if err != nil || !ok {
		t.Fatalf("LoadEpic: %v ok=%v", err, ok)
	}
	one.Labels[0] = "evil"
	one.Meta["k"] = "evil"

	all, err := s.LoadEpics()
	if err != nil {
		t.Fatal(err)
	}
	if all[0].Labels[0] != "l" || all[0].Meta["k"] != "v" {
		t.Errorf("epic mutation leaked into the store: %+v", all[0])
	}
	all[0].Repos[0] = "evil/x"
	fresh, _, _ := s.LoadEpic("e-00001")
	if fresh.Repos[0] != "me/x" {
		t.Errorf("LoadEpics mutation leaked into the store: %+v", fresh)
	}
}
