package app

import "github.com/akira-toriyama/furrow/internal/core"

// TaskRef is an EDGE resolved for legibility: the referenced task's id plus its
// title and lane, so a reader (agent or human) sees what an edge points at without
// a second lookup. A dangling ref (an id naming no task — lint's dep-missing)
// resolves to the id with an empty Title and Status, so a broken edge is still
// reported rather than vanishing.
//
// BlockedBy is the referenced task's OWN not-yet-done deps (blockedDeps — the
// same fact every ls/show row carries), always non-nil. It is what lets a
// `dep --list` blocks row answer "what moves if I close this" without a second
// read per row: the subject is that row's last open dep exactly when BlockedBy
// is [subject] (t-wwk2 — seven drill runs retyped `show` per row for this).
// A dangling ref has no deps to report: [].
type TaskRef struct {
	ID        string
	Title     string
	Status    string
	BlockedBy []string
}

// DepListResult is the read-only, both-directions view of a task's dependency
// graph neighborhood: what it DependsOn (its own Deps — what it waits on) and
// what it Blocks (the reverse edge — the tasks waiting on it). Both slices are
// always non-nil (so JSON is [] not null) and in canonical order. Done says
// whether the subject itself sits in the done lane — the renderer's cue that
// "once this closes" has already happened.
type DepListResult struct {
	ID        string
	Title     string
	Done      bool
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
		return DepListResult{}, a.notFoundTask(id)
	}
	doneIDs := a.doneSet(idx)
	res := DepListResult{ID: t.ID, Title: t.Title, Done: doneIDs[t.ID], DependsOn: []TaskRef{}, Blocks: []TaskRef{}}
	for _, depID := range t.Deps {
		res.DependsOn = append(res.DependsOn, resolveTaskRef(idx, depID, doneIDs))
	}
	for _, dt := range idx.Dependents(id) {
		res.Blocks = append(res.Blocks, TaskRef{ID: dt.ID, Title: dt.Title, Status: dt.Status, BlockedBy: blockedDeps(&dt, doneIDs)})
	}
	return res, nil
}

// resolveTaskRef yields the id alone, with an empty title/status and no
// BlockedBy, for a dangling id: the edge is still reported (faithful to the
// shard) and lint's dep-missing finding is the place that flags it as a problem.
func resolveTaskRef(idx *core.Index, id string, doneIDs map[string]bool) TaskRef {
	if t, i := idx.Find(id); i >= 0 {
		return TaskRef{ID: t.ID, Title: t.Title, Status: t.Status, BlockedBy: blockedDeps(t, doneIDs)}
	}
	return TaskRef{ID: id, BlockedBy: []string{}}
}
