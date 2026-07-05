package app

import "github.com/akira-toriyama/furrow/internal/core"

// RevisitItem pairs a task with the re-evaluation signals it triggered. It is the
// read-only counterpart to an actionable `next` row: the cli layer renders it
// (attaching the reasons in --json), the agent acts on it via the setters.
type RevisitItem struct {
	Task    core.Task
	Reasons []core.RevisitReason
}

// Revisit lists open tasks that may need a fresh judgment, in canonical order.
// It is purely read-only. A task surfaces when it has at least one signal (no
// repo, unset value/effort, stale, or a done dependency). Terminal-lane tasks
// (done/icebox/waiting) are skipped — there is nothing to re-evaluate about
// parked or finished work. The query's filters restrict the result like
// List/Next, with one carve-out: a draft (repos == []) bypasses the board
// scope and any repo filter, so open drafts surface (as no_repo) regardless of
// scope. staleDays of 0 disables the stale signal (the rest still fire).
func (a *App) Revisit(o QueryOpts, staleDays int) ([]RevisitItem, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
		if t.Status == a.Cfg.DoneLane {
			doneIDs[t.ID] = true
		}
	}
	now := a.Clock.Now()
	var out []RevisitItem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if a.Cfg.IsTerminal(t.Status) {
			continue
		}
		if !o.matchRevisit(t) {
			continue
		}
		reasons := core.RevisitReasons(*t, now, staleDays, doneIDs)
		if len(reasons) == 0 {
			continue
		}
		out = append(out, RevisitItem{Task: *t, Reasons: reasons})
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// RevisitSummary is the two "silent staleness" counts the session loop must not
// miss, within a scope: task ids whose dependency is already done (reconcile-on-
// close) and task ids past the stale threshold. Ids are in canonical order.
type RevisitSummary struct {
	DepDone []string // task ids with >=1 dependency in the done lane
	Stale   []string // task ids not updated within staleDays
}

// Empty reports whether nothing is worth surfacing (a clean board).
func (s RevisitSummary) Empty() bool { return len(s.DepDone) == 0 && len(s.Stale) == 0 }

// RevisitSummary tallies the dep_done and stale signals over the open
// (non-terminal) tasks passing o.match — strict scope, so repo-less drafts are
// excluded (that is the difference from Revisit, which surfaces drafts as
// no_repo). staleDays <= 0 disables the stale half (matching core.RevisitReasons).
// It is purely read-only; it drives the `furrow sync` staleness nudge.
func (a *App) RevisitSummary(o QueryOpts, staleDays int) (RevisitSummary, error) {
	idx, err := a.load()
	if err != nil {
		return RevisitSummary{}, err
	}
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
		if t.Status == a.Cfg.DoneLane {
			doneIDs[t.ID] = true
		}
	}
	now := a.Clock.Now()
	sum := RevisitSummary{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if a.Cfg.IsTerminal(t.Status) || !o.match(t) {
			continue
		}
		var depDone, stale bool
		for _, r := range core.RevisitReasons(*t, now, staleDays, doneIDs) {
			switch r.Code {
			case core.RevisitDepDone:
				depDone = true
			case core.RevisitStale:
				stale = true
			}
		}
		if depDone {
			sum.DepDone = append(sum.DepDone, t.ID)
		}
		if stale {
			sum.Stale = append(sum.Stale, t.ID)
		}
	}
	return sum, nil
}
