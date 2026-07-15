package app

import (
	"github.com/akira-toriyama/furrow/internal/config"
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

// Progress is a container's rolled-up child completion — a DERIVED value, never
// stored (all prior art computes it), so editing a child always yields a current
// count with no stale number to reconcile. Done/Total count children in the done
// lane vs all children; the scope is direct children by default, the whole subtree
// with ls --tree --progress-recursive.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// TreeNode is one task in the hierarchy, with its children and the derived facts
// about whether it can be worked on right now and, for a container, how its work
// is progressing.
type TreeNode struct {
	Task core.Task
	// Actionable is `furrow next`'s own predicate: workable AND not a container. In
	// a tree it is what turns "here is the shape of the work" into "here is where
	// you can pick it up". A container is never actionable (a box is not work).
	Actionable bool
	// BlockedBy names the deps that are NOT yet done — what is actually stopping
	// this task. A done dep is history and is left out: the question a reader has in
	// front of a tree is "what is in the way", not "what was".
	BlockedBy []string
	// Container reports whether this node's type is a container (an epic): a box
	// that groups child work and is itself skipped by `furrow next`.
	Container bool
	// Progress is the child-completion roll-up, non-nil only for a container (a box
	// is the thing a count is ABOUT). Derived, never stored.
	Progress *Progress
	// Stuck marks a container that has open (non-terminal) work under it but NO
	// actionable descendant anywhere in its subtree — org-mode's "stuck project".
	// The one state a box can be in that `furrow next` cannot show (a box is never
	// in next), so the tree and `revisit` surface it instead.
	Stuck    bool
	Children []TreeNode
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
func (a *App) Tree(o QueryOpts, rootID string, progressRecursive bool) ([]TreeNode, error) {
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
		out = append(out, a.treeNode(idx, r, children, doneIDs, seen, progressRecursive))
	}
	return out, nil
}

// treeNode renders one node and descends. seen is the cycle guard: a task already
// placed in this forest is never expanded twice, so a merged-in parent cycle
// truncates instead of recursing forever.
func (a *App) treeNode(idx *core.Index, t core.Task, children map[string][]core.Task, doneIDs, seen map[string]bool, progressRecursive bool) TreeNode {
	n := TreeNode{
		Task:       t,
		Actionable: a.actionable(idx, &t, doneIDs),
		Container:  a.Cfg.IsContainerType(t.Type),
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
		n.Children = append(n.Children, a.treeNode(idx, c, children, doneIDs, seen, progressRecursive))
	}
	// Derived roll-ups for a container, computed bottom-up from the built subtree.
	// Progress honours the direct/recursive scope; Stuck is ALWAYS the whole subtree
	// (recursing through container children to any actionable leaf), so a box whose
	// only child is a sub-epic with a ready task under it is NOT stuck.
	if n.Container {
		done, total := progressCount(n.Children, progressRecursive, a.Cfg.DoneLane)
		n.Progress = &Progress{Done: done, Total: total}
		n.Stuck = anyOpen(n.Children, a.Cfg) && !anyActionable(n.Children)
	}
	return n
}

// progressCount tallies done/total children. recursive walks the whole subtree
// (counting every descendant, container or not); otherwise only direct children.
func progressCount(nodes []TreeNode, recursive bool, doneLane string) (done, total int) {
	for _, n := range nodes {
		total++
		if n.Task.Status == doneLane {
			done++
		}
		if recursive {
			d, t := progressCount(n.Children, true, doneLane)
			done += d
			total += t
		}
	}
	return done, total
}

// anyActionable reports whether any descendant is actionable — the "has a next
// action" test that keeps a container off the stuck list.
func anyActionable(nodes []TreeNode) bool {
	for _, n := range nodes {
		if n.Actionable || anyActionable(n.Children) {
			return true
		}
	}
	return false
}

// anyOpen reports whether any descendant sits in a non-terminal lane — i.e. there
// is genuinely open work under the box. Parked descendants (done, icebox, waiting)
// do not count, so a box whose remaining children are all iceboxed is finished-ish,
// not stuck.
func anyOpen(nodes []TreeNode, cfg *config.Config) bool {
	for _, n := range nodes {
		if !cfg.IsTerminal(n.Task.Status) {
			return true
		}
		if anyOpen(n.Children, cfg) {
			return true
		}
	}
	return false
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

// workable is the type-BLIND readiness test: the task sits in a next lane and
// every dep it names is done. It says nothing about whether the task is a box.
// `furrow next --containers` surfaces on this directly — a ready epic IS "next"
// when you explicitly ask to see boxes.
func (a *App) workable(idx *core.Index, t *core.Task, doneIDs map[string]bool) bool {
	return a.Cfg.IsNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs)
}

// actionable is `furrow next`'s default membership test AND `ls --tree`'s ★, kept
// as one definition so the two views cannot drift on what "you could pick this up
// now" means: workable AND not a container. A container (an epic) is a box that
// groups child work — never a thing you pick up — so it is never starred in a tree
// and never handed out by a plain `next`. `next --containers` relaxes this to
// workable (see App.Next); the tree ★ never does (a box is never actionable).
func (a *App) actionable(idx *core.Index, t *core.Task, doneIDs map[string]bool) bool {
	return a.workable(idx, t, doneIDs) && !a.Cfg.IsContainerType(t.Type)
}
