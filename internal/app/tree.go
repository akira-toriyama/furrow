package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
)

// The hierarchy, drawn. furrow stores TWO relations between tasks and, until now,
// could only ever show you one level of either: `parent --list` (its parent, its
// children) and `dep --list` (what it waits on, what waits on it). "Show me
// everything that leads to this goal" was a question you had to answer by hand —
// and the last time someone needed it, they read the shards with a Python script.
//
// The two relations do different jobs and the tree keeps them apart:
//
//   - `parent` is the SKELETON. It is a real tree (one parent, many children), so
//     it nests.
//   - `deps` are the GATE. They form a DAG across the tree — a task in one branch
//     can wait on a task in another — so they cannot nest, and appear as an
//     annotation on the node they block.
//
// What an agent actually wants from this is not the drawing but the two derived
// facts, so the node carries them: Actionable (nothing is stopping this — the exact
// predicate `furrow next` uses) and BlockedBy (what is).

// TreeNode is one task in the hierarchy, with its children and the two derived
// facts about whether it can be worked on right now.
type TreeNode struct {
	Task core.Task
	// Actionable is `furrow next`'s own predicate: the task sits in a next lane and
	// every dep it names is done. In a tree it is what turns "here is the shape of
	// the work" into "here is where you can pick it up".
	Actionable bool
	// BlockedBy names the deps that are NOT yet done — what is actually stopping
	// this task. A done dep is history and is left out: the question a reader has in
	// front of a tree is "what is in the way", not "what was".
	BlockedBy []string
	Children  []TreeNode
}

// Tree builds the parent forest over the tasks matching o. rootID (optional) picks
// a single subtree instead of the whole forest.
//
// The forest is built over the FILTERED set, which has one consequence worth
// stating plainly: a task whose parent was filtered out (a different repo, another
// lane) is rendered as a ROOT, not hidden. Dropping it would make `--tree` quietly
// show fewer tasks than the same flags without it — a tree that lies about what
// matched is worse than one with a few extra roots.
//
// o.Limit caps the number of ROOTS (whole trees), never the tasks — a limit that
// truncated mid-hierarchy would silently amputate children from the trees it did
// show.
//
// A parent CYCLE (which only a git merge of two half-edges can create — Reparent
// refuses one, and lint reports it as parent-cycle) would leave every task in it
// parentless-but-unreachable, i.e. invisible. Those tasks are surfaced as roots and
// the descent carries a visited set, so a corrupt hierarchy renders — truncated at
// the loop — rather than vanishing or hanging.
func (a *App) Tree(o QueryOpts, rootID string) ([]TreeNode, error) {
	limit := o.Limit
	o.Limit = 0 // the limit is on roots, applied after the forest is built
	tasks, err := a.List(o)
	if err != nil {
		return nil, err
	}
	idx, err := a.listIndex(o)
	if err != nil {
		return nil, err
	}

	inSet := make(map[string]bool, len(tasks))
	for i := range tasks {
		inSet[tasks[i].ID] = true
	}
	if rootID != "" && !inSet[rootID] {
		if !idx.Has(rootID) {
			return nil, core.NotFound(rootID)
		}
		// The id exists but the active filters exclude it — say so rather than
		// returning an empty tree that reads like "this task has nothing under it".
		return nil, core.Validationf(rootID, "task %s exists but is outside the current filters — widen them (e.g. -s '' -r '') to draw its tree", rootID)
	}

	doneIDs := make(map[string]bool, len(idx.Tasks))
	for i := range idx.Tasks {
		if idx.Tasks[i].Status == a.Cfg.DoneLane {
			doneIDs[idx.Tasks[i].ID] = true
		}
	}

	// children[parent] preserves the incoming order, so siblings inherit whatever
	// order the query produced (canonical lane->priority->id, or --sort's).
	children := map[string][]core.Task{}
	var roots []core.Task
	for _, t := range tasks {
		if t.Parent != "" && inSet[t.Parent] {
			children[t.Parent] = append(children[t.Parent], t)
			continue
		}
		roots = append(roots, t)
	}

	if rootID != "" {
		t, _ := idx.Find(rootID)
		roots = []core.Task{*t}
	} else {
		roots = append(roots, orphanedByCycle(tasks, children, roots)...)
		if limit > 0 && len(roots) > limit {
			roots = roots[:limit]
		}
	}

	out := make([]TreeNode, 0, len(roots))
	seen := map[string]bool{}
	for _, r := range roots {
		out = append(out, a.treeNode(idx, r, children, doneIDs, seen))
	}
	return out, nil
}

// treeNode renders one node and descends. seen is the cycle guard: a task already
// placed in this forest is never expanded twice, so a merged-in parent cycle
// truncates instead of recursing forever.
func (a *App) treeNode(idx *core.Index, t core.Task, children map[string][]core.Task, doneIDs, seen map[string]bool) TreeNode {
	n := TreeNode{
		Task:       t,
		Actionable: a.actionable(idx, &t, doneIDs),
		BlockedBy:  []string{},
		Children:   []TreeNode{},
	}
	for _, d := range t.Deps {
		if !doneIDs[d] {
			n.BlockedBy = append(n.BlockedBy, d)
		}
	}
	if seen[t.ID] {
		return n // already drawn elsewhere: a cycle. Draw the node, stop the descent.
	}
	seen[t.ID] = true
	for _, c := range children[t.ID] {
		n.Children = append(n.Children, a.treeNode(idx, c, children, doneIDs, seen))
	}
	return n
}

// orphanedByCycle returns the tasks that no root can reach — every task in a parent
// cycle, since each has a parent inside the set and so is nobody's root. Without
// this they would simply not be drawn: the one failure mode a tree must not have is
// showing fewer tasks than it was given, silently.
func orphanedByCycle(tasks []core.Task, children map[string][]core.Task, roots []core.Task) []core.Task {
	reachable := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, c := range children[id] {
			walk(c.ID)
		}
	}
	for _, r := range roots {
		walk(r.ID)
	}
	var out []core.Task
	for _, t := range tasks {
		if !reachable[t.ID] {
			out = append(out, t)
			walk(t.ID) // one task per cycle becomes its root; the rest hang under it
		}
	}
	return out
}

// actionable is `furrow next`'s membership test, extracted so `next` and `--tree`
// cannot drift into disagreeing about what "you could pick this up now" means: in a
// next lane, and every dep done.
func (a *App) actionable(idx *core.Index, t *core.Task, doneIDs map[string]bool) bool {
	return a.Cfg.IsNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs)
}
