package core

import (
	"fmt"
	"sort"
	"strings"
)

// CycleProblems reports each dependency-cycle region in the index as one error.
// Cycles are prevented at mutation time (AddDep refuses an edge that would close
// one), but two operators can add the two half-edges on separate shards that git
// merges silently — so lint is the backstop. A cycle makes the involved tasks
// wait on each other forever, so they never surface in `next` (silent
// starvation); hence an error, not a warning.
//
// A "region" is a strongly-connected component (SCC): the maximal set of tasks
// each reachable from every other. Every task in an SCC of size >= 2 sits on at
// least one cycle, so the whole SCC is one entangled knot and is reported once —
// naming EVERY task in it (a diamond of two cycles sharing a node is one region,
// not two half-reported ones). A self-dependency (t-x -> t-x) is the degenerate
// one-node region. The message shows a representative cycle path and, when the
// region is larger than that path, lists the remaining entangled ids.
//
// Only edges between existing ids are followed — an unknown dep contributes no
// edge (Validate reports it separately), so a dangling dep never fabricates a
// cycle. The walk is deterministic: nodes and each node's deps are visited in
// sorted order, and the output is sorted by message.
func CycleProblems(idx *Index) []Problem {
	return cycleProblems(idx, "dep-cycle", "dependency cycle", "mutually blocking",
		func(t *Task) []string { return t.Deps })
}

// cycleProblems is the task-graph front of the shared engine: build the
// adjacency from `edges`, then hand the id graph to cycleProblemsGraph. It is
// parameterized rather than inlined into its one caller because the shape has
// already had two users (deps and, until v6, the `parent` hierarchy), and since
// v7 the epic dep graph is the third — EpicDepCycleProblems (epic_lint.go)
// enters at cycleProblemsGraph, the id level, instead of growing a second walk.
func cycleProblems(idx *Index, code, label, knot string, edges func(*Task) []string) []Problem {
	ids := make(map[string]bool, len(idx.Tasks))
	for i := range idx.Tasks {
		ids[idx.Tasks[i].ID] = true
	}
	// Adjacency limited to known ids, each node's edges sorted for determinism.
	adj := make(map[string][]string, len(idx.Tasks))
	order := make([]string, 0, len(idx.Tasks))
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		order = append(order, t.ID)
		var outs []string
		for _, d := range edges(t) {
			if ids[d] {
				outs = append(outs, d)
			}
		}
		sort.Strings(outs)
		adj[t.ID] = outs
	}
	sort.Strings(order)
	return cycleProblemsGraph(order, adj, code, label, knot)
}

// cycleProblemsGraph reports each cyclic region of an id graph as one Problem.
// order must be sorted and adj's edge lists sorted and limited to known ids —
// the caller owns building the graph (a dangling edge must not fabricate a
// cycle), this owns the decomposition and the deterministic report. Entity-
// agnostic on purpose: task deps and epic deps are both "ids waiting on ids",
// and one engine is what keeps their cycle reports reading the same.
func cycleProblemsGraph(order []string, adj map[string][]string, code, label, knot string) []Problem {
	var out []Problem
	for _, scc := range stronglyConnected(order, adj) {
		if !isCyclicSCC(scc, adj) {
			continue // a lone task with no self-edge is not a cycle
		}
		sort.Strings(scc)
		member := make(map[string]bool, len(scc))
		for _, id := range scc {
			member[id] = true
		}
		cyc := representativeCycle(scc[0], adj, member)
		msg := fmt.Sprintf("%s: %s -> %s", label, strings.Join(cyc, " -> "), cyc[0])
		// When the region is bigger than the representative path, the path alone
		// would leave some entangled tasks unnamed — list the whole knot so an
		// operator sees every task caught in it, not just one example loop.
		if len(cyc) < len(scc) {
			msg += fmt.Sprintf(" (%s: %s)", knot, strings.Join(scc, ", "))
		}
		out = append(out, Problem{SevError, code, scc[0], msg})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Msg < out[j].Msg })
	return out
}

// stronglyConnected returns the SCCs of the graph (Tarjan's algorithm). Roots
// are visited in the given (sorted) order and each node's neighbors come
// pre-sorted, so the decomposition is deterministic.
func stronglyConnected(order []string, adj map[string][]string) [][]string {
	index := 0
	idxOf := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var sccs [][]string

	var connect func(v string)
	connect = func(v string) {
		idxOf[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := idxOf[w]; !seen {
				connect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if idxOf[w] < low[v] {
					low[v] = idxOf[w]
				}
			}
		}
		if low[v] == idxOf[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}
	for _, v := range order {
		if _, seen := idxOf[v]; !seen {
			connect(v)
		}
	}
	return sccs
}

// isCyclicSCC reports whether an SCC contains a cycle: any component of size >= 2
// does, and a size-1 component does only if the node depends on itself.
func isCyclicSCC(scc []string, adj map[string][]string) bool {
	if len(scc) > 1 {
		return true
	}
	v := scc[0]
	for _, w := range adj[v] {
		if w == v {
			return true
		}
	}
	return false
}

// representativeCycle returns a shortest cycle through start within the SCC (a
// concrete id sequence for the message), using a BFS over the region's internal
// edges. A self-loop yields the one-node cycle [start].
func representativeCycle(start string, adj map[string][]string, member map[string]bool) []string {
	for _, w := range adj[start] {
		if w == start {
			return []string{start}
		}
	}
	prev := map[string]string{}
	visited := map[string]bool{start: true}
	queue := []string{start}
	closing := ""
	for len(queue) > 0 && closing == "" {
		u := queue[0]
		queue = queue[1:]
		for _, w := range adj[u] {
			if !member[w] {
				continue
			}
			if w == start {
				closing = u
				break
			}
			if !visited[w] {
				visited[w] = true
				prev[w] = u
				queue = append(queue, w)
			}
		}
	}
	if closing == "" {
		return []string{start} // unreachable in a cyclic SCC; guards against a hang
	}
	// Rebuild start -> ... -> closing by walking prev backwards from closing.
	rev := []string{}
	for u := closing; u != start; u = prev[u] {
		rev = append(rev, u)
	}
	rev = append(rev, start)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
