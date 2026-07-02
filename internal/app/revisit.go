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
