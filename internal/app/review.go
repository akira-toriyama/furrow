package app

import "github.com/akira-toriyama/furrow/internal/core"

// ReviewTask stamps a task's `reviewed` timestamp — the record that a human
// looked at and re-assessed this task. Unlike every other single-task edit it
// does NOT go through mutate (which stamps `updated` on any real edit): a
// review changes no content, so bumping `updated` would wrongly disturb
// staleness and `--sort updated`. The shard is rewritten only because
// `reviewed` changed (zero churn otherwise).
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

// ReviewEpic stamps a box's `reviewed` timestamp — Task.Reviewed's box-level
// twin, and the reset for revisit's epic_review_due cadence on a standing box.
// The ref resolves under the epic-ref contract (exact id, unique id prefix,
// unique title substring). Like ReviewTask it deliberately does NOT touch
// `updated`: a review changes no content. Returns before and after for the
// epic mutation envelope.
func (a *App) ReviewEpic(ref string) (*core.Epic, *core.Epic, error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, nil, err
	}
	e, ok, err := a.Store.LoadEpic(id)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		// Resolution just answered with this id, so only a concurrent delete
		// lands here; mutateEpic reports the same race as core.NotFound, and
		// two exit codes for one condition would be a trap.
		return nil, nil, core.NotFound(id)
	}
	before := *e
	now := a.Clock.Now()
	e.Reviewed = &now
	if err := a.Store.SaveEpic(e); err != nil {
		return nil, nil, err
	}
	return &before, e, nil
}

// ReviewRepo records a per-repo review: it resolves repo against the board's
// universe, loads (or creates) its review shard, and stamps a timestamp. A human
// review (byAgent == false) advances LastReviewed — the clock the sync staleness
// nudge reads. An agent sweep (byAgent == true) advances LastAgentReviewed only,
// so an autonomous re-evaluation is logged WITHOUT resetting the human nudge
// clock (the actor separation the review design turns on). Returns the saved
// record.
func (a *App) ReviewRepo(repo string, byAgent bool) (*core.RepoRecord, error) {
	canonical, err := a.resolveReviewRepo(repo)
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

// resolveReviewRepo resolves a review's repo argument against a universe that
// ALSO includes the repos already carrying a review shard — not just the ones
// named by a task or derived from the checkout (ResolveRepo's universe). Without
// this, a repo reviewed but not yet attached to any task is invisible to
// resolution: its short name would not resolve, and a differently-cased full
// owner/repo would pass through verbatim and fork a SECOND shard for the same
// repo. Including the existing records keeps one repo == one shard.
func (a *App) resolveReviewRepo(repo string) (string, error) {
	idx, err := a.load()
	if err != nil {
		return "", err
	}
	universe := repoUniverse(idx, a.BoardRepos)
	recs, err := a.Store.ListRepos()
	if err != nil {
		return "", err
	}
	for _, r := range recs {
		universe = append(universe, r.Repo)
	}
	return resolveRepoIn(repo, "", universe)
}
