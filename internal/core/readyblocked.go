package core

import (
	"fmt"
	"sort"
	"strings"
)

// ReadyBlockedProblems reports each task parked in an actionable (next) lane
// while a dependency is still unsatisfied. Such a task is a contradiction the
// board cannot see from either side alone: its lane says "startable now", but
// `furrow next` will never hand it out (Actionable refuses a task with an
// unsatisfied dep), so it sits invisible in exactly the place the operator
// expects everything to be workable. `brief` shows the same set as `blocked` —
// a read-side view for the session that runs `brief`; this is the always-on
// (hook/CI) twin that makes the state a finding, not a glance.
//
// An ERROR, not a warn: one of the two fields is simply wrong — either the
// lane (the work is not startable; move it back) or the dep edge (the work IS
// startable; drop the dep). Unsatisfied means exactly what Actionable means:
// the dep id is not in doneIDs, so an unknown dep counts too (Validate's
// dep-missing reports the missing id itself; this rule reports the lane
// contradiction it causes). One finding per task, naming every unsatisfied dep
// — the remedy is per-task, not per-edge. Which lanes are "actionable" is the
// caller's vocabulary ([next].lanes), never core's — as is which lanes are
// terminal, which the message uses to split the deps by KIND of blockage: a
// dep that is merely open resolves by waiting (someone is doing it), but a dep
// PARKED in a terminal non-done lane (icebox, waiting) will never complete on
// its own, so "wait" is not among its remedies — unpark it, drop the edge, or
// park this task too. One code for both: the lane contradiction is the same
// defect; only the way out differs, and the message is where remedies live.
func ReadyBlockedProblems(idx *Index, nextLanes, terminal, doneIDs map[string]bool) []Problem {
	laneOf := make(map[string]string, len(idx.Tasks))
	for i := range idx.Tasks {
		laneOf[idx.Tasks[i].ID] = idx.Tasks[i].Status
	}
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !nextLanes[t.Status] {
			continue
		}
		var open, parked []string
		for _, dep := range t.Deps {
			if doneIDs[dep] {
				continue
			}
			if lane, ok := laneOf[dep]; ok && terminal[lane] {
				parked = append(parked, fmt.Sprintf("%s (parked in %s)", dep, lane))
				continue
			}
			open = append(open, dep)
		}
		switch {
		case len(parked) > 0:
			all := append(append([]string(nil), open...), parked...)
			out = append(out, Problem{SevError, "ready-blocked", t.ID, fmt.Sprintf(
				"task is in actionable lane %q but dep(s) %s are not done — and a PARKED dep never completes on its own: unpark it, drop the edge (`furrow dep %s <dep> --rm`), or park this task too",
				t.Status, strings.Join(all, ", "), t.ID)})
		case len(open) > 0:
			out = append(out, Problem{SevError, "ready-blocked", t.ID, fmt.Sprintf(
				"task is in actionable lane %q but dep(s) %s are not done — `furrow next` will never hand it out; move it back or drop the dep",
				t.Status, strings.Join(open, ", "))})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].ID != out[b].ID {
			return out[a].ID < out[b].ID
		}
		return out[a].Msg < out[b].Msg
	})
	return out
}
