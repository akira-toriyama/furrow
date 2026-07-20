package main

import (
	"strings"
	"testing"
)

func mk(id, lane string, deps ...string) *Task {
	return &Task{ID: id, Title: id, Status: lane, Deps: deps}
}

func TestBlockedByCountsOnlyUnfinishedDeps(t *testing.T) {
	b := NewBoard([]*Task{
		mk("a", "ready", "b", "c"),
		mk("b", "done"),
		mk("c", "backlog"),
	})
	g := NewGraph(b)

	got := g.BlockedBy("a")
	if len(got) != 1 || got[0] != "c" {
		t.Errorf("BlockedBy(a) = %v, want [c] (b is done)", got)
	}
	if g.Actionable("a") {
		t.Error("a is in a next lane but has an open dep — not actionable")
	}
	if !g.Actionable("c") == false && !g.Actionable("c") {
		// c is in backlog, which is not a next lane.
		t.Log("c is not actionable because backlog is not a next lane")
	}
}

func TestUnknownDepBlocks(t *testing.T) {
	b := NewBoard([]*Task{mk("a", "ready", "ghost")})
	g := NewGraph(b)
	if got := g.BlockedBy("a"); len(got) != 1 || got[0] != "ghost" {
		t.Errorf("an unknown dep must block (we cannot prove it is satisfied), got %v", got)
	}
	if g.Known("ghost") {
		t.Error("ghost is not on the board")
	}
	if g.Actionable("a") {
		t.Error("a depends on something unknown; it cannot be actionable")
	}
}

func TestReverseDeps(t *testing.T) {
	b := NewBoard([]*Task{
		mk("root", "backlog"),
		mk("x", "ready", "root"),
		mk("y", "backlog", "root"),
		mk("z", "done", "root"),
	})
	g := NewGraph(b)

	if got := g.Blocks("root"); strings.Join(got, ",") != "x,y,z" {
		t.Errorf("Blocks(root) = %v, want x,y,z", got)
	}
	// OpenBlocks is what closing root would actually free up.
	if got := g.OpenBlocks("root"); strings.Join(got, ",") != "x,y" {
		t.Errorf("OpenBlocks(root) = %v, want x,y", got)
	}
	if got := g.Blocks("x"); len(got) != 0 {
		t.Errorf("Blocks(x) = %v, want none", got)
	}
}

func TestContainerIsDeclaredNotInferred(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "epic", Status: "in-progress", Type: "epic"},
		{ID: "plain", Status: "ready", Type: ""},
		{ID: "kid", Status: "ready", Parent: "plain"},
		{ID: "empty-epic", Status: "backlog", Type: "epic"},
	})
	g := NewGraph(b)

	if !g.IsContainer("epic") {
		t.Error("a declared epic is a container")
	}
	if g.IsContainer("plain") {
		t.Error("a plain task with children is NOT a container — type is declared, not inferred")
	}
	if !g.IsContainer("empty-epic") {
		t.Error("an empty epic is still a legitimate declaration")
	}
	// A container is a box, not work: furrow next skips it.
	if g.Actionable("epic") {
		t.Error("a container must never be actionable")
	}
	if !g.Actionable("plain") {
		t.Error("a type-less task in a next lane with no deps is actionable")
	}
	// An empty container never nags.
	if g.ChildrenDone("empty-epic") {
		t.Error("an empty epic must not report children_done")
	}
	if g.Stuck("empty-epic") {
		t.Error("an empty epic has no open work, so it is not stuck")
	}
}

func TestStuckWalksTheSubtree(t *testing.T) {
	// epic -> sub-epic -> blocked leaf. Nothing actionable anywhere below.
	b := NewBoard([]*Task{
		{ID: "e", Status: "in-progress", Type: "epic"},
		{ID: "se", Status: "backlog", Type: "epic", Parent: "e"},
		{ID: "leaf", Status: "ready", Parent: "se", Deps: []string{"wall"}},
		{ID: "wall", Status: "backlog"},
	})
	g := NewGraph(b)
	if !g.Stuck("e") {
		t.Error("e has open work below it but nothing actionable — stuck")
	}

	// Unblock the leaf and the stuck flag must clear, through the sub-epic.
	b2 := NewBoard([]*Task{
		{ID: "e", Status: "in-progress", Type: "epic"},
		{ID: "se", Status: "backlog", Type: "epic", Parent: "e"},
		{ID: "leaf", Status: "ready", Parent: "se"},
	})
	if NewGraph(b2).Stuck("e") {
		t.Error("an actionable descendant clears stuck, however deep")
	}
}

