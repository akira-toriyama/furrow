package app

import (
	"fmt"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// loadSpy counts index Loads and can tamper with what a given Load hands back —
// the seam for "did this verb read the board twice, and did it then edit the
// OTHER snapshot?". It wraps a real store (countingStore's LoadBody twin, one
// level up).
type loadSpy struct {
	Store
	loads  int
	onLoad func(n int, idx *core.Index)
}

func (s *loadSpy) Load() (*core.Index, error) {
	idx, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	s.loads++
	if s.onLoad != nil {
		s.onLoad(s.loads, idx)
	}
	return idx, nil
}

func spyApp(t *testing.T) (*App, *loadSpy) {
	t.Helper()
	a := newApp()
	spy := &loadSpy{Store: a.Store}
	a.Store = spy
	return a, spy
}

// seedForEdit creates a task carrying something for each verb to edit.
func seedForEdit(t *testing.T, a *App) string {
	t.Helper()
	tk, err := a.Add("seed", AddOpts{Labels: []string{"keep"}, Repos: []string{"me/x"}, Refs: []string{"a.go:1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddChecks(tk.ID, []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	return tk.ID
}

// Every single-task edit reads the board ONCE. The verbs below validate
// against the task before editing it (a checklist index in range, a relabel that
// must not empty a required set, a repo arg resolved against the board's repo
// universe) and used to do that against one snapshot and then call mutate, which
// loaded a second — so the read was doubled and, worse, the edit landed on a
// snapshot nobody had checked (see TestChecklistVerbsUseTheSnapshotTheyValidated).
func TestSingleTaskEditsLoadTheBoardOnce(t *testing.T) {
	cases := []struct {
		name string
		edit func(a *App, id string) error
	}{
		{"label --add", func(a *App, id string) error { _, err := a.Relabel(id, []string{"bug"}, nil); return err }},
		{"ref --add", func(a *App, id string) error { _, err := a.Reref(id, []string{"b.go:2"}, nil); return err }},
		{"repo --add", func(a *App, id string) error { _, err := a.Rerepo(id, []string{"me/y"}, nil); return err }},
		{"check toggle", func(a *App, id string) error { _, err := a.Check(id, 1, true); return err }},
		{"check --reword", func(a *App, id string) error { _, err := a.RewordCheck(id, 1, "second"); return err }},
		{"check --rm", func(a *App, id string) error { _, err := a.RemoveCheck(id, 1); return err }},
		// Controls: these were already one load, and must stay that way.
		{"move", func(a *App, id string) error { _, err := a.Move(id, "ready"); return err }},
		{"value", func(a *App, id string) error { _, err := a.SetValue(id, intptr(3)); return err }},
		{"retitle", func(a *App, id string) error { _, err := a.Retitle(id, "renamed"); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, st := spyApp(t)
			id := seedForEdit(t, a)
			st.loads = 0
			if err := c.edit(a, id); err != nil {
				t.Fatal(err)
			}
			if st.loads != 1 {
				t.Errorf("the board was loaded %d times, want 1", st.loads)
			}
		})
	}
}

// The checklist verbs range-check `item` and then INDEX with it. Doing those
// against two different reads is an out-of-range panic, not an error, the moment
// a co-writer shortens the list in between — there is no lock in the store. The
// store here shortens the list on any load AFTER the first, so an implementation
// that reads twice edits a one-item list with an index it validated against two.
func TestChecklistVerbsUseTheSnapshotTheyValidated(t *testing.T) {
	cases := []struct {
		name string
		edit func(a *App, id string) error
		want []core.ChecklistItem
	}{
		{"check toggle", func(a *App, id string) error { _, err := a.Check(id, 1, true); return err },
			[]core.ChecklistItem{{Text: "one"}, {Text: "two", Done: true}}},
		{"check --reword", func(a *App, id string) error { _, err := a.RewordCheck(id, 1, "second"); return err },
			[]core.ChecklistItem{{Text: "one"}, {Text: "second"}}},
		{"check --rm", func(a *App, id string) error { _, err := a.RemoveCheck(id, 1); return err },
			[]core.ChecklistItem{{Text: "one"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, st := spyApp(t)
			id := seedForEdit(t, a)
			st.loads = 0
			st.onLoad = func(n int, idx *core.Index) {
				if n < 2 {
					return
				}
				// The concurrent writer: by this read the second item is gone.
				if tk, i := idx.Find(id); i >= 0 && len(tk.Checklist) > 1 {
					tk.Checklist = tk.Checklist[:1]
				}
			}
			// RECOVER rather than letting it crash: against an implementation that
			// reads twice this panics, and a panic takes the whole test binary
			// down with it — every later test in the package goes unjudged, which
			// is exactly what the bite gate reports as "could not judge".
			if err := recovering(func() error { return c.edit(a, id) }); err != nil {
				t.Fatal(err)
			}
			edits := st.loads
			st.onLoad = nil // the read-back below must see the board, not the tamper
			// Belt and braces: the tampering above must never have had a chance to
			// fire. A future refactor that reads twice without panicking would
			// otherwise pass this test while reintroducing the race.
			if edits != 1 {
				t.Fatalf("the edit read the board %d times — validation and application are back on separate snapshots", edits)
			}
			got, _, err := a.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Checklist) != len(c.want) {
				t.Fatalf("checklist = %+v, want %+v", got.Checklist, c.want)
			}
			for i := range c.want {
				if got.Checklist[i] != c.want[i] {
					t.Errorf("checklist[%d] = %+v, want %+v", i, got.Checklist[i], c.want[i])
				}
			}
		})
	}
}

// recovering runs fn and turns a panic into an error naming it. The checklist
// verbs' failure mode IS a panic, and a test for it has to be able to report
// that as a failure rather than abort the run.
func recovering(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the edit panicked — validation and application were on different snapshots: %v", r)
		}
	}()
	return fn()
}

// Repos take the same add/rm algebra as labels and refs, down to how it settles
// a value named in BOTH --add and --rm: remove first, then append the adds not
// already present, so the add wins and lands exactly once. Rerepo wrote that out
// by hand — a second implementation of one rule — so pin that the three agree.
//
// bite-exempt: pins current behaviour, not a fix — the hand-written composition
// this replaces was already semantically identical (verified before folding it),
// so this is the characterisation test that makes a future divergence visible.
func TestRepoDeltaMatchesLabelAndRefDelta(t *testing.T) {
	a, _ := spyApp(t)
	id := seedForEdit(t, a)
	if _, err := a.Rerepo(id, []string{"me/y", "me/x"}, []string{"me/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Relabel(id, []string{"b", "keep"}, []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reref(id, []string{"b.go:2", "a.go:1"}, []string{"a.go:1"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := a.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	// The contested entry survives in all three, exactly once, and the new one is
	// there. Labels and repos are sorted sets; refs keep their user order (the
	// re-added one moves to the end), which is the marshaller's rule, not this
	// helper's.
	if want := []string{"me/x", "me/y"}; !equalStrings(got.Repos, want) {
		t.Errorf("repos = %v, want %v", got.Repos, want)
	}
	if want := []string{"b", "keep"}; !equalStrings(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
	if want := []string{"b.go:2", "a.go:1"}; !equalStrings(got.Refs, want) {
		t.Errorf("refs = %v, want %v", got.Refs, want)
	}
}
