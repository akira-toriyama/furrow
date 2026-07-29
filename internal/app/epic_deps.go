package app

import (
	"sort"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The epic dep graph's app layer (v7): the write funnel for `furrow epic dep`
// and its read-only --list view, mirroring the task-side AddDeps/RemoveDeps/
// DepList so the two entities' dep commands read the same. One deliberate
// difference: dep arguments are RESOLVED like every other epic reference
// (exact id, unique prefix, unique title substring — with candidates on a
// miss), because the epic command family already speaks refs and a stricter
// rule on this one subcommand would be a trap.

// EpicRef is one epic dep edge resolved for legibility: id plus title and
// state, the epic twin of TaskRef. State is "open" or "closed" — an epic has
// no lane. A dangling edge (an id naming no epic — lint's epic-dep-missing)
// resolves to the id with an empty Title and State, so a broken edge is still
// reported rather than vanishing.
type EpicRef struct {
	ID    string
	Title string
	State string
}

// EpicDepListResult is the read-only, both-directions view of an epic's dep
// neighborhood: what it DependsOn (its own Deps — the boxes it waits on) and
// what it Blocks (the reverse edge — the boxes waiting on it). Both slices are
// always non-nil (so JSON is [] not null) and in canonical order.
type EpicDepListResult struct {
	ID        string
	Title     string
	DependsOn []EpicRef
	Blocks    []EpicRef
}

// EpicAddDeps makes `ref` wait on each of depRefs in one write (`epic dep`).
// Every dep is resolved and validated up front — must exist, must not be the
// epic itself, must not close a cycle (checked against the graph as the batch
// grows, so an in-batch edge counts) — and a dep already present is a no-op.
// Validation is all-or-nothing: the first bad dep returns before any save. A
// CLOSED dep is legal — it is simply a satisfied one (filing "this box waited
// on that finished work" is a legitimate record). The marshaller keeps the
// stored set sorted+deduped.
func (a *App) EpicAddDeps(ref string, depRefs []string) (*core.Epic, *core.Epic, error) {
	if err := requireNonBlank(ref, "dep", depRefs); err != nil {
		return nil, nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, nil, err
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		return nil, nil, err
	}
	var target *core.Epic
	for i := range epics {
		if epics[i].ID == id {
			target = &epics[i]
		}
	}
	// resolveEpicIn only returns ids that exist, so target is never nil.
	added := []string{}
	for _, dr := range depRefs {
		dep, err := a.resolveEpicIn(dr, epics)
		if err != nil {
			return nil, nil, err
		}
		if dep == id {
			return nil, nil, core.Validationf(id, "an epic cannot depend on itself")
		}
		if core.EpicDependsOn(epics, dep, id) {
			return nil, nil, core.Validationf(id, "adding dep %q would create a cycle (%s already waits on %s)", dep, dep, id)
		}
		if !contains(target.Deps, dep) {
			// Mutating the loaded slice is what makes the NEXT iteration's cycle
			// check see this batch's earlier edges.
			target.Deps = append(target.Deps, dep)
			added = append(added, dep)
		}
	}
	return a.mutateEpic(id, func(e *core.Epic) error {
		e.Deps = append(e.Deps, added...)
		return nil
	})
}

// EpicRemoveDeps drops several deps from `ref` in one write. Each must be a
// current dep (else a validation error naming it — never a silent no-op), and
// the whole batch is validated before any change. A dep argument that exactly
// matches a stored edge is accepted VERBATIM before ref resolution runs:
// removing a dangling edge (epic-dep-missing's fix) must not require the dep
// to still exist.
func (a *App) EpicRemoveDeps(ref string, depRefs []string) (*core.Epic, *core.Epic, error) {
	if err := requireNonBlank(ref, "dep", depRefs); err != nil {
		return nil, nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, nil, err
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		return nil, nil, err
	}
	var target *core.Epic
	for i := range epics {
		if epics[i].ID == id {
			target = &epics[i]
		}
	}
	rm := make(map[string]bool, len(depRefs))
	for _, dr := range depRefs {
		dep := dr
		if !contains(target.Deps, dep) {
			resolved, err := a.resolveEpicIn(dr, epics)
			if err != nil {
				return nil, nil, err
			}
			dep = resolved
		}
		if !contains(target.Deps, dep) {
			return nil, nil, core.Validationf(id, "%q is not a dependency of %s", dep, id)
		}
		rm[dep] = true
	}
	return a.mutateEpic(id, func(e *core.Epic) error {
		kept := make([]string, 0, len(e.Deps))
		for _, d := range e.Deps {
			if !rm[d] {
				kept = append(kept, d)
			}
		}
		e.Deps = kept
		return nil
	})
}

// EpicDepList resolves an epic's dep neighborhood in both directions in one
// epics read: DependsOn are its own Deps (the boxes it waits on), Blocks are
// the epics that name it in THEIR Deps (the boxes waiting on it). Each edge is
// resolved to id+title+state. A zero-edge result is a clean object, never an
// error.
func (a *App) EpicDepList(ref string) (EpicDepListResult, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return EpicDepListResult{}, err
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		return EpicDepListResult{}, err
	}
	byID := make(map[string]*core.Epic, len(epics))
	for i := range epics {
		byID[epics[i].ID] = &epics[i]
	}
	target := byID[id]
	res := EpicDepListResult{ID: target.ID, Title: target.Title, DependsOn: []EpicRef{}, Blocks: []EpicRef{}}
	for _, d := range target.Deps {
		res.DependsOn = append(res.DependsOn, resolveEpicRef(byID, d))
	}
	var blockIDs []string
	for i := range epics {
		if epics[i].ID != id && contains(epics[i].Deps, id) {
			blockIDs = append(blockIDs, epics[i].ID)
		}
	}
	sort.Strings(blockIDs)
	for _, b := range blockIDs {
		res.Blocks = append(res.Blocks, resolveEpicRef(byID, b))
	}
	return res, nil
}

// resolveEpicRef looks up id in the epic set, returning its id+title+state. A
// dangling id yields the id alone, so the edge is still reported (faithful to
// the shard) and lint's epic-dep-missing is the place that flags it.
func resolveEpicRef(byID map[string]*core.Epic, id string) EpicRef {
	e, ok := byID[id]
	if !ok {
		return EpicRef{ID: id}
	}
	state := "open"
	if !e.IsOpen() {
		state = "closed"
	}
	return EpicRef{ID: e.ID, Title: e.Title, State: state}
}

// openEpicDeps returns the deps of e that exist and are still OPEN — the
// unsatisfied edges, sorted. What `epic activate` warns with, `epic ls`
// surfaces as waits, and epic_dep_done requires to be empty. A dangling dep is
// deliberately NOT listed (it is neither open nor closed — lint's
// epic-dep-missing owns it).
func openEpicDeps(e *core.Epic, epics []core.Epic) []string {
	var out []string
	for _, d := range e.Deps {
		for i := range epics {
			if epics[i].ID == d && epics[i].IsOpen() {
				out = append(out, d)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
