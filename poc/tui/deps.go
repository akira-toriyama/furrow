package main

import "sort"

// Graph is the ONE place "blocked", "actionable", "reverse deps", "container",
// "stuck" and "progress" are defined. The card glyph, the filter, the detail
// pane and the dep-tree overlay all read it, so they cannot drift apart.
//
// It is rebuilt after every mutation — with 24 tasks (and 658 on the real
// board, 102 edges) that is free, and it removes a whole class of stale-cache
// bugs.
type Graph struct {
	b    *Board
	rev  map[string][]string // id -> ids that depend on it
	kids map[string][]string // parent id -> child ids, in lane/priority order
}

// NewGraph indexes the reverse dep and parent edges of a board.
func NewGraph(b *Board) *Graph {
	g := &Graph{b: b, rev: map[string][]string{}, kids: map[string][]string{}}
	for _, t := range b.Tasks() {
		for _, d := range t.Deps {
			g.rev[d] = append(g.rev[d], t.ID)
		}
		if t.Parent != "" {
			g.kids[t.Parent] = append(g.kids[t.Parent], t.ID)
		}
	}
	for k := range g.rev {
		sort.Strings(g.rev[k])
	}
	for k := range g.kids {
		ids := g.kids[k]
		sort.SliceStable(ids, func(i, j int) bool {
			a, bb := b.Task(ids[i]), b.Task(ids[j])
			if a == nil || bb == nil {
				return ids[i] < ids[j]
			}
			if la, lb := b.LaneIndex(a.Status), b.LaneIndex(bb.Status); la != lb {
				return la < lb
			}
			return a.Priority < bb.Priority
		})
	}
	return g
}

// Board is the board this graph indexes.
func (g *Graph) Board() *Board { return g.b }

// Known reports whether the id names a task on the board. A dep pointing at an
// absent id renders "?" rather than silently counting as satisfied.
func (g *Graph) Known(id string) bool { return g.b.Task(id) != nil }

// IsDone reports whether the task exists and sits in a done lane.
func (g *Graph) IsDone(id string) bool {
	t := g.b.Task(id)
	return t != nil && g.b.isDoneLane(t.Status)
}

// IsContainer reports whether the task's effective type is a container type —
// a box, not work. Declared, never inferred from structure: an empty epic is
// still a container and a plain task with children is not.
func (g *Graph) IsContainer(id string) bool {
	t := g.b.Task(id)
	return t != nil && containerTypes[t.EffectiveType()]
}

// BlockedBy lists the task's deps that are NOT done, in dep order. An unknown
// dep counts as blocking: we cannot prove it is satisfied.
func (g *Graph) BlockedBy(id string) []string {
	t := g.b.Task(id)
	if t == nil {
		return nil
	}
	var out []string
	for _, d := range t.Deps {
		if !g.IsDone(d) {
			out = append(out, d)
		}
	}
	return out
}

// Blocks lists the tasks that depend on this one — the reverse edges, which
// exist nowhere on disk and are the half of a dep a raw shard cannot show you.
func (g *Graph) Blocks(id string) []string { return g.rev[id] }

// OpenBlocks is Blocks restricted to tasks that are not done, i.e. what would
// actually be unblocked by closing this task.
func (g *Graph) OpenBlocks(id string) []string {
	var out []string
	for _, x := range g.rev[id] {
		if !g.IsDone(x) {
			out = append(out, x)
		}
	}
	return out
}

// Actionable is exactly what `furrow next` would hand you: in a next lane,
// every dep done, and not a container (a box is not work).
func (g *Graph) Actionable(id string) bool {
	t := g.b.Task(id)
	if t == nil {
		return false
	}
	l := g.b.Lane(t.Status)
	if l == nil || !l.Next {
		return false
	}
	if g.IsContainer(id) {
		return false
	}
	return len(g.BlockedBy(id)) == 0
}

// Children lists a container's direct children.
func (g *Graph) Children(id string) []string { return g.kids[id] }

// Progress rolls up child completion for a container. recursive walks the whole
// subtree (furrow's --progress-recursive); otherwise direct children only.
func (g *Graph) Progress(id string, recursive bool) (done, total int) {
	seen := map[string]bool{id: true}
	var walk func(string)
	walk = func(p string) {
		for _, c := range g.kids[p] {
			if seen[c] {
				continue
			}
			seen[c] = true
			total++
			if g.IsDone(c) {
				done++
			}
			if recursive {
				walk(c)
			}
		}
	}
	walk(id)
	return done, total
}

// Stuck is org-mode's stuck project: a container with open work under it but no
// actionable descendant anywhere in its subtree. It always walks the subtree,
// through sub-epics.
func (g *Graph) Stuck(id string) bool {
	if !g.IsContainer(id) {
		return false
	}
	var open, act int
	seen := map[string]bool{id: true}
	var walk func(string)
	walk = func(p string) {
		for _, c := range g.kids[p] {
			if seen[c] {
				continue
			}
			seen[c] = true
			if !g.IsDone(c) {
				open++
				if g.Actionable(c) {
					act++
				}
			}
			walk(c)
		}
	}
	walk(id)
	return open > 0 && act == 0
}

// ChildrenDone reports a container whose children are all closed — consider
// closing the box.
func (g *Graph) ChildrenDone(id string) bool {
	if !g.IsContainer(id) {
		return false
	}
	done, total := g.Progress(id, false)
	return total > 0 && done == total
}

// depDir selects which way TreeOf walks.
type depDir int

const (
	// dirBlockedBy walks "what must finish before this" (task -> its deps).
	dirBlockedBy depDir = iota
	// dirBlocks walks "what this unblocks" (task -> its reverse deps).
	dirBlocks
)

// depNode is one node of a rendered dep tree.
type depNode struct {
	ID       string
	Elided   bool // a done subtree, collapsed
	Repeat   bool // already drawn elsewhere in this tree (shared node in a DAG)
	Children []*depNode
}

// TreeOf builds a depth-capped, cycle-safe dependency tree in one direction.
//
// It is a TREE over a DAG: a node reachable by two paths is drawn twice (marked
// Repeat) rather than merged, because lipgloss/v2's tree has no multi-parent
// support. Measured over the real board that costs ~2.5% extra rows — far
// cheaper than a Sugiyama layout engine, which no Go library provides and which
// a median live component of 2 nodes does not justify.
//
// Done subtrees are elided: once a blocker is closed, its own blockers are
// history, not context.
func (g *Graph) TreeOf(id string, dir depDir, maxDepth int) *depNode {
	drawn := map[string]bool{}
	var build func(string, int, map[string]bool) *depNode
	build = func(cur string, depth int, path map[string]bool) *depNode {
		n := &depNode{ID: cur}
		if drawn[cur] && depth > 0 {
			n.Repeat = true
			return n
		}
		drawn[cur] = true
		if depth >= maxDepth || path[cur] {
			return n
		}
		if depth > 0 && g.IsDone(cur) {
			n.Elided = true
			return n
		}
		var next []string
		if dir == dirBlockedBy {
			if t := g.b.Task(cur); t != nil {
				next = t.Deps
			}
		} else {
			next = g.rev[cur]
		}
		sub := map[string]bool{cur: true}
		for k := range path {
			sub[k] = true
		}
		for _, c := range next {
			n.Children = append(n.Children, build(c, depth+1, sub))
		}
		return n
	}
	return build(id, 0, map[string]bool{})
}
