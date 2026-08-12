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
	// Active is the open+active epic(s) for the scope — the focus Next scopes to,
	// with the member roll-up. EpicsDeclared distinguishes the two empty cases a
	// reader must tell apart: false = the board never declared a box (the scope
	// discipline is off, Next is board-wide), true+empty = participating board
	// with nothing active, so Next is DELIBERATELY empty until someone runs
	// `furrow epic activate`.
	Active        []EpicItem
	EpicsDeclared bool
	// Pinned is the open pinned epic(s) NOT already listed in Active — the
	// always-visible channels whose tasks lead Next regardless of the active
	// scope (v7). Board-wide like NextScope's pinned band (pinning is not
	// repo-slotted; the tasks repo-filter themselves), and deduped against
	// Active so a box both active and pinned appears once, as the focus.
	// Only channels with an OPEN member appear: an empty standing box is its
	// healthy state (a mandate inbox sits empty most sessions), so a fleet of
	// them would put a band of inert header lines on every brief. The rest
	// collapse to PinnedQuiet — hidden, never silently.
	Pinned []EpicItem
	// PinnedQuiet counts the open pinned boxes Pinned omitted because every
	// member is closed (or none exist) — the next_total pattern: a filter must
	// keep the size of what it hid readable.
	PinnedQuiet int
	// Due is what has come DUE — overdue first, then today. It LEADS the read
	// because a date is the one thing on this board that expires: everything else
	// brief reports waits for you, a date passes without you. Deliberately not
	// epic-scoped (promised work is usually parked outside the active focus),
	// though it obeys the repo scope like every other section. `lint`'s
	// due-overdue/due-today are its twin — this is the surface a session cannot
	// miss, that one is the check a sweep goes looking for.
	Due       DueSummary
	Next      []ShowItem
	NextTotal int // actionable count BEFORE the display cap — a cap must never hide the size of the queue
	Blocked   []ListItem
	Revisit   RevisitSummary
	Drafts    int // repo-less tasks, board-wide by definition (a draft has no repo, so no scope can own it)
	// Lint is the error-count ride-along sync also prints (LintErrorCounts —
	// errors only, by code). Best-effort: a lint failure zeroes it rather than
	// failing the orientation read.
	Lint LintErrorSummary
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

	// The due section under this read's scope, minus any lane filter: a promised
	// task is typically parked in `waiting` or still in an intake lane, so
	// inheriting a caller's -s would hide precisely the rows this section exists
	// to show. The board's repo scope is dropped by App.Due itself (no AUTOMATIC
	// narrowing may hide a date — a promise does not stop expiring because this
	// session happens to sit in another repo), and the epic scope was never
	// applied here to begin with. An explicit -r/-l survives all three.
	dueOpts := o
	dueOpts.Status = ""
	due, err := a.Due(dueOpts)
	if err != nil {
		return nil, err
	}

	// The epic header: EpicsDeclared is board-wide (participation is a board
	// property), Active is scoped like Next's own scope (o.Repo, else the
	// board's ScopeRepo) so the line names the focus THIS session's next obeys.
	all, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	repoScope := o.Repo
	if repoScope == "" {
		repoScope = o.ScopeRepo
	}
	items, err := a.EpicList(EpicQueryOpts{Repo: repoScope})
	if err != nil {
		return nil, err
	}
	var active []EpicItem
	for _, it := range items {
		if it.Epic.Active {
			active = append(active, it)
		}
	}
	// The pinned channels, board-wide (EpicList with no repo filter): a pinned
	// box outside this repo can still lead Next when its tasks match the scope.
	allItems, err := a.EpicList(EpicQueryOpts{})
	if err != nil {
		return nil, err
	}
	var pinned []EpicItem
	quiet := 0
	for _, it := range allItems {
		if !it.Epic.Pinned || it.Epic.Active {
			continue
		}
		if it.Progress.Total-it.Progress.Done == 0 {
			quiet++
			continue
		}
		pinned = append(pinned, it)
	}

	// Best-effort, exactly like sync's ride-along: orientation must not fail
	// because a lint sweep hit an IO error.
	lint, err := a.LintErrorCounts()
	if err != nil {
		lint = LintErrorSummary{}
	}

	return &BriefData{
		Active:        active,
		EpicsDeclared: len(all) > 0,
		Pinned:        pinned,
		PinnedQuiet:   quiet,
		Due:           due,
		Next:          picks,
		NextTotal:     total,
		Blocked:       blocked,
		Revisit:       sum,
		Drafts:        len(drafts),
		Lint:          lint,
	}, nil
}
