package app

import "strings"

// BriefData is the one-shot session-orient read (`furrow brief`): what to pick
// up (next, WITH bodies — the show follow-up folded in), what is in flight but
// stuck (blocked: next-lane tasks with an unsatisfied dep, which plain `next`
// deliberately hides), what deserves a fresh look (the revisit summary), and
// how many loose balls sit repo-less (drafts). It is a pure COMPOSITION of the
// existing reads — Next, GetBatch, ListItems, RevisitSummary, List — so its
// answers can never diverge from the commands it summarizes.
type BriefData struct {
	Next      []ShowItem
	NextTotal int // actionable count BEFORE the display cap — a cap must never hide the size of the queue
	Blocked   []ListItem
	Revisit   RevisitSummary
	Drafts    int // repo-less tasks, board-wide by definition (a draft has no repo, so no scope can own it)
}

// Brief assembles BriefData under the query's scope (repo/label). nextLimit
// caps how many next picks carry a body (0 = uncapped); NextTotal always
// reports the uncapped count. staleDays feeds the revisit summary (0 disables
// the stale signal, Revisit's contract).
func (a *App) Brief(o QueryOpts, nextLimit, staleDays int) (*BriefData, error) {
	no := o
	no.Limit = 0
	nextTasks, err := a.Next(no)
	if err != nil {
		return nil, err
	}
	total := len(nextTasks)
	if nextLimit > 0 && len(nextTasks) > nextLimit {
		nextTasks = nextTasks[:nextLimit]
	}
	ids := make([]string, 0, len(nextTasks))
	for _, t := range nextTasks {
		ids = append(ids, t.ID)
	}
	picks, _, err := a.GetBatch(ids, true)
	if err != nil {
		return nil, err
	}

	bo := o
	bo.Limit = 0
	bo.Status = strings.Join(a.Cfg.NextLanes, ",")
	bo.Blocked = true
	blocked, err := a.ListItems(bo)
	if err != nil {
		return nil, err
	}

	ro := o
	ro.Limit = 0
	sum, err := a.RevisitSummary(ro, staleDays)
	if err != nil {
		return nil, err
	}

	drafts, err := a.List(QueryOpts{Drafts: true})
	if err != nil {
		return nil, err
	}

	return &BriefData{
		Next:      picks,
		NextTotal: total,
		Blocked:   blocked,
		Revisit:   sum,
		Drafts:    len(drafts),
	}, nil
}
