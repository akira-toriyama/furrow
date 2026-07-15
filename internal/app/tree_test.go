package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// treeIDs flattens a forest depth-first — the order the human renderer prints.
func treeIDs(nodes []TreeNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Task.ID)
		out = append(out, treeIDs(n.Children)...)
	}
	return out
}

func findNode(nodes []TreeNode, id string) *TreeNode {
	for i := range nodes {
		if nodes[i].Task.ID == id {
			return &nodes[i]
		}
		if n := findNode(nodes[i].Children, id); n != nil {
			return n
		}
	}
	return nil
}

// treeFind builds the whole forest and returns the node for id (t.Fatal if absent).
func treeFind(t *testing.T, a *App, id string, recursive bool) *TreeNode {
	t.Helper()
	nodes, err := a.Tree(QueryOpts{}, "", recursive)
	if err != nil {
		t.Fatal(err)
	}
	n := findNode(nodes, id)
	if n == nil {
		t.Fatalf("node %s not found in tree", id)
	}
	return n
}

// TestTreeContainerProgressAndStuck pins t-3jd1 §3(c/d): a container shows rolled-up
// child progress and is "stuck" when it has open work but no actionable descendant.
func TestTreeContainerProgressAndStuck(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	epic := mk("epic", AddOpts{Type: "epic", Status: "backlog"})
	mk("done child", AddOpts{Parent: epic, Status: "done"})
	back := mk("backlog child", AddOpts{Parent: epic, Status: "backlog"})

	e := treeFind(t, a, epic, false)
	if !e.Container {
		t.Error("a type=epic task must be a container")
	}
	if e.Progress == nil || e.Progress.Done != 1 || e.Progress.Total != 2 {
		t.Errorf("progress = %+v, want 1/2", e.Progress)
	}
	if !e.Stuck {
		t.Error("an epic with an open child and no actionable descendant must be stuck")
	}

	// Moving the open child into a next lane gives the box an actionable descendant.
	if _, err := a.Move(back, "ready"); err != nil {
		t.Fatal(err)
	}
	if treeFind(t, a, epic, false).Stuck {
		t.Error("an epic with a ready (actionable) descendant must NOT be stuck")
	}
}

// TestTreeStuckRecursesThroughSubEpics pins the design's §3(c) clarification: stuck
// walks the whole subtree, so a top epic whose only actionable work sits under a
// SUB-epic is not stuck. And a freshly-declared empty epic is never stuck.
func TestTreeStuckRecursesThroughSubEpics(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	top := mk("top epic", AddOpts{Type: "epic", Status: "backlog"})
	sub := mk("sub epic", AddOpts{Type: "epic", Status: "backlog", Parent: top})
	mk("ready leaf", AddOpts{Status: "ready", Parent: sub})

	if treeFind(t, a, top, false).Stuck {
		t.Error("a top epic with a ready leaf under a sub-epic must NOT be stuck (stuck recurses through containers)")
	}

	empty := mk("empty epic", AddOpts{Type: "epic", Status: "backlog"})
	ee := treeFind(t, a, empty, false)
	if ee.Progress == nil || ee.Progress.Total != 0 {
		t.Errorf("empty epic progress = %+v, want 0/0", ee.Progress)
	}
	if ee.Stuck {
		t.Error("an empty epic (zero children) must NOT be stuck — it is a fresh declaration, not a stalled box")
	}
}

// TestTreeProgressRecursive pins the direct-vs-subtree scope of the roll-up.
func TestTreeProgressRecursive(t *testing.T) {
	a := newApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	top := mk("top", AddOpts{Type: "epic", Status: "backlog"})
	sub := mk("sub", AddOpts{Type: "epic", Status: "done", Parent: top})
	mk("leaf done", AddOpts{Status: "done", Parent: sub})
	mk("leaf open", AddOpts{Status: "backlog", Parent: sub})

	if e := treeFind(t, a, top, false); e.Progress.Done != 1 || e.Progress.Total != 1 {
		t.Errorf("direct progress = %+v, want 1/1 (the one direct child, done)", e.Progress)
	}
	if e := treeFind(t, a, top, true); e.Progress.Done != 2 || e.Progress.Total != 3 {
		t.Errorf("recursive progress = %+v, want 2/3 (sub + 2 leaves, two done)", e.Progress)
	}
}

func TestTreeNestsTheHierarchy(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "slice", "sub-slice", "unrelated")
	epic, slice, sub, loose := ids[0], ids[1], ids[2], ids[3]
	if _, err := a.Reparent(slice, epic); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reparent(sub, slice); err != nil {
		t.Fatal(err)
	}

	nodes, err := a.Tree(QueryOpts{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want two roots (the epic and the loose task), got %v", treeIDs(nodes))
	}
	e := findNode(nodes, epic)
	if e == nil || len(e.Children) != 1 || e.Children[0].Task.ID != slice {
		t.Fatalf("the slice must nest under the epic: %v", treeIDs(nodes))
	}
	if len(e.Children[0].Children) != 1 || e.Children[0].Children[0].Task.ID != sub {
		t.Errorf("the sub-slice must nest two deep: %v", treeIDs(nodes))
	}
	if findNode(nodes, loose) == nil {
		t.Errorf("a parentless task is its own root: %v", treeIDs(nodes))
	}
}

