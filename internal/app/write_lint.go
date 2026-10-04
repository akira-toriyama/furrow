package app

import (
	"sort"

	"github.com/akira-toriyama/furrow/internal/core"
)

// saveIndex is the one write of the hot store's task index. The first call in
// this App's life loads the board as it still stands on disk, so
// NewLintErrors can tell the errors a write created from the ones the board
// already had, and every call remembers the index it wrote as the board after.
// Every index write in this package goes through here except two that have no
// "before" worth comparing: Init stamping an empty store, and upgrade's
// layout rewrite. The diagnostic load is best effort: if it fails the write
// still lands, and only the note is lost.
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

// NewLintErrors returns the lint ERRORS this App's writes created: lintIndex,
// leveled by the board's policy exactly as Lint levels it, run over the board
// as it stood before the first write and as this App last wrote it. An error
// is new when it is about a task (or board-level id) the earlier board had no
// error of that code about (errorMembers), so touching a task that was
// already in error says nothing, while a write that puts a DIFFERENT task in
// error (reopening a dep its dependent sits ready on) is caught. "After" is the index this process saved, not a re-read, so a
// co-located writer landing in between is never blamed on this command. Both
// sides judge against the boxes as they are now: a write that changes only a
// box is not compared. nil when nothing was written.
//
// It reports, it never refuses: the write already happened, the state can be a
// deliberate step, and lint plus the board's pre-push check are the gate. What
// this buys is that the writer hears it in the same command instead of at push
// time (t-7vgb).
func (a *App) NewLintErrors() ([]core.Problem, error) {
	if a.preWrite == nil || a.postWrite == nil {
		return nil, nil
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	had := map[[2]string]bool{}
	before := a.indexErrors(a.preWrite, epics)
	members := errorMembers(a.preWrite)
	for _, p := range before {
		for _, m := range members(p) {
			had[[2]string{p.Code, m}] = true
		}
	}
	var out []core.Problem
	after := a.indexErrors(a.postWrite, epics)
	members = errorMembers(a.postWrite)
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
