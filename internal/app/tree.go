package app

import (
	"sort"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The board, grouped by box. Before v6 this drew a task->task `parent` forest of
// arbitrary depth; measured on the real board, 107 of 108 parent edges pointed at
// a task typed "epic", i.e. the hierarchy WAS epic membership wearing a general
// mechanism. v6 makes that explicit: one level of grouping (epic -> its tasks),
// and a task's internal breakdown lives in its checklist, where the one genuine
// task-under-task edge on that board had no business being either.
//
// Deps are a different relation and stay separate: they form a DAG ACROSS boxes —
// a task in one epic can wait on a task in another — so they can never nest, and
// appear as an annotation (BlockedBy) on the node they block.
//
// What an agent actually wants from this is not the drawing but the derived facts,
// so the node carries them: Actionable (nothing is stopping this — the exact
// predicate `furrow next` uses, before its epic scoping) and BlockedBy (what is).

// Progress is an epic's rolled-up member completion — a DERIVED value, never
// stored, so closing a task always yields a current count with no second number to
// reconcile. Done/Total count members in the done lane vs all members.
//
// There is no recursive variant any more, and none is missing: epics do not nest,
// so "direct members" and "the whole subtree" are the same set. (v5's
// --progress-recursive existed only because containers could contain containers.)
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// TreeGroup is one epic with its member tasks — the unit `ls --tree` prints.
type TreeGroup struct {
	// Epic is the box. Nil for the synthetic trailing group that collects tasks
	// belonging to no epic: they are a lint error, not a fiction to hide, and a
	// tree that dropped them would show fewer tasks than the same flags without
	// --tree.
	Epic *core.Epic
	// Active mirrors Epic.Active, hoisted so a renderer does not have to nil-check
	// the pointer to answer the question every reader asks first.
	Active bool
	// Progress is the member-completion roll-up over the FULL index, so a read
	// filter that hides some members cannot under-count the box. Nil for the
	// unfiled group (a count of "not in a box" is not progress toward anything).
	Progress *Progress
	// Stuck marks an epic with open members but NO actionable one — org-mode's
	// stuck project. It is the state `next` cannot show you (it would just return
	// empty), which is exactly why the group carries it.
	Stuck bool
	Tasks []TreeNode
}

// TreeNode is one task inside a group, with the derived facts about whether it
// can be picked up right now and, if not, what is in the way.
type TreeNode struct {
	Task core.Task
	// Actionable is `furrow next`'s task-level predicate: in a next lane, every
	// dep done. It is deliberately NOT epic-scoped — the ★ means "ready to pick
	// up", and a glyph whose meaning changed with which box happens to be active
	// would be unreadable. `next` narrows ★ further with its own scope filters, so
	// ★ is a strict superset of what it hands you.
	Actionable bool
	// BlockedBy names the deps that are NOT yet done — what is actually stopping
	// this task. A done dep is history and is left out: the question a reader has
	// in front of a tree is "what is in the way", not "what was".
	BlockedBy []string
}

// Tree groups the tasks matching o by epic. rootID (optional) picks a single
// epic's group instead of every group.
//
// Groups are ordered: the active epic first, then the remaining open epics by id,
// then closed epics, then the unfiled group. That is the order the reader's
// attention should go in, and it is total (no ties), so two runs print alike.
//
// o.Limit caps the number of GROUPS, never the tasks — a limit that truncated
// mid-group would silently amputate members from a box it did show.
func (a *App) Tree(o QueryOpts, rootID string) ([]TreeGroup, error) {
	limit := o.Limit
	o.Limit = 0 // the limit is on groups, applied after they are built
	tasks, err := a.List(o)
	if err != nil {
		return nil, err
	}
	idx, err := a.listIndex(o)
	if err != nil {
		return nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}

	if rootID != "" {
		resolved, err := a.resolveEpicIn(rootID, epics)
		if err != nil {
			return nil, err
		}
		rootID = resolved
	}

	doneIDs := a.doneSet(idx)

	// members[epicID] preserves the incoming order, so tasks inside a group keep
	// whatever order the query produced (canonical lane->priority->id, or --sort's).
	members := map[string][]core.Task{}
	var unfiled []core.Task
	for _, t := range tasks {
		if t.Epic == "" {
			unfiled = append(unfiled, t)
			continue
		}
		members[t.Epic] = append(members[t.Epic], t)
	}

	// Roll-ups run over the FULL index, never the filtered set, so `-s ready` can
	// not make a box look 1/1 when it is 1/7. The filtered `members` map drives
	// only what the tree DRAWS.
	counts := epicProgress(idx, a.Cfg.DoneLane)

	groups := make([]TreeGroup, 0, len(epics)+1)
	for i := range epics {
		e := &epics[i]
		if rootID != "" && e.ID != rootID {
			continue
		}
		p := counts[e.ID]
		groups = append(groups, TreeGroup{
			Epic:     e,
			Active:   e.Active,
			Progress: &p,
			Stuck:    a.epicStuck(idx, e.ID, doneIDs),
			Tasks:    a.treeNodes(idx, members[e.ID], doneIDs),
		})
	}
	sortEpicGroups(groups)

	// The unfiled group is appended LAST and is suppressed when a single epic was
	// requested (`--tree <id>` asked about one box, not about the backlog of
	// unfiled work).
	if rootID == "" && len(unfiled) > 0 {
		groups = append(groups, TreeGroup{Tasks: a.treeNodes(idx, unfiled, doneIDs)})
	}
	if limit > 0 && len(groups) > limit {
		groups = groups[:limit]
	}
	return groups, nil
}

func (a *App) treeNodes(idx *core.Index, tasks []core.Task, doneIDs map[string]bool) []TreeNode {
	out := make([]TreeNode, 0, len(tasks))
	for i := range tasks {
		t := tasks[i]
		actionable, blockedBy := a.factsFor(idx, &t, doneIDs)
		out = append(out, TreeNode{Task: t, Actionable: actionable, BlockedBy: blockedBy})
	}
	return out
}

// sortEpicGroups puts the active epic first, then open epics by id, then closed
// ones. Stable and total, so the print order never depends on map iteration.
func sortEpicGroups(gs []TreeGroup) {
	rank := func(g TreeGroup) int {
		switch {
		case g.Epic == nil:
			return 3 // unfiled always last
		case g.Active && g.Epic.IsOpen():
			return 0
		case g.Epic.IsOpen():
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if ri, rj := rank(gs[i]), rank(gs[j]); ri != rj {
			return ri < rj
		}
		if gs[i].Epic == nil || gs[j].Epic == nil {
			return false
		}
		return gs[i].Epic.ID < gs[j].Epic.ID
	})
}

// factsFor computes the per-task facts that the flat `ls` and `ls --tree` both
// surface, kept as ONE definition so the two views can never disagree about a
// task's state:
//
//   - actionable: in a next lane and every dep done (App.actionable). The ★.
//   - blockedBy: the deps that are NOT done — what is actually in the way (a done
//     dep is history and is left out; always [] not nil).
//
// It is deliberately cheap (no epic lookup): epic-level roll-ups need the member
// index and are computed once per read by epicProgress/epicStuck.
func (a *App) factsFor(idx *core.Index, t *core.Task, doneIDs map[string]bool) (actionable bool, blockedBy []string) {
	return a.actionable(idx, t, doneIDs), blockedDeps(t, doneIDs)
}

// blockedDeps returns t's deps that are not yet done — the "what is in the way"
// list, always non-nil ([] not nil) so a JSON view emits [] rather than null.
func blockedDeps(t *core.Task, doneIDs map[string]bool) []string {
	out := []string{}
	for _, d := range t.Deps {
		if !doneIDs[d] {
			out = append(out, d)
		}
	}
	return out
}

// epicProgress tallies done/total per epic in ONE pass over the full index. It
// replaces v5's recursive roll-up: with no nesting there is no subtree to walk,
// no `seen` set, and no cycle to defend against — the flattening is the point of
// making a box an entity instead of a task.
func epicProgress(idx *core.Index, doneLane string) map[string]Progress {
	out := map[string]Progress{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Epic == "" {
			continue
		}
		p := out[t.Epic]
		p.Total++
		if t.Status == doneLane {
			p.Done++
		}
		out[t.Epic] = p
	}
	return out
}

// epicStuck reports the org-mode "stuck project" state for a box: it has open
// (non-terminal) members but not one of them is actionable. It is the state
// `furrow next` structurally cannot show — next would simply return empty, and
// "empty" reads as "nothing to do" rather than "everything here is blocked".
//
// An epic with NO members is not stuck: declaring the box before filling it is a
// legitimate first step, and nagging about it would train the reader to ignore
// the signal.
func (a *App) epicStuck(idx *core.Index, epicID string, doneIDs map[string]bool) bool {
	open, actionable := 0, 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Epic != epicID || a.Cfg.IsTerminal(t.Status) {
			continue
		}
		open++
		if a.actionable(idx, t, doneIDs) {
			actionable++
		}
	}
	return open > 0 && actionable == 0
}

// actionable is the task-level readiness test: the task sits in a next lane and
// every dep it names is done. It is `ls --tree`'s ★, `ls --actionable`, and
// `is:actionable`.
//
// It is deliberately NOT epic-aware, and that is a change from v5 worth stating:
// `furrow next` now ALSO scopes to the active epic, so ★ is a strict superset of
// what next hands you. Making the glyph epic-aware was the alternative and it is
// worse — a mark whose meaning shifts with which box happens to be open cannot be
// read at a glance, and `ls` is the board-wide view by design.
func (a *App) actionable(idx *core.Index, t *core.Task, doneIDs map[string]bool) bool {
	return a.Cfg.IsNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs)
}
