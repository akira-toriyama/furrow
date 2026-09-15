package app

import "github.com/akira-toriyama/furrow/internal/core"

// The single-target conveniences the tests read naturally — one task in, one
// task out — over the batch entry points production actually has. They live
// in the test binary only: production callers went through the batch/series
// forms, and the exported twins were dead weight the linter could not see
// (t-3gz4).

func first(ts []*core.Task) *core.Task {
	if len(ts) == 0 {
		return nil
	}
	return ts[0]
}

func (a *App) Move(id, lane string) (*core.Task, error) {
	ts, _, err := a.MoveMany([]string{id}, lane, nil)
	return first(ts), err
}

func (a *App) Done(id string) (*core.Task, error) {
	ts, _, err := a.DoneMany([]string{id}, nil)
	return first(ts), err
}

func (a *App) DoneNote(id, note string) (*core.Task, error) {
	ts, _, err := a.DoneMany([]string{id}, &note)
	return first(ts), err
}

func (a *App) DoneManyNote(ids []string, note string) ([]*core.Task, error) {
	ts, _, err := a.DoneMany(ids, &note)
	return ts, err
}

func (a *App) AddDep(id, dep string) (*core.Task, error)    { return a.AddDeps(id, []string{dep}) }
func (a *App) RemoveDep(id, dep string) (*core.Task, error) { return a.RemoveDeps(id, []string{dep}) }
func (a *App) AddCheck(id, text string) (*core.Task, error) { return a.AddChecks(id, []string{text}) }

// Backlinks is BacklinksBatch for one id; an id the batch does not know is
// NotFound, the single-read contract the batch answers with omission.
func (a *App) Backlinks(id string) ([]core.Task, error) {
	m, err := a.BacklinksBatch([]string{id})
	if err != nil {
		return nil, err
	}
	links, ok := m[id]
	if !ok {
		return nil, core.NotFound(id)
	}
	return links, nil
}
