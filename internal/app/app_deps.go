// Checklist and dependency edits — the list-shaped fields whose validation
// must read the same snapshot the edit lands in.

package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Check sets a checklist item's done state by zero-based index. An out-of-range
// index is a validation error (not a silent no-op), so the CLI exit code and
// the {"error":...} envelope honor the contract.
func (a *App) Check(id string, item int, done bool) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	if item < 0 || item >= len(t.Checklist) {
		return nil, core.Validationf(id, "checklist index %d out of range (have %d item(s))", item, len(t.Checklist))
	}
	return a.mutateIn(idx, id, func(t *core.Task) { t.Checklist[item].Done = done })
}

// RemoveCheck deletes the checklist item at the zero-based index. An
// out-of-range index is a validation error (never a silent no-op), mirroring
// Check — so an agent's exit code and envelope honor the contract.
func (a *App) RemoveCheck(id string, item int) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	if item < 0 || item >= len(t.Checklist) {
		return nil, core.Validationf(id, "checklist index %d out of range (have %d item(s))", item, len(t.Checklist))
	}
	return a.mutateIn(idx, id, func(t *core.Task) {
		t.Checklist = append(t.Checklist[:item], t.Checklist[item+1:]...)
	})
}

// RewordCheck replaces the text of the checklist item at the zero-based index,
// preserving its done state. Out-of-range index and empty text are validation
// errors.
func (a *App) RewordCheck(id string, item int, text string) (*core.Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, core.Validationf(id, "checklist item text must not be empty")
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	if item < 0 || item >= len(t.Checklist) {
		return nil, core.Validationf(id, "checklist index %d out of range (have %d item(s))", item, len(t.Checklist))
	}
	return a.mutateIn(idx, id, func(t *core.Task) { t.Checklist[item].Text = text })
}

// AddDeps adds several dependencies to `id` in one write (`dep a b c`). Every
// dep is validated against the same contract AddDep enforced — must exist, must
// not be `id` itself, must not create a cycle (checked against the graph as it
// grows, so an in-batch edge counts) — and a dep already present is a no-op.
// Validation is all-or-nothing: the first bad dep returns before any save, so a
// partial batch never lands. The marshaller keeps the dep list sorted+deduped.
func (a *App) AddDeps(id string, deps []string) (*core.Task, error) {
	if err := requireNonBlank(id, "dep", deps); err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	// Validated and applied against the ONE snapshot (mutateIn's reason to
	// exist): the cycle check reads the same index the edit lands in.
	return a.mutateInErr(idx, id, func(t *core.Task) error {
		for _, dep := range deps {
			if id == dep {
				return core.Validationf(id, "a task cannot depend on itself")
			}
			if !idx.Has(dep) {
				return core.Validationf(id, "dependency %q does not exist", dep)
			}
			if idx.DependsOn(dep, id) {
				return core.Validationf(id, "adding dep %q would create a cycle (%s already depends on %s)", dep, dep, id)
			}
			if !contains(t.Deps, dep) {
				t.Deps = append(t.Deps, dep)
			}
		}
		return nil
	})
}

// RemoveDeps drops several dependencies from `id` in one write. Each must be a
// current dependency (else a validation error naming it — never a silent no-op),
// and the whole batch is validated before any change, so a bad id aborts without
// a partial removal.
func (a *App) RemoveDeps(id string, deps []string) (*core.Task, error) {
	if err := requireNonBlank(id, "dep", deps); err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.mutateInErr(idx, id, func(t *core.Task) error {
		rm := make(map[string]bool, len(deps))
		for _, dep := range deps {
			if !contains(t.Deps, dep) {
				return core.Validationf(id, "%q is not a dependency of %s", dep, id)
			}
			rm[dep] = true
		}
		kept := make([]string, 0, len(t.Deps))
		for _, d := range t.Deps {
			if !rm[d] {
				kept = append(kept, d)
			}
		}
		t.Deps = kept
		return nil
	})
}

// AddChecks appends several checklist items in one write, so `check --add A
// --add B` records both (a repeated flag kept only the last before). Items are
// appended verbatim, preserving order and any commas in the text.
func (a *App) AddChecks(id string, items []string) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) {
		for _, text := range items {
			t.Checklist = append(t.Checklist, core.ChecklistItem{Text: text})
		}
	})
}
