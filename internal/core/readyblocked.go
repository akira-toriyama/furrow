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
// caller's vocabulary ([next].lanes), never core's.
func ReadyBlockedProblems(idx *Index, nextLanes, doneIDs map[string]bool) []Problem {
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !nextLanes[t.Status] {
			continue
		}
		var open []string
		for _, dep := range t.Deps {
			if !doneIDs[dep] {
				open = append(open, dep)
			}
		}
		if len(open) > 0 {
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
