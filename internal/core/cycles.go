package core

import (
	"fmt"
	"sort"
	"strings"
)

// CycleProblems reports each dependency cycle in the index as one error listing
// the ids around the loop (e.g. "dependency cycle: t-a -> t-b -> t-a"). Cycles
// are prevented at mutation time (AddDep refuses an edge that would close one),
// but two operators can add the two halves on separate shards that git merges
// silently — so lint is the backstop. A cycle makes the involved tasks wait on
// each other forever, so they never surface in `next` (silent starvation);
// hence an error, not a warning. A self-dependency (t-x -> t-x) is the
// degenerate one-node cycle and is reported once.
//
// Only edges between existing ids are followed — an unknown dep contributes no
// edge (Validate reports it separately), so a dangling dep never fabricates a
// cycle. Each distinct cycle is reported once no matter how many DFS entry
// points reach it, and the walk is deterministic: root ids and each node's deps
// are visited in sorted order.
func CycleProblems(idx *Index) []Problem {
	ids := make(map[string]bool, len(idx.Tasks))
	for i := range idx.Tasks {
		ids[idx.Tasks[i].ID] = true
	}
	// Adjacency limited to known ids, each node's deps sorted for determinism.
	adj := make(map[string][]string, len(idx.Tasks))
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		var outs []string
		for _, d := range t.Deps {
			if ids[d] {
				outs = append(outs, d)
			}
		}
		sort.Strings(outs)
		adj[t.ID] = outs
	}

	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS path
		black = 2 // fully explored
	)
	color := make(map[string]int, len(idx.Tasks))
	var path []string
	reported := map[string]bool{}
	var out []Problem

	var dfs func(u string)
	dfs = func(u string) {
		color[u] = gray
		path = append(path, u)
		for _, v := range adj[u] {
			switch color[v] {
			case white:
				dfs(v)
			case gray:
				// Back-edge u->v: the cycle is the path from v to u, closing to v.
				key, canon := canonicalizeCycle(cycleSlice(path, v))
				if !reported[key] {
					reported[key] = true
					out = append(out, Problem{SevError, canon[0],
						fmt.Sprintf("dependency cycle: %s -> %s", strings.Join(canon, " -> "), canon[0])})
				}
			}
		}
		path = path[:len(path)-1]
		color[u] = black
	}

	order := make([]string, 0, len(idx.Tasks))
	for i := range idx.Tasks {
		order = append(order, idx.Tasks[i].ID)
	}
	sort.Strings(order)
	for _, id := range order {
		if color[id] == white {
			dfs(id)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Msg < out[j].Msg })
	return out
}

// cycleSlice returns the tail of path starting at v — the portion of the current
// DFS path that a back-edge to v closes into a cycle.
func cycleSlice(path []string, v string) []string {
	for i, id := range path {
		if id == v {
			return append([]string(nil), path[i:]...)
		}
	}
	return nil // unreachable: v is gray, hence on the path
}

// canonicalizeCycle rotates cyc so its lexicographically smallest id leads,
// giving a rotation-invariant dedup key and a stable display order regardless of
// which node the DFS entered from.
func canonicalizeCycle(cyc []string) (key string, canon []string) {
	min := 0
	for i := range cyc {
		if cyc[i] < cyc[min] {
			min = i
		}
	}
	canon = append(append([]string(nil), cyc[min:]...), cyc[:min]...)
	return strings.Join(canon, ","), canon
}
