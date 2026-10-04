package app

import (
	"sort"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// plantDeps writes deps straight to the store, the way a merge of two
// half-edges lands a cycle the write path would refuse — and without going
// through saveIndex, so the App's "before" is still unset.
func plantDeps(t *testing.T, a *App, deps map[string][]string) {
	t.Helper()
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range idx.Tasks {
		if d, ok := deps[idx.Tasks[i].ID]; ok {
			idx.Tasks[i].Deps = d
		}
	}
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
}

func codesOf(ps []core.Problem) map[string]bool {
	out := map[string]bool{}
	for _, p := range ps {
		out[p.Code+" "+p.ID] = true
	}
	return out
}

// A dep-cycle finding is filed under the region's smallest id but is about
// every task in it. Cutting one edge of the knot A↔B↔C leaves B↔C — a region
// now filed under B, which was in the cycle all along: not a new error. A task
// newly drawn into a cycle is.
func TestNewLintErrorsReadsACycleAsItsWholeRegion(t *testing.T) {
	a := newApp()
	ids := addSorted(t, a, 3)
	// ids[0] is the smallest, so cutting it out moves the region's filing id —
	// the case a (code, id) key reads as a new error.
	plantDeps(t, a, map[string][]string{ids[0]: {ids[1]}, ids[1]: {ids[0], ids[2]}, ids[2]: {ids[1]}})
	a.preWrite, a.postWrite = nil, nil

	if _, err := a.RemoveDep(ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	ps, err := a.NewLintErrors()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("shrinking a cycle region created no error, got %v", codesOf(ps))
	}

	b := newApp()
	sorted := addSorted(t, b, 3)
	// p is the LARGEST id, so the grown region keeps its filing id — the case a
	// (code, id) key misses.
	q, r, p := sorted[0], sorted[1], sorted[2]
	// q↔r is a knot and q also waits on p; the write under test closes p→q,
	// drawing p into the region.
	plantDeps(t, b, map[string][]string{q: {r, p}, r: {q}})
	b.preWrite, b.postWrite = nil, nil
	idx, _ := b.Store.Load()
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == p {
			idx.Tasks[i].Deps = []string{q}
		}
	}
	if err := b.saveIndex(idx); err != nil {
		t.Fatal(err)
	}
	if ps, _ = b.NewLintErrors(); len(ps) != 1 || ps[0].Code != "dep-cycle" {
		t.Errorf("a task drawn into a cycle is a new error, got %v", codesOf(ps))
	}
}

// A priority tie is the same kind of region: with priority-duplicate promoted
// to an error, moving the tie's smallest id out leaves the other two tied —
// filed under a new smallest id, but no new error.
func TestNewLintErrorsReadsATieAsItsWholeGroup(t *testing.T) {
	a := newApp()
	a.Cfg.LintSeverity = map[string]string{"priority-duplicate": core.SevError}
	ids := addSorted(t, a, 3) // ids[0], the smallest, is the tie's filing id
	idx, _ := a.Store.Load()
	for i := range idx.Tasks {
		idx.Tasks[i].Priority = 500
	}
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	a.preWrite, a.postWrite = nil, nil

	idx, _ = a.Store.Load()
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == ids[0] {
			idx.Tasks[i].Priority = 900
		}
	}
	if err := a.saveIndex(idx); err != nil {
		t.Fatal(err)
	}
	ps, err := a.NewLintErrors()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 0 {
		t.Errorf("leaving a tie created no error, got %v", codesOf(ps))
	}
}

// addSorted adds n tasks and returns their ids sorted, so a test can pick a
// region's smallest (filing) id on purpose.
func addSorted(t *testing.T, a *App, n int) []string {
	t.Helper()
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, mustAdd(t, a, "task", AddOpts{}).ID)
	}
	sort.Strings(ids)
	return ids
}
