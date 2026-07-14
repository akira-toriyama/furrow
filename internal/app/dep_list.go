package app

import "github.com/akira-toriyama/furrow/internal/core"

// TaskRef is an EDGE resolved for legibility: the referenced task's id plus its
// title and lane, so a reader (agent or human) sees what an edge points at without
// a second lookup. It is edge-agnostic on purpose — a dep, a parent, and a child
// are all "a task this one is wired to", and one shape for all three is what keeps
// `dep --list` and `parent --list` reading the same. A dangling ref (an id naming
// no task — lint's dep-missing / parent-missing) resolves to the id with an empty
// Title and Status, so a broken edge is still reported rather than vanishing.
type TaskRef struct {
	ID     string
	Title  string
	Status string
}

// DepListResult is the read-only, both-directions view of a task's dependency
// graph neighborhood: what it DependsOn (its own Deps — what it waits on) and
// what it Blocks (the reverse edge — the tasks waiting on it). Both slices are
// always non-nil (so JSON is [] not null) and in canonical order.
type DepListResult struct {
	ID        string
	Title     string
	DependsOn []TaskRef
	Blocks    []TaskRef
}

// DepList resolves a task's dependency neighborhood in both directions in one
// index load: DependsOn are the task's own Deps (what it waits on), Blocks are
// the tasks that name it in their Deps (what waits on it — via core.Dependents,
// the shared reverse-deps helper). Each edge is resolved to id+title+status.
// NotFound (exit 1) when id names no task; a zero-edge result is a clean object,
// never an error.
func (a *App) DepList(id string) (DepListResult, error) {
	idx, err := a.load()
	if err != nil {
		return DepListResult{}, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return DepListResult{}, core.NotFound(id)
	}
	res := DepListResult{ID: t.ID, Title: t.Title, DependsOn: []TaskRef{}, Blocks: []TaskRef{}}
	for _, depID := range t.Deps {
		res.DependsOn = append(res.DependsOn, resolveTaskRef(idx, depID))
	}
	for _, dt := range idx.Dependents(id) {
		res.Blocks = append(res.Blocks, TaskRef{ID: dt.ID, Title: dt.Title, Status: dt.Status})
	}
	return res, nil
}

// resolveTaskRef looks up id in the index, returning its id+title+status. A
// dangling id (no such task) yields the id alone with an empty title/status, so
// the edge is still reported (faithful to the shard) and lint's dep-missing /
// parent-missing finding is the place that flags it as a problem.
func resolveTaskRef(idx *core.Index, id string) TaskRef {
	if t, i := idx.Find(id); i >= 0 {
		return TaskRef{ID: t.ID, Title: t.Title, Status: t.Status}
	}
	return TaskRef{ID: id}
}
