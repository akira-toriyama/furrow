package core

import (
	"fmt"
	"sort"
	"strings"
)

// The epic dep graph (v7): pure helpers over an epic set's Deps edges. They
// mirror the task graph's split of duties — a cheap transitive walk for the
// WRITE path (EpicDependsOn, what EpicAddDeps refuses a cycle with) and the
// SCC-based lint backstop for what git merges let through (epicDepProblems).

// EpicDependsOn reports whether epic `from` transitively waits on epic `to`
// through Deps edges — the epic twin of Index.DependsOn, and the write-path
// cycle guard: adding `dep` to `id` is refused when EpicDependsOn(dep, id).
// Unknown ids contribute no edges, so a dangling dep never fabricates a path.
func EpicDependsOn(epics []Epic, from, to string) bool {
	byID := make(map[string]*Epic, len(epics))
	for i := range epics {
		byID[epics[i].ID] = &epics[i]
	}
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == to {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		e, ok := byID[id]
		if !ok {
			return false
		}
		for _, d := range e.Deps {
			if walk(d) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

// epicDepProblems runs the consistency rules over the epic dep graph alone.
// Called from EpicProblems so every lint caller gets them for free.
//
//   - epic-dep-missing (error) a dep naming no epic — the epic twin of
//     dep-missing. An epic id is never reused, so a dangling edge is a typo or
//     a half-merged hand edit, not history.
//   - epic-dep-cycle   (error) boxes waiting on each other. Prevented at
//     mutation time (EpicAddDeps refuses the closing edge), so like dep-cycle
//     this is the git-merge backstop; a cycle makes epic_dep_done unreachable
//     for every box in it — silent starvation, hence an error.
//   - epic-dep-open    (warn)  an ACTIVE epic still waiting on an open box.
//     Deliberately a warning, never a gate: `epic activate` already said this
//     out loud and proceeded (the ordering is advice, and furrow never chooses
//     — or refuses — which box a human opens), so lint's job is only to keep
//     the out-of-order state visible after the fact.
func epicDepProblems(epics []Epic) []Problem {
	var out []Problem

	byID := make(map[string]*Epic, len(epics))
	for i := range epics {
		byID[epics[i].ID] = &epics[i]
	}

	// Adjacency limited to known ids, sorted, for the deterministic SCC walk.
	order := make([]string, 0, len(epics))
	adj := make(map[string][]string, len(epics))
	for i := range epics {
		e := &epics[i]
		order = append(order, e.ID)
		var outs []string
		for _, d := range e.Deps {
			if _, ok := byID[d]; ok {
				outs = append(outs, d)
			} else {
				out = append(out, Problem{SevError, "epic-dep-missing", e.ID,
					fmt.Sprintf("dep epic %q does not exist", d)})
			}
		}
		sort.Strings(outs)
		adj[e.ID] = outs
	}
	sort.Strings(order)
	out = append(out, cycleProblemsGraph(order, adj, "epic-dep-cycle", "epic dependency cycle", "mutually waiting")...)

	for i := range epics {
		e := &epics[i]
		if !e.Active || !e.IsOpen() {
			continue
		}
		var open []string
		for _, d := range e.Deps {
			if dep, ok := byID[d]; ok && dep.IsOpen() {
				open = append(open, d)
			}
		}
		if len(open) > 0 {
			out = append(out, Problem{SevWarn, "epic-dep-open", e.ID, fmt.Sprintf(
				"active but still waiting on open epic(s) %s — close or detach them (`furrow epic dep %s <dep-id> --rm`), or carry on knowingly",
				strings.Join(open, ", "), e.ID)})
		}
	}
	return out
}
