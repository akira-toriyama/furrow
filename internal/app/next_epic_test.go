package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// sameIDs reports whether tasks carry exactly want's ids, in order.
func sameIDs(tasks []core.Task, want []string) bool {
	if len(tasks) != len(want) {
		return false
	}
	for i := range tasks {
		if tasks[i].ID != want[i] {
			return false
		}
	}
	return true
}

// The active-epic scope predicate of `furrow next`, exhaustively — active × flag ×
// the task's epic:
//
//	| active | flag        | task's epic | in next |
//	|--------|-------------|-------------|---------|
//	| e-A    | —           | e-A         | yes     |
//	| e-A    | —           | none        | yes (rescue, AFTER the focus) |
//	| e-A    | —           | e-B         | no      |
//	| none   | —           | anything    | no (deliberately empty, exit 0) |
//	| any    | --all-epics | anything    | yes     |
//	| e-A    | -e e-B      | e-B         | yes     |
//	| e-A    | -e e-B      | none        | no (an explicit -e is strict)   |
//
// This table is the reason the test exists as a table: the scope shipped once as
// a QueryOpts field + a --all-epics flag with NO consuming predicate — every
// unit test stayed green while `furrow next` read the whole board. (The same
// way v5's `next --containers` shipped with zero coverage.)
func TestNextEpicScopeTable(t *testing.T) {
	// One board, mutated per row via activate/deactivate: e-A active, e-B open.
	// Task add order puts the unfiled task FIRST so canonical order favors it —
	// proving the focus-first partition rather than riding on insertion order.
	setup := func(t *testing.T) (a *App, eA, eB, inA, inB, unfiled string) {
		t.Helper()
		a = newApp()
		eA = mustEpic(t, a, "box a", EpicAddOpts{Repos: []string{"o/r"}})
		eB = mustEpic(t, a, "box b", EpicAddOpts{Repos: []string{"o/r"}})
		mustActivate(t, a, eA)
		// NoEpic: this fixture MEANS "unfiled" — without it the new active-epic
		// inheritance would file the task under eA and dissolve the partition.
		tu, err := a.Add("unfiled ready", AddOpts{Status: "ready", NoEpic: true})
		if err != nil {
			t.Fatal(err)
		}
		ta, err := a.Add("in box a", AddOpts{Status: "ready", Epic: eA})
		if err != nil {
			t.Fatal(err)
		}
		tb, err := a.Add("in box b", AddOpts{Status: "ready", Epic: eB})
		if err != nil {
			t.Fatal(err)
		}
		return a, eA, eB, ta.ID, tb.ID, tu.ID
	}

	t.Run("active box: its tasks lead, the unfiled pile rides behind, the other box is out", func(t *testing.T) {
		a, _, _, inA, inB, unfiled := setup(t)
		got, err := a.Next(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{inA, unfiled}
		if !sameIDs(got, want) {
			t.Fatalf("next = %v, want %v (focus first, rescue second, %s excluded)", idsOf(got), want, inB)
		}
	})

	t.Run("limit hands you the focus, not whatever sorts first", func(t *testing.T) {
		a, _, _, inA, _, _ := setup(t)
		got, err := a.Next(QueryOpts{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !sameIDs(got, []string{inA}) {
			t.Fatalf("next -n1 = %v, want just the focus task %s", idsOf(got), inA)
		}
	})

	t.Run("no active box: deliberately empty, never the unfiled pile", func(t *testing.T) {
		a, eA, _, _, _, _ := setup(t)
		if _, _, err := a.EpicDeactivate(eA); err != nil {
			t.Fatal(err)
		}
		got, err := a.Next(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("next with no active epic = %v, want [] (\"no box open\" must not degrade into \"show me the unfiled pile\")", idsOf(got))
		}
	})

	t.Run("--all-epics: the whole board", func(t *testing.T) {
		a, _, _, inA, inB, unfiled := setup(t)
		got, err := a.Next(QueryOpts{AllEpics: true})
		if err != nil {
			t.Fatal(err)
		}
		if !sameIDs(got, []string{unfiled, inA, inB}) {
			t.Fatalf("next --all-epics = %v, want all three in canonical order", idsOf(got))
		}
	})

	t.Run("-e names a box strictly: its members only, no unfiled carve-out", func(t *testing.T) {
		a, _, eB, _, inB, _ := setup(t)
		got, err := a.Next(QueryOpts{Epic: eB})
		if err != nil {
			t.Fatal(err)
		}
		if !sameIDs(got, []string{inB}) {
			t.Fatalf("next -e %s = %v, want only %s (explicit -e overrides the active box AND excludes unfiled)", eB, idsOf(got), inB)
		}
	})

	t.Run("a closed active box frees the scope", func(t *testing.T) {
		// EpicDone clears Active in the same write; the scope must see that, not
		// the stale flag.
		a, eA, _, _, _, _ := setup(t)
		if _, _, err := a.EpicDone(eA); err != nil {
			t.Fatal(err)
		}
		got, err := a.Next(QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("next after closing the active box = %v, want [] until a new box opens", idsOf(got))
		}
	})
}

// A board that never declared a box is not participating: `furrow next` behaves
// classically (the same rule lint's epic-required/epic-no-active follow). The
// alternative — scoping an epic-less board — would make every fresh board's
// `next` permanently empty until an epic exists.
func TestNextEpicScopeDisengagedOnEpiclessBoard(t *testing.T) {
	a := newApp()
	task, err := a.Add("plain ready", AddOpts{Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(got, []string{task.ID}) {
		t.Fatalf("next on an epic-less board = %v, want the classic result", idsOf(got))
	}
	scope, err := a.NextScope(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if scope.Engaged {
		t.Error("NextScope must not engage on a board with no epics")
	}
}

// The scope is per REPO: a repo-scoped read sees only the active epic(s) naming
// that repo, and a board-wide read unions every repo's active box.
func TestNextEpicScopePerRepo(t *testing.T) {
	a := newApp()
	eR := mustEpic(t, a, "box for r", EpicAddOpts{Repos: []string{"o/r"}})
	eS := mustEpic(t, a, "box for s", EpicAddOpts{Repos: []string{"o/s"}})
	mustActivate(t, a, eR)
	mustActivate(t, a, eS)
	tr, err := a.Add("r task", AddOpts{Status: "ready", Epic: eR, Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	ts, err := a.Add("s task", AddOpts{Status: "ready", Epic: eS, Repos: []string{"o/s"}})
	if err != nil {
		t.Fatal(err)
	}

	// Repo-scoped: only that repo's active box (its other-repo sibling is out of
	// scope by the repo filter anyway; the point is the ACTIVE SET is narrowed,
	// so the read is not emptied by "two actives exist board-wide").
	got, err := a.Next(QueryOpts{Repo: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(got, []string{tr.ID}) {
		t.Fatalf("next -r o/r = %v, want [%s]", idsOf(got), tr.ID)
	}

	// Board-wide: both repos' active boxes, unioned — a cross-repo listing has
	// no single focus to pick, and erroring would make `-r ''` useless.
	got, err = a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(got, []string{tr.ID, ts.ID}) {
		t.Fatalf("board-wide next = %v, want both active boxes' tasks", idsOf(got))
	}

	scope, err := a.NextScope(QueryOpts{ScopeRepo: "o/s"})
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Engaged || len(scope.Active) != 1 || !scope.Active[eS] {
		t.Errorf("NextScope for o/s = %+v, want engaged with exactly %s", scope, eS)
	}
}

// The explicit -e filter is match()'s, not just Next's: every filtering read
// (ls and friends) narrows to the named box, strictly. This is also the
// predicate `epic show` uses to list members — before it existed, EpicShow's
// ListItems(QueryOpts{Epic: id}) matched the WHOLE BOARD.
func TestListEpicFilterIsStrict(t *testing.T) {
	a := newApp()
	eA := mustEpic(t, a, "box a", EpicAddOpts{Repos: []string{"o/r"}})
	inA, err := a.Add("member", AddOpts{Epic: eA})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("unfiled", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := a.List(QueryOpts{Epic: eA})
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(got, []string{inA.ID}) {
		t.Fatalf("ls -e %s = %v, want only the member %s", eA, idsOf(got), inA.ID)
	}
	detail, err := a.EpicShow(eA)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tasks) != 1 || detail.Tasks[0].Task.ID != inA.ID {
		t.Fatalf("epic show members = %d rows, want exactly the one member", len(detail.Tasks))
	}
}
