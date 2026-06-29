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
// It is purely read-only. A task surfaces when it has at least one signal (unset
// value/effort, stale, or a done dependency). Terminal-lane tasks (done/icebox/
// waiting) are skipped — there is nothing to re-evaluate about parked or finished
// work. A non-empty label restricts the result the same way as List/Next.
// staleDays of 0 disables the stale signal (the rest still fire).
func (a *App) Revisit(label string, staleDays, limit int) ([]RevisitItem, error) {
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
		if label != "" && !contains(t.Labels, label) {
			continue
		}
		reasons := core.RevisitReasons(*t, now, staleDays, doneIDs)
		if len(reasons) == 0 {
			continue
		}
		out = append(out, RevisitItem{Task: *t, Reasons: reasons})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
