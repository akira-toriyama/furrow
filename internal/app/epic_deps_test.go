package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// mustEpicDone closes a box, failing the test on error.
func mustEpicDone(t *testing.T, a *App, id string) {
	t.Helper()
	if _, _, err := a.EpicDone(id); err != nil {
		t.Fatalf("epic done %s: %v", id, err)
	}
}

// The write funnel's contract: variadic, all-or-nothing, acyclic, idempotent —
// the task-side AddDeps contract carried over to boxes.
func TestEpicAddDeps(t *testing.T) {
	a := newApp()
	moving := mustEpic(t, a, "moving", EpicAddOpts{Repos: []string{"o/r"}})
	furniture := mustEpic(t, a, "buy furniture", EpicAddOpts{Repos: []string{"o/r"}})
	walk := mustEpic(t, a, "walk the new neighborhood", EpicAddOpts{Repos: []string{"o/r"}})

	if _, after, err := a.EpicAddDeps(walk, []string{moving, furniture}); err != nil {
		t.Fatalf("add deps: %v", err)
	} else if len(after.Deps) != 2 {
		t.Fatalf("want 2 deps, got %v", after.Deps)
	}
	if _, after, err := a.EpicAddDeps(walk, []string{moving}); err != nil {
		t.Fatalf("re-add must be a no-op: %v", err)
	} else if len(after.Deps) != 2 {
		t.Errorf("re-add grew the set: %v", after.Deps)
	}

	// Self and cycle are refused before anything is written.
	if _, _, err := a.EpicAddDeps(walk, []string{walk}); err == nil {
		t.Error("a box must not depend on itself")
	}
	if _, _, err := a.EpicAddDeps(moving, []string{walk}); err == nil {
		t.Error("the closing edge of a cycle must be refused")
	}
	// The in-batch half: an edge added earlier in the SAME call counts.
	third := mustEpic(t, a, "third", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(third, []string{walk, moving}); err != nil {
		t.Fatalf("legit batch: %v", err)
	}
	if _, _, err := a.EpicAddDeps(furniture, []string{third}); err == nil {
		t.Error("a cycle through a batch-added edge must be refused")
	}

	// An unknown dep is exit-2 material with candidates (the epic resolver).
	_, _, err := a.EpicAddDeps(walk, []string{"e-nope"})
	var fe *core.Error
	if err == nil || !errors.As(err, &fe) || fe.Code != core.CodeValidation {
		t.Fatalf("an unknown dep must be a validation error, got %v", err)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("the unknown-dep error must carry candidates, got %+v", fe)
	}
}

func TestEpicRemoveDeps(t *testing.T) {
	a := newApp()
	first := mustEpic(t, a, "first", EpicAddOpts{Repos: []string{"o/r"}})
	second := mustEpic(t, a, "second", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(second, []string{first}); err != nil {
		t.Fatal(err)
	}
	// Removing a non-dep is an error naming it, never a silent no-op.
	if _, _, err := a.EpicRemoveDeps(first, []string{second}); err == nil {
		t.Error("removing an absent dep must fail")
	}
	if _, after, err := a.EpicRemoveDeps(second, []string{first}); err != nil {
		t.Fatalf("remove: %v", err)
	} else if len(after.Deps) != 0 {
		t.Errorf("dep not removed: %v", after.Deps)
	}
}

// A dangling edge (its epic gone from the store) must be removable by its
// LITERAL id — it no longer resolves, and requiring resolution would make
// epic-dep-missing unfixable from the CLI.
func TestEpicRemoveDepsAcceptsDanglingLiteral(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	dep := mustEpic(t, a, "doomed dep", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(box, []string{dep}); err != nil {
		t.Fatal(err)
	}
	// Simulate the dangle the way it happens in the wild: the dep's shard is
	// hand-deleted / lost in a merge. memstore has no delete, so write the
	// edge onto a box whose dep never existed via the store directly.
	e, ok, err := a.Store.LoadEpic(box)
	if err != nil || !ok {
		t.Fatal(err)
	}
	e.Deps = []string{"e-gone"}
	if err := a.Store.SaveEpic(e); err != nil {
		t.Fatal(err)
	}
	if _, after, err := a.EpicRemoveDeps(box, []string{"e-gone"}); err != nil {
		t.Fatalf("a dangling edge must be removable by literal id: %v", err)
	} else if len(after.Deps) != 0 {
		t.Errorf("dangling edge not removed: %v", after.Deps)
	}
}

func TestEpicDepList(t *testing.T) {
	a := newApp()
	moving := mustEpic(t, a, "moving", EpicAddOpts{Repos: []string{"o/r"}})
	furniture := mustEpic(t, a, "buy furniture", EpicAddOpts{Repos: []string{"o/r"}})
	appliances := mustEpic(t, a, "buy appliances", EpicAddOpts{Repos: []string{"o/r"}})
	for _, id := range []string{furniture, appliances} {
		if _, _, err := a.EpicAddDeps(id, []string{moving}); err != nil {
			t.Fatal(err)
		}
	}
	mustEpicDone(t, a, moving)

	res, err := a.EpicDepList(furniture)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn) != 1 || res.DependsOn[0].ID != moving || res.DependsOn[0].State != "closed" {
		t.Errorf("depends_on must resolve the closed dep: %+v", res.DependsOn)
	}
	rev, err := a.EpicDepList(moving)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Blocks) != 2 {
		t.Fatalf("moving must block both branches, got %+v", rev.Blocks)
	}
	if rev.Blocks[0].ID > rev.Blocks[1].ID {
		t.Errorf("blocks must be sorted: %+v", rev.Blocks)
	}
}

// Activate on a box with open deps WARNS (returns them) and proceeds — the
// ratified semantics: the edge is information, furrow never refuses the human's
// choice of box.
func TestEpicActivateWarnsOnOpenDeps(t *testing.T) {
	a := newApp()
	moving := mustEpic(t, a, "moving", EpicAddOpts{Repos: []string{"o/r"}})
	furniture := mustEpic(t, a, "buy furniture", EpicAddOpts{Repos: []string{"o/other"}})
	closedDep := mustEpic(t, a, "done box", EpicAddOpts{Repos: []string{"o/r2"}})
	mustEpicDone(t, a, closedDep)
	if _, _, err := a.EpicAddDeps(furniture, []string{moving, closedDep}); err != nil {
		t.Fatal(err)
	}

	_, after, openDeps, err := a.EpicActivate(furniture, "")
	if err != nil {
		t.Fatalf("activate must proceed despite open deps: %v", err)
	}
	if !after.Active {
		t.Error("the box must actually be active")
	}
	if len(openDeps) != 1 || openDeps[0] != moving {
		t.Errorf("openDeps must name exactly the still-open dep: %v", openDeps)
	}
}

// The box-level revisit signal: a PARKED box whose deps are all closed raises
// epic_dep_done; the active box and a box with an open dep stay silent.
func TestRevisitEpicDepDone(t *testing.T) {
	a := newApp()
	moving := mustEpic(t, a, "moving", EpicAddOpts{Repos: []string{"o/r"}})
	furniture := mustEpic(t, a, "buy furniture", EpicAddOpts{Repos: []string{"o/r"}})
	walk := mustEpic(t, a, "walk", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(furniture, []string{moving}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicAddDeps(walk, []string{furniture}); err != nil {
		t.Fatal(err)
	}

	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicDepDone) != 0 {
		t.Fatalf("no dep closed yet — epic_dep_done must be empty, got %v", sum.EpicDepDone)
	}

	mustEpicDone(t, a, moving)
	sum, err = a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicDepDone) != 1 || sum.EpicDepDone[0] != furniture {
		t.Fatalf("furniture's turn to open: want [%s], got %v", furniture, sum.EpicDepDone)
	}

	// Once ACTIVE, the nudge stops — there is nothing left to open.
	mustActivate(t, a, furniture)
	sum, err = a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicDepDone) != 0 {
		t.Errorf("an active box must not be nudged to open: %v", sum.EpicDepDone)
	}
}