// The star is the point of drawing the tree: it says WHERE the work is available.
// It must be `next`'s own predicate, not a lookalike.
func TestTreeStarsExactlyWhatNextWouldHand(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "gate", "waits on the gate", "still in inbox")
	epic, gate, waiter, inbox := ids[0], ids[1], ids[2], ids[3]
	for _, c := range []string{gate, waiter, inbox} {
		if _, err := a.Reparent(c, epic); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []string{gate, waiter} {
		if _, err := a.Move(c, "ready"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.AddDeps(waiter, []string{gate}); err != nil {
		t.Fatal(err)
	}

	nodes, err := a.Tree(QueryOpts{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	starred := map[string]bool{}
	var walk func([]TreeNode)
	walk = func(ns []TreeNode) {
		for _, n := range ns {
			if n.Actionable {
				starred[n.Task.ID] = true
			}
			walk(n.Children)
		}
	}
	walk(nodes)

	if len(next) != 1 || next[0].ID != gate {
		t.Fatalf("setup: next should hand exactly the gate, got %v", listIDs(next))
	}
	if len(starred) != 1 || !starred[gate] {
		t.Errorf("the tree must star exactly what `next` hands (%s), got %v", gate, starred)
	}

	// And the blocked one says what is in its way — only the deps that are NOT done.
	w := findNode(nodes, waiter)
	if w == nil || len(w.BlockedBy) != 1 || w.BlockedBy[0] != gate {
		t.Fatalf("the waiter must name its blocker, got %+v", w)
	}
	if _, err := a.Done(gate); err != nil {
		t.Fatal(err)
	}
	nodes, err = a.Tree(QueryOpts{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	w = findNode(nodes, waiter)
	if len(w.BlockedBy) != 0 || !w.Actionable {
		t.Errorf("once the gate is done the waiter is free and blocked_by empties: %+v", w)
	}
	if inboxNode := findNode(nodes, inbox); inboxNode.Actionable {
		t.Error("an inbox task is not in a next lane, so it is not actionable however free it is")
	}
}

// A tree that showed FEWER tasks than the same filters without it would be lying.
// A task whose parent was filtered out becomes a root — it is never dropped.
func TestTreeNeverHidesAMatchWhoseParentWasFiltered(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic stays in inbox", "child is ready")
	epic, child := ids[0], ids[1]
	if _, err := a.Reparent(child, epic); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Move(child, "ready"); err != nil {
		t.Fatal(err)
	}

	nodes, err := a.Tree(QueryOpts{Status: "ready"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := treeIDs(nodes)
	if len(got) != 1 || got[0] != child {
		t.Errorf("the child matched the filter and must appear as a root, got %v", got)
	}
}

// -n caps TREES, not tasks: truncating mid-hierarchy would amputate children from
// the very trees it did show.
func TestTreeLimitCapsRootsNotTasks(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "a", "b", "c", "second epic")
	epic := ids[0]
	for _, c := range ids[1:4] {
		if _, err := a.Reparent(c, epic); err != nil {
			t.Fatal(err)
		}
	}

	nodes, err := a.Tree(QueryOpts{Limit: 1}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("-n 1 must yield one TREE, got %d", len(nodes))
	}
	if len(nodes[0].Children) != 3 {
		t.Errorf("the tree it did show must be whole (3 children), got %d", len(nodes[0].Children))
	}
}

func TestTreeRootArgumentAndItsErrors(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "slice", "elsewhere")
	epic, slice := ids[0], ids[1]
	if _, err := a.Reparent(slice, epic); err != nil {
		t.Fatal(err)
	}

	nodes, err := a.Tree(QueryOpts{}, epic, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := treeIDs(nodes); len(got) != 2 || got[0] != epic || got[1] != slice {
		t.Errorf("a root id draws just that subtree, got %v", got)
	}

	// An unknown id is a MISS (exit 1).
	if _, err := a.Tree(QueryOpts{}, "t-404", false); core.AsError(err) == nil || core.AsError(err).Code != core.CodeNotFound {
		t.Errorf("an unknown root is exit 1, got %v", err)
	}
	// An id that EXISTS but the filters exclude is a validation error — an empty
	// tree would read as "this task has nothing under it", which is a different fact.
	if _, err := a.Tree(QueryOpts{Status: "done"}, epic, false); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Errorf("a filtered-out root must say so (exit 2), got %v", err)
	}
}

// A parent cycle can only arrive by a git merge of two half-edges (Reparent refuses
// one, lint reports it). Every task in it has a parent, so none is a root — a naive
// forest would silently DROP them all. Render them, truncated at the loop.
func TestTreeSurvivesAParentCycleMergedInBehindUs(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "a", "b", "innocent bystander")
	x, y, z := ids[0], ids[1], ids[2]

	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := idx.Find(x)
	ty, _ := idx.Find(y)
	tx.Parent, ty.Parent = y, x
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}

	done := make(chan []string, 1)
	go func() {
		nodes, err := a.Tree(QueryOpts{}, "", false)
		if err != nil {
			done <- nil
			return
		}
		done <- treeIDs(nodes)
	}()
	got := <-done // if the walker looped, the test times out here rather than hanging a user

	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range []string{x, y, z} {
		if !seen[id] {
			t.Errorf("a corrupt hierarchy must still render every task (%s missing): %v", id, got)
		}
	}
}
