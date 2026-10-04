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
// is new when the earlier board had no error with the same (code, id), so
// touching a task that was already in error says nothing, while a write that
// puts a DIFFERENT task in error (reopening a dep its dependent sits ready on)
// is caught. "After" is the index this process saved, not a re-read, so a
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
	for _, p := range a.indexErrors(a.preWrite, epics) {
		had[[2]string{p.Code, p.ID}] = true
	}
	var out []core.Problem
	for _, p := range a.indexErrors(a.postWrite, epics) {
		if !had[[2]string{p.Code, p.ID}] {
			out = append(out, p)
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
