package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
)

// The hierarchy edge, both ways: Reparent writes it, ParentList reads it.
//
// `parent` used to be write-once — settable at `add --parent`, and after that only
// by hand-editing a shard, which is precisely what CLAUDE.md forbids (the shards are
// machine-written; a hand-edit fights the next marshaller pass). Every other field
// on a task has a command; this one did not, so the one honest way to fix a
// mis-filed task was the one way you are told never to use.

// Reparent sets a task's parent, or clears it when parent is "" (the task becomes
// top-level). It is the `repo`/`label` of the hierarchy edge.
//
// Three refusals, all validation (exit 2 — the fix is a different argument, not a
// re-run):
//
//   - a parent that does not exist (the same check `add --parent` makes: a dangling
//     parent is lint's parent-missing ERROR, so never create one);
//   - a task as its own parent;
//   - an edge that would close a CYCLE — i.e. the proposed parent is already a
//     descendant of this task. A hierarchy cycle has no root, so every task in it
//     belongs to no tree and shows up under nothing; refusing at the edit is the
//     same discipline AddDeps applies to deps (lint's parent-cycle is the backstop
//     for the half-edges a git merge can slip in behind us).
//
// A parent already in the done lane is deliberately ALLOWED: re-filing a leftover
// under the epic it actually came from is a legitimate — and common — thing to want
// after the epic closed. Refusing it would make the correct record unrepresentable.
// lint's parent-done warn is where that state surfaces instead.
func (a *App) Reparent(id, parent string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	if _, i := idx.Find(id); i < 0 {
		return nil, core.NotFound(id)
	}
	if parent != "" {
		switch {
		case parent == id:
			return nil, core.Validationf(id, "a task cannot be its own parent")
		case !idx.Has(parent):
			return nil, core.Validationf(id, "parent %q does not exist", parent)
		case idx.HasAncestor(parent, id):
			return nil, core.Validationf(id,
				"setting parent %q would create a cycle (%s is already a descendant of %s)", parent, parent, id)
		}
	}
	return a.mutate(id, func(t *core.Task) { t.Parent = parent })
}

// ParentListResult is one task's hierarchy neighborhood, both directions at once:
// the parent it hangs under (nil when it is top-level) and the children hanging
// under it. The reverse edge is the point — "what is under this epic?" was
// otherwise a full-board dump and a grep.
type ParentListResult struct {
	ID       string
	Title    string
	Parent   *TaskRef
	Children []TaskRef
}

// ParentList resolves both directions of the hierarchy edge for id, each to
// id+title+lane (the same shape DepList returns). A dangling parent — an id naming
// no task, which lint reports as parent-missing — resolves to the bare id with an
// empty title/lane rather than vanishing, so the broken edge stays visible.
func (a *App) ParentList(id string) (ParentListResult, error) {
	idx, err := a.load()
	if err != nil {
		return ParentListResult{}, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return ParentListResult{}, core.NotFound(id)
	}
	res := ParentListResult{ID: t.ID, Title: t.Title, Children: []TaskRef{}}
	if t.Parent != "" {
		ref := resolveTaskRef(idx, t.Parent)
		res.Parent = &ref
	}
	for _, c := range idx.Children(id) {
		res.Children = append(res.Children, TaskRef{ID: c.ID, Title: c.Title, Status: c.Status})
	}
	return res, nil
}
