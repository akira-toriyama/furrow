package app

import "github.com/akira-toriyama/furrow/internal/core"

// ReviewTask stamps a task's `reviewed` timestamp — the record that a human
// looked at and re-assessed this task. Unlike every other single-task edit it
// does NOT go through mutate (which stamps `updated`): a review changes no
// content, so bumping `updated` would wrongly disturb staleness and
// `--sort updated`. It loads, finds, sets `reviewed = now`, and saves; the
// shard is rewritten only because `reviewed` changed (zero churn otherwise).
func (a *App) ReviewTask(id string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	now := a.Clock.Now()
	t.Reviewed = &now
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// ReviewRepo records a per-repo review: it resolves repo against the board's
// universe, loads (or creates) its review shard, and stamps a timestamp. A human
// review (byAgent == false) advances LastReviewed — the clock the sync staleness
// nudge reads. An agent sweep (byAgent == true) advances LastAgentReviewed only,
// so an autonomous re-evaluation is logged WITHOUT resetting the human nudge
// clock (the actor separation the review design turns on). Returns the saved
// record.
func (a *App) ReviewRepo(repo string, byAgent bool) (*core.RepoRecord, error) {
	canonical, err := a.ResolveRepo(repo)
	if err != nil {
		return nil, err
	}
	rec, ok, err := a.Store.LoadRepo(canonical)
	if err != nil {
		return nil, err
	}
	if !ok {
		rec = &core.RepoRecord{Repo: canonical}
	}
	now := a.Clock.Now()
	if byAgent {
		rec.LastAgentReviewed = &now
	} else {
		rec.LastReviewed = &now
	}
	if err := a.Store.SaveRepo(rec); err != nil {
		return nil, err
	}
	return rec, nil
}
