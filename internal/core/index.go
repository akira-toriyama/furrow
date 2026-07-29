package core

// Index operations. All pure: they mutate or query an in-memory *Index and never
// touch the filesystem. The store loads/saves; these shape what is in memory.

import "sort"

// Find returns a pointer to the task with the given id and its slice position,
// or (nil, -1). The pointer is into idx.Tasks, so mutating it mutates the index
// (callers re-Save afterward).
func (idx *Index) Find(id string) (*Task, int) {
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == id {
			return &idx.Tasks[i], i
		}
	}
	return nil, -1
}

// Has reports whether an id is present.
func (idx *Index) Has(id string) bool {
	_, i := idx.Find(id)
	return i >= 0
}

// Add appends a task. The caller is responsible for a unique id (the store's
// NextID guarantees it); Add does not check, so migrate can bulk-load.
func (idx *Index) Add(t Task) {
	idx.Tasks = append(idx.Tasks, t)
}

// Remove deletes the task with id, returning whether it was present.
func (idx *Index) Remove(id string) bool {
	_, i := idx.Find(id)
	if i < 0 {
		return false
	}
	idx.Tasks = append(idx.Tasks[:i], idx.Tasks[i+1:]...)
	return true
}

// NextPriority returns a sparse priority that places a new task after every
// existing task in the same lane: max(priority in lane) + step, or `base` when
// the lane is empty. This is what keeps `add` from renumbering anything.
func (idx *Index) NextPriority(lane string, base, step int) int {
	max := -1
	for _, t := range idx.Tasks {
		if t.Status == lane && t.Priority > max {
			max = t.Priority
		}
	}
	if max < 0 {
		return base
	}
	return max + step
}

// PriorityChange records one neighbor's priority move inside a respace plan —
// the machine-readable receipt for "inserting here had to renumber the lane".
type PriorityChange struct {
	ID   string `json:"id"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// PlanRelativePriority computes the priority that places task id immediately
// before (or after) task ref in ref's lane — the lane's display order is
// (priority, then id), the same tie-break the marshaller sorts by. Both tasks
// must exist and share a lane (relative position across lanes is meaningless);
// planning against a ref in another lane is a validation error, and id == ref
// is too.
//
// When an integer strictly between the two neighbors exists, the plan is just
// that midpoint and no other task moves. When the gap is exhausted (adjacent or
// duplicate priorities), the plan respaces the WHOLE lane — id slotted at its
// requested position, every lane member renumbered to base, base+step, … — so
// one respace restores the sparse spacing for every future insert. The returned
// changes list the OTHER tasks' moves (id's own target is the first return);
// the caller applies target + changes in one write, all-or-nothing, so the
// renumber can never half-land.
func (idx *Index) PlanRelativePriority(id, ref string, before bool, base, step int) (int, []PriorityChange, error) {
	if id == ref {
		return 0, nil, Validationf(id, "--before/--after must name a different task")
	}
	t, i := idx.Find(id)
	if i < 0 {
		return 0, nil, NotFound(id)
	}
	rt, ri := idx.Find(ref)
	if ri < 0 {
		return 0, nil, NotFound(ref)
	}
	if rt.Status != t.Status {
		return 0, nil, Validationf(id, "relative target %s is in lane %q, not %s's lane %q — relative order only exists within one lane", ref, rt.Status, id, t.Status)
	}

	// The lane's current display order, with id itself excluded: it is being
	// re-slotted, so its old position must not influence the neighbors.
	type slot struct {
		id  string
		pri int
	}
	var lane []slot
	for j := range idx.Tasks {
		tt := &idx.Tasks[j]
		if tt.Status == t.Status && tt.ID != id {
			lane = append(lane, slot{tt.ID, tt.Priority})
		}
	}
	sort.Slice(lane, func(a, b int) bool {
		if lane[a].pri != lane[b].pri {
			return lane[a].pri < lane[b].pri
		}
		return lane[a].id < lane[b].id
	})
	r := 0
	for j := range lane {
		if lane[j].id == ref {
			r = j
			break
		}
	}
	ins := r // insertion index into lane
	if !before {
		ins = r + 1
	}

	// Cheap path: a free integer strictly between the two neighbors.
	var lo, hi *int
	if ins > 0 {
		lo = &lane[ins-1].pri
	}
	if ins < len(lane) {
		hi = &lane[ins].pri
	}
	switch {
	case lo == nil: // ref is the lane's first task; hi != nil because ref exists
		return *hi - step, nil, nil
	case hi == nil: // ref is the lane's last task
		return *lo + step, nil, nil
	default:
		if gap := *hi - *lo; gap >= 2 {
			return *lo + gap/2, nil, nil
		}
	}

	// Gap exhausted: respace the whole lane with id in its slot.
	target := 0
	var changes []PriorityChange
	pri := base
	for j := 0; j <= len(lane); j++ {
		switch {
		case j == ins:
			target = pri
		case j < ins:
			if lane[j].pri != pri {
				changes = append(changes, PriorityChange{ID: lane[j].id, From: lane[j].pri, To: pri})
			}
		default:
			if lane[j-1].pri != pri {
				changes = append(changes, PriorityChange{ID: lane[j-1].id, From: lane[j-1].pri, To: pri})
			}
		}
		pri += step
	}
	return target, changes, nil
}

// Actionable reports the DEPENDENCY half of readiness: the task is not in a
// terminal/parked lane and every dependency it names is itself done. The
// terminal lane set and the set of done ids are supplied by the caller (lane
// semantics live in config, not core).
//
// It is NOT the full `furrow next` test. next-eligibility also requires a NEXT
// lane and a non-container type, both of which live in app (workable /
// actionable in internal/app) because both are config vocabulary. A task can be
// Actionable here and still, correctly, never appear in `next`.
func (idx *Index) Actionable(t *Task, terminal map[string]bool, doneIDs map[string]bool) bool {
	if terminal[t.Status] {
		return false
	}
	for _, dep := range t.Deps {
		// A dep that is unknown or not-yet-done blocks the task. (lint reports
		// unknown deps separately; here an unknown dep is conservatively
		// treated as unsatisfied so `next` never suggests a blocked task.)
		if !doneIDs[dep] {
			return false
		}
	}
	return true
}

// Dependents returns the tasks that depend on id — those naming id in their
// Deps — in index (canonical) order. It is the reverse of the Deps edge: id
// "blocks" each returned task. This is the shared reverse-deps helper the CLI's
// `dep --list` and future TUI dep views read (so the relation is computed in one
// place). An unknown id simply has no dependents (never panics).
func (idx *Index) Dependents(id string) []Task {
	var out []Task
	for i := range idx.Tasks {
		for _, dep := range idx.Tasks[i].Deps {
			if dep == id {
				out = append(out, idx.Tasks[i])
				break
			}
		}
	}
	return out
}

// DependsOn reports whether task `a` reaches task `b` by following dependency
// edges transitively (a depends on b, directly or indirectly). It underpins
// acyclic dep edits: adding a->b is safe only when b does not already depend on
// a. Unknown ids contribute no out-edges; a visited set keeps the walk finite
// even if the index already contains a cycle.
func (idx *Index) DependsOn(a, b string) bool {
	visited := map[string]bool{}
	var stack []string
	if t, i := idx.Find(a); i >= 0 {
		stack = append(stack, t.Deps...)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == b {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if t, i := idx.Find(cur); i >= 0 {
			stack = append(stack, t.Deps...)
		}
	}
	return false
}