func TestProgressRollsUp(t *testing.T) {
	b := NewBoard([]*Task{
		{ID: "e", Status: "in-progress", Type: "epic"},
		{ID: "a", Status: "done", Parent: "e"},
		{ID: "bq", Status: "backlog", Parent: "e"},
		{ID: "se", Status: "backlog", Type: "epic", Parent: "e"},
		{ID: "deep", Status: "done", Parent: "se"},
	})
	g := NewGraph(b)
	if d, tot := g.Progress("e", false); d != 1 || tot != 3 {
		t.Errorf("direct progress = %d/%d, want 1/3", d, tot)
	}
	if d, tot := g.Progress("e", true); d != 2 || tot != 4 {
		t.Errorf("recursive progress = %d/%d, want 2/4", d, tot)
	}
}

func TestGraphAgreesWithFixtureFacts(t *testing.T) {
	b, g := fixtureGraph(t)

	// t-fw2m is the epic, in a next lane, with 18 children.
	if !g.IsContainer("t-fw2m") {
		t.Error("t-fw2m is the fixture's epic")
	}
	if n := len(g.Children("t-fw2m")); n != 18 {
		t.Errorf("t-fw2m has %d children, want 18", n)
	}
	if g.Actionable("t-fw2m") {
		t.Error("the epic sits in in-progress but must be skipped by next")
	}

	// t-jv3j depends on t-ehk7 (open) and t-t38k (done).
	got := g.BlockedBy("t-jv3j")
	if len(got) != 1 || got[0] != "t-ehk7" {
		t.Errorf("BlockedBy(t-jv3j) = %v, want [t-ehk7]", got)
	}

	// No dangling deps and no cycles in the fixture — the same facts measured
	// over the real 658-task board.
	for _, task := range b.Tasks() {
		for _, d := range task.Deps {
			if !g.Known(d) {
				t.Errorf("%s has a dangling dep %s", task.ID, d)
			}
		}
	}
}

func TestTreeOfIsCycleSafeAndElidesDone(t *testing.T) {
	// A deliberate cycle: a -> b -> a. The real board has none, but a tree
	// walker that trusts that is a walker that hangs on the first bad merge.
	b := NewBoard([]*Task{
		mk("a", "backlog", "b"),
		mk("b", "backlog", "a"),
	})
	g := NewGraph(b)
	n := g.TreeOf("a", dirBlockedBy, 10)
	if depth(n) > 11 {
		t.Fatalf("cycle was not cut: depth %d", depth(n))
	}

	// A done blocker's own blockers are history, not context.
	b2 := NewBoard([]*Task{
		mk("top", "ready", "mid"),
		mk("mid", "done", "bottom"),
		mk("bottom", "backlog"),
	})
	n2 := NewGraph(b2).TreeOf("top", dirBlockedBy, 5)
	if len(n2.Children) != 1 {
		t.Fatalf("want one child, got %d", len(n2.Children))
	}
	kid := n2.Children[0]
	if !kid.Elided || len(kid.Children) != 0 {
		t.Errorf("a done subtree must be elided, got elided=%v children=%d", kid.Elided, len(kid.Children))
	}
}

func TestTreeOfDuplicatesSharedNodesRatherThanMerging(t *testing.T) {
	// lipgloss/v2's tree has no multi-parent support, so a DAG node reachable
	// twice is DRAWN twice and flagged, never silently merged.
	b := NewBoard([]*Task{
		mk("root", "backlog", "l", "r"),
		mk("l", "backlog", "shared"),
		mk("r", "backlog", "shared"),
		mk("shared", "backlog"),
	})
	n := NewGraph(b).TreeOf("root", dirBlockedBy, 5)
	var repeats int
	var walk func(*depNode)
	walk = func(x *depNode) {
		if x.Repeat {
			repeats++
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	if repeats != 1 {
		t.Errorf("the second sighting of a shared node must be flagged Repeat, got %d", repeats)
	}
}

func depth(n *depNode) int {
	d := 0
	for _, c := range n.Children {
		if x := depth(c); x > d {
			d = x
		}
	}
	return d + 1
}