// epic ls surfaces the unsatisfied edges as OpenDeps, and epic show resolves
// the dep set to titles+states.
func TestEpicListAndShowSurfaceDeps(t *testing.T) {
	a := newApp()
	moving := mustEpic(t, a, "moving", EpicAddOpts{Repos: []string{"o/r"}})
	furniture := mustEpic(t, a, "buy furniture", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(furniture, []string{moving}); err != nil {
		t.Fatal(err)
	}

	items, err := a.EpicList(EpicQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var row *EpicItem
	for i := range items {
		if items[i].Epic.ID == furniture {
			row = &items[i]
		}
	}
	if row == nil || len(row.OpenDeps) != 1 || row.OpenDeps[0] != moving {
		t.Errorf("epic ls must surface the open dep, got %+v", row)
	}

	d, err := a.EpicShow(furniture)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Deps) != 1 || d.Deps[0].ID != moving || d.Deps[0].State != "open" || !strings.Contains(d.Deps[0].Title, "moving") {
		t.Errorf("epic show must resolve the dep edge: %+v", d.Deps)
	}

	mustEpicDone(t, a, moving)
	items, err = a.EpicList(EpicQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if items[i].Epic.ID == furniture && len(items[i].OpenDeps) != 0 {
			t.Errorf("a closed dep is satisfied — OpenDeps must empty out: %+v", items[i].OpenDeps)
		}
	}
}
