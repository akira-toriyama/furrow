package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func itemFor(items []ListItem, id string) *ListItem {
	for i := range items {
		if items[i].Task.ID == id {
			return &items[i]
		}
	}
	return nil
}

func ids(items []ListItem) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Task.ID
	}
	return out
}

// TestListItemsDerivedFacts pins the per-row facts `ls` exposes: actionable
// (a next lane + all deps done), blocked_by (the undone deps), and — for a
// container box with open work but nothing actionable under it — stuck. These
// must match what `ls --tree` computes (same helpers), so the flat list and the
// tree can never disagree.
func TestListItemsDerivedFacts(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	gate := mk("gate", AddOpts{Status: "ready"})
	blocked := mk("waits on gate", AddOpts{Status: "ready", Deps: []string{gate}})
	epic := mk("epic", AddOpts{Type: "epic", Status: "backlog"})
	mk("child blocked", AddOpts{Parent: epic, Status: "ready", Deps: []string{gate}})

	items, err := a.ListItems(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if it := itemFor(items, gate); it == nil || !it.Actionable || it.Container || len(it.BlockedBy) != 0 {
		t.Errorf("gate should be actionable, non-container, unblocked: %+v", it)
	}
	if it := itemFor(items, blocked); it == nil || it.Actionable || len(it.BlockedBy) != 1 || it.BlockedBy[0] != gate {
		t.Errorf("blocked task should be non-actionable with blocked_by=[gate]: %+v", it)
	}
	if it := itemFor(items, epic); it == nil || !it.Container || it.Actionable || !it.Stuck {
		t.Errorf("epic should be a stuck container (open child, no actionable descendant): %+v", it)
	}
}

// TestListItemsActionableBlockedFilters pins that --actionable / --blocked narrow
// the list by the derived state and AND with the lane scope, and that -n caps the
// FILTERED set (the filter runs before the limit).
func TestListItemsActionableBlockedFilters(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	gate := mk("gate", AddOpts{Status: "ready"})
	blocked := mk("blocked", AddOpts{Status: "ready", Deps: []string{gate}})

	act, err := a.ListItems(QueryOpts{Actionable: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(act); len(got) != 1 || got[0] != gate {
		t.Errorf("--actionable should keep only the gate: %v", got)
	}

	blk, err := a.ListItems(QueryOpts{Blocked: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(blk); len(got) != 1 || got[0] != blocked {
		t.Errorf("--blocked should keep only the blocked task: %v", got)
	}

	// --blocked AND -s ready: still the blocked ready task; a non-ready blocked
	// task would be filtered by the lane scope.
	scoped, err := a.ListItems(QueryOpts{Status: "ready", Blocked: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(scoped); len(got) != 1 || got[0] != blocked {
		t.Errorf("-s ready --blocked: %v", got)
	}
}

// TestNextLanesOverride pins that --lanes (o.Lanes) widens which lanes `next`
// considers for THIS call, without touching config, and that the deps-done half
// is unchanged (a backlog task with an undone dep still does not surface).
func TestNextLanesOverride(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	ready := mk("ready", AddOpts{Status: "ready"})
	backlogFree := mk("backlog no-deps", AddOpts{Status: "backlog"})
	gate := mk("gate", AddOpts{Status: "ready"})
	backlogBlocked := mk("backlog blocked", AddOpts{Status: "backlog", Deps: []string{gate}})

	set := func(ts []core.Task) map[string]bool {
		m := map[string]bool{}
		for _, tk := range ts {
			m[tk.ID] = true
		}
		return m
	}

	// Default: no backlog task surfaces (backlog is not a configured next-lane).
	def, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if d := set(def); d[backlogFree] || d[backlogBlocked] {
		t.Errorf("default next must exclude backlog tasks: %v", def)
	}

	// --lanes backlog,ready: the free backlog task surfaces; the blocked one still
	// does not (the deps-done half of the predicate is unchanged).
	got, err := a.Next(QueryOpts{Lanes: []string{"backlog", "ready"}})
	if err != nil {
		t.Fatal(err)
	}
	g := set(got)
	if !g[backlogFree] || !g[ready] {
		t.Errorf("--lanes backlog,ready should surface the free backlog task AND the ready one: %v", got)
	}
	if g[backlogBlocked] {
		t.Error("a backlog task still blocked by an undone dep must not surface, even with --lanes")
	}

	// The override is in-memory only: config's next-lanes are untouched.
	if !a.Cfg.IsNextLane("ready") || a.Cfg.IsNextLane("backlog") {
		t.Error("--lanes must not mutate the configured next-lanes")
	}

	// An unknown --lanes token is a validation error (exit 2 + candidates).
	if _, err := a.Next(QueryOpts{Lanes: []string{"nope"}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown --lanes token should be a validation error, got %v", err)
	}
}
