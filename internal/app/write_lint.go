package app

import (
	"sort"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// saveIndex is the one write of the hot store's task index. The board as it
// stood before this process's first write is the copy load() kept (preWrite),
// so NewLintErrors can tell the errors a write created from the ones the board
// already had; every call remembers the index it wrote as the board after.
// Every index write in this package goes through here except two that have no
// "before" worth comparing: Init stamping an empty store, and upgrade's
// layout rewrite. A writer that never went through load() (none today) gets
// its snapshot here, best effort.
func (a *App) saveIndex(idx *core.Index) error {
	if a.preWrite == nil {
		if before, err := a.Store.Load(); err == nil {
			a.preWrite = before
		}
	}
	if err := a.Store.Save(idx); err != nil {
		return err
	}
	a.postWrite = idx
	return nil
}

// saveEpic is saveIndex's box twin: the one write of a box shard this package
// makes (upgrade's layout rewrite aside, which has no "before"). The first
// call snapshots the boxes as they still stand on disk; every call folds the
// box it wrote into the "after" set in memory — never a re-read, so a
// co-located writer landing in between is not blamed on this command. Without
// this, a write that changed only a box was not compared at all: the first
// `epic add` on a board put every unfiled task into epic-required, and the
// note README promises for "every rule" stayed silent (t-dhzm).
func (a *App) saveEpic(e *core.Epic) error {
	if !a.epicsWritten {
		// Non-nil even for a board with no box yet: nil is "no snapshot", and
		// the board's FIRST box is exactly the write that must be compared.
		if before, err := a.Store.LoadEpics(); err == nil {
			a.preEpics = append([]core.Epic{}, before...)
			a.postEpics = append([]core.Epic{}, before...)
		}
	}
	if err := a.Store.SaveEpic(e); err != nil {
		return err
	}
	a.epicsWritten = true
	replaced := false
	for i := range a.postEpics {
		if a.postEpics[i].ID == e.ID {
			a.postEpics[i] = *e
			replaced = true
			break
		}
	}
	if !replaced {
		a.postEpics = append(a.postEpics, *e)
	}
	return nil
}

// NewLintErrors returns the lint ERRORS this App's writes created: lintIndex,
// leveled by the board's policy exactly as Lint levels it, run over the board
// as it stood before the first write and as this App last wrote it — tasks
// AND boxes, each side taken from the write funnels (saveIndex, saveEpic) and
// read from disk once only for the half this process never wrote. An error
// is new when it is about a task (or board-level id) the earlier board had no
// error of that code about (errorMembers), so touching a task that was
// already in error says nothing, while a write that puts a DIFFERENT task in
// error (reopening a dep its dependent sits ready on, or declaring the
// board's first box) is caught. "After" is what this process saved, not a
// re-read, so a co-located writer landing in between is never blamed on this
// command. nil when nothing was written.
//
// It reports, it never refuses: the write already happened, the state can be a
// deliberate step, and lint plus the board's pre-push check are the gate. What
// this buys is that the writer hears it in the same command instead of at push
// time (t-7vgb).
func (a *App) NewLintErrors() ([]core.Problem, error) {
	wroteIndex := a.preWrite != nil && a.postWrite != nil
	wroteEpics := a.epicsWritten && a.preEpics != nil
	if !wroteIndex && !wroteEpics {
		return nil, nil
	}
	beforeIdx, afterIdx := a.preWrite, a.postWrite
	if !wroteIndex {
		idx, err := a.Store.Load()
		if err != nil {
			return nil, err
		}
		beforeIdx, afterIdx = idx, idx
	}
	beforeEpics, afterEpics := a.preEpics, a.postEpics
	if !wroteEpics {
		epics, err := a.Store.LoadEpics()
		if err != nil {
			return nil, err
		}
		beforeEpics, afterEpics = epics, epics
	}
	had := map[[2]string]bool{}
	before := a.indexErrors(beforeIdx, beforeEpics)
	members := errorMembers(beforeIdx)
	for _, p := range before {
		for _, m := range members(p) {
			had[[2]string{p.Code, m}] = true
		}
	}
	var out []core.Problem
	after := a.indexErrors(afterIdx, afterEpics)
	members = errorMembers(afterIdx)
	for _, p := range after {
		for _, m := range members(p) {
			if !had[[2]string{p.Code, m}] {
				out = append(out, p)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}

// errorMembers says who a finding is about. Most findings are about their ID;
// a region finding — one per dep cycle or priority tie, filed under its
// smallest id — is about every task in the region, so a write that only
// shrinks or splits a region (moving its smallest id) is not a new error,
// while a task newly drawn into one is.
func errorMembers(idx *core.Index) func(core.Problem) []string {
	cycles := core.CycleRegions(idx)
	ties := map[string][]string{}
	for _, tie := range priorityTies(idx) {
		ties[tie.ids[0]] = tie.ids
	}
	return func(p core.Problem) []string {
		switch p.Code {
		case "dep-cycle":
			if m, ok := cycles[p.ID]; ok {
				return m
			}
		case "priority-duplicate":
			if m, ok := ties[p.ID]; ok {
				return m
			}
		}
		return []string{p.ID}
	}
}

// indexErrors is lintIndex under the board's lint policy, errors only.
func (a *App) indexErrors(idx *core.Index, epics []core.Epic) []core.Problem {
	var out []core.Problem
	for _, p := range a.applyLintPolicy(a.lintIndex(idx, epics)) {
		if p.Severity == core.SevError {
			out = append(out, p)
		}
	}
	return out
}
