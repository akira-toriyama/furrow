package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// BriefData is the one-shot session-orient read (`furrow brief`): what to pick
// up (next, WITH bodies — the show follow-up folded in), what is in flight but
// stuck (blocked: next-lane tasks with an unsatisfied dep, which plain `next`
// deliberately hides), what deserves a fresh look (the revisit summary), and
// how many loose balls sit repo-less (drafts). It is a pure COMPOSITION of the
// existing reads, so its answers can never diverge from the commands it
// summarizes.
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
	// NextHidden is what the cap dropped, grouped per lane in [lanes].order
	// with the ids — the total alone once misled: the picks follow canonical
	// order, whose lane order puts in-progress after ready, so a board with -n
	// or more ready tasks ALWAYS drops its in-flight work off the band, and a
	// session that stopped at brief started a second task beside the one
	// already open (t-xzxm, furrow-test 2026-09-15). Naming the lane is what
	// tells "3 more like these" from "your own started work is below the
	// fold"; naming the ids is what tells whether the row the due band flagged
	// is one of them — a lane-and-count note sent four drills in one run back
	// to `next` to find out (t-fsvt, furrow-test 2026-09-25).
	NextHidden []HiddenLane
	Blocked    []ListItem
	// BlockedTotal is the same count across every OPEN lane in this scope. The
	// band above is deliberately next-lane only — a dep blocking nothing in
	// flight is not this session's problem — but a lane filter must not hide the
	// size of what it skipped (the NextTotal/PinnedQuiet rule). Terminal lanes
	// stay out: a closed or parked row's unmet dep is nobody's work to unblock.
	BlockedTotal int
	Revisit      RevisitSummary
	Drafts       int // repo-less tasks, board-wide by definition (a draft has no repo, so no scope can own it)
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
	var hidden []HiddenLane
	if nextLimit > 0 && len(nextTasks) > nextLimit {
		hidden = hiddenByLane(nextTasks[nextLimit:], a.Cfg.Lanes)
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

	// The same read with the lane filter widened to every open lane, so the
	// band can print what its own scope left out instead of a bare 0.
	to := o
	to.Limit = 0
	to.Status = strings.Join(openLanes(a.Cfg.Lanes, a.Cfg.IsTerminal), ",")
	to.Blocked = true
	blockedAll, err := a.ListItems(to)
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
		NextHidden:    hidden,
		Blocked:       blocked,
		BlockedTotal:  len(blockedAll),
		Revisit:       sum,
		Drafts:        len(drafts),
		Lint:          lint,
	}, nil
}

// openLanes is the configured lane order minus the terminal ones, in order.
func openLanes(lanes []string, isTerminal func(string) bool) []string {
	open := make([]string, 0, len(lanes))
	for _, l := range lanes {
		if !isTerminal(l) {
			open = append(open, l)
		}
	}
	return open
}

// HiddenLane is one lane's share of what brief's cap dropped: the lane and
// the ids that fell below the fold, in canonical order (the first id is the
// first row `next` would print past the fold). The count is len(IDs) — the
// stats window's rule that a count on this board is verifiable one id at a
// time, applied to the cap.
type HiddenLane struct {
	Lane string
	IDs  []string
}

// hiddenByLane groups the rows a cap dropped by lane, in [lanes].order then
// any unconfigured lane alphabetically (stats' laneHistogram), minus the
// lanes that lost nothing: a tally of what was hidden has no use for them.
func hiddenByLane(dropped []core.Task, lanes []string) []HiddenLane {
	byLane := map[string][]string{}
	for i := range dropped {
		byLane[dropped[i].Status] = append(byLane[dropped[i].Status], dropped[i].ID)
	}
	counts := make(map[string]int, len(byLane))
	for lane, ids := range byLane {
		counts[lane] = len(ids)
	}
	var out []HiddenLane
	for _, row := range laneHistogram(lanes, counts) {
		if row.Count > 0 {
			out = append(out, HiddenLane{Lane: row.Key, IDs: byLane[row.Key]})
		}
	}
	return out
}
