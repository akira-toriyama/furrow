package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// EpicRevisitItem pairs an epic with the signals it triggered. Epics are
// reported SEPARATELY from tasks rather than smuggled into RevisitItem: the two
// are different entities with different ids, and a single list whose rows were
// sometimes one and sometimes the other would force every consumer to type-switch
// before it could read a field.
type EpicRevisitItem struct {
	Epic    core.Epic
	Reasons []core.RevisitReason
}

// epicReasons computes the box-level signals for an OPEN epic.
//
// An epic with NO members yields no MEMBER signal (all_done/stuck): declaring
// the box before filling it is a legitimate first step, and nagging about it
// would teach the reader to ignore the signal. dep_done can still fire for it —
// the nudge is about opening order, and an empty box whose turn has come is
// exactly the one you open and then fill.
//
// The member signals name a state the reader can act on, and nothing else:
// all_done is every member in the done lane (ask for the close — whether the
// box is active or not), stuck is a box `next` reads handing out nothing (ask
// for a switch). A box that is neither says nothing: members parked outside
// the done lane are not "done", and a box waiting its turn is not blocked. The
// two were once read off "no open member" and "open members, none actionable"
// alone, so a parked box nagged in every session with no move to make.
//
// all_done and stuck are mutually exclusive by construction (all-done short
// circuits), but stale is orthogonal and can accompany either — an active box can
// be both blocked and forgotten, and that is worth saying twice. A STANDING box
// (v7) is exempt from all_done, dep_done AND stuck — the nags that assume a box
// is meant to finish, open, and drain: its healthy resting states are open 0 (a
// drained inbox) and open N with none actionable (deposits awaiting triage), and
// a board with a dozen standing channels would otherwise report a dozen
// permanent stucks that drown the real one. The review cadence below is the
// standing box's replacement signal. dep_done is
// dep_done's box twin and needs the EPIC set (the edges point at boxes, not
// members), which is why epics rides in: it fires only for a non-active box
// with >=1 dep, every one of them existing and closed — never on a broken
// graph (a dangling dep is lint's epic-dep-missing) and never for the active
// box (already open, nothing to nudge).
func (a *App) epicReasons(e *core.Epic, st epicStats, epics []core.Epic, now time.Time, staleDays int) []core.RevisitReason {
	if !e.IsOpen() {
		return nil
	}
	var out []core.RevisitReason

	switch {
	case st.Total == 0:
		// A freshly declared box. Nothing to say.
	case st.Done == st.Total:
		// A STANDING box (v7) is exempt: every deposit consumed is its healthy
		// resting state (a drained mandate inbox, a parking lot between
		// deposits), so "consider closing" would be a permanent false nag — the
		// exact failure mode a signal cannot recover from.
		if !e.Standing {
			out = append(out, core.RevisitReason{Code: core.RevisitEpicAllDone,
				Detail: fmt.Sprintf("all %d members done — consider closing", st.Total)})
		}
	case st.stuck(e):
		// epicStats.stuck carries every exemption (not read by next, standing,
		// waiting), so this signal and the `stuck` flag the reads print can
		// never disagree about a box.
		out = append(out, core.RevisitReason{Code: core.RevisitEpicStuck, Detail: st.stuckDetail()})
	}

	// A standing box also has no "turn to open" — it is always on — so the
	// dep_done nudge below skips it too.
	if !e.Active && !e.Standing && len(e.Deps) > 0 {
		satisfied := 0
		for _, d := range e.Deps {
			for i := range epics {
				if epics[i].ID == d && !epics[i].IsOpen() {
					satisfied++
					break
				}
			}
		}
		if satisfied == len(e.Deps) {
			out = append(out, core.RevisitReason{Code: core.RevisitEpicDepDone,
				Detail: fmt.Sprintf("all %d dep epic(s) closed — this box's turn to open", len(e.Deps))})
		}
	}

	// Staleness applies only to the ACTIVE box: a parked epic is SUPPOSED to sit
	// untouched, so flagging it would make the signal meaningless. The active one
	// going quiet is exactly the failure this whole feature exists to catch.
	if e.Active && staleDays > 0 && !e.Updated.IsZero() {
		if days := int(now.Sub(e.Updated).Hours() / 24); days >= staleDays {
			out = append(out, core.RevisitReason{Code: core.RevisitEpicStale,
				Detail: fmt.Sprintf("active but not updated for %d days", days)})
		}
	}

	// The STANDING box's review cadence (v9): the nag it trades the
	// finish-shaped ones for. [review].stale_after_days is the SAME clock the
	// per-repo review nudge reads (one board, one review rhythm; 0 disables
	// both), and like that nudge a never-reviewed box stays quiet — the first
	// `furrow review <epic-ref>` opts it in.
	if e.Standing && a.Cfg.ReviewStaleAfterDays > 0 && e.Reviewed != nil {
		if days := int(now.Sub(*e.Reviewed).Hours() / 24); days >= a.Cfg.ReviewStaleAfterDays {
			out = append(out, core.RevisitReason{Code: core.RevisitEpicReviewDue,
				Detail: fmt.Sprintf("standing box last reviewed %d days ago (threshold %dd) — re-check its contents", days, a.Cfg.ReviewStaleAfterDays)})
		}
	}
	return out
}

// stuckDetail words which of stuck's two shapes the box is in.
func (s epicStats) stuckDetail() string {
	if s.Open > 0 {
		return fmt.Sprintf("%d open members but none actionable", s.Open)
	}
	return fmt.Sprintf("no open members, and %d of %d parked rather than done", s.Total-s.Done, s.Total)
}

// RevisitEpics lists the boxes with at least one signal, in EpicList's order
// (active first) — the read behind `furrow revisit --boxes`, and the source of
// RevisitSummary's epic keys. Read-only.
func (a *App) RevisitEpics(o QueryOpts, staleDays int) ([]EpicRevisitItem, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.revisitEpicsIn(idx, o, staleDays)
}

// revisitEpicsIn is RevisitEpics over the caller's index snapshot — what
// RevisitSummary uses so `sync`'s nudge reads the board once.
func (a *App) revisitEpicsIn(idx *core.Index, o QueryOpts, staleDays int) ([]EpicRevisitItem, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	return a.revisitEpicsFrom(idx, epics, o, staleDays), nil
}

// revisitEpicsFrom is revisitEpicsIn over boxes the caller already holds.
func (a *App) revisitEpicsFrom(idx *core.Index, epics []core.Epic, o QueryOpts, staleDays int) []EpicRevisitItem {
	now := a.Clock.Now()
	stats := a.epicMemberStats(idx, a.doneSet(idx), now)
	var out []EpicRevisitItem
	for i := range epics {
		e := &epics[i]
		if o.Repo != "" && !anyRepoMatch(e.Repos, []string{o.Repo}) {
			continue
		}
		if o.ScopeRepo != "" && o.Repo == "" && !anyRepoMatch(e.Repos, []string{o.ScopeRepo}) {
			continue
		}
		if rs := a.epicReasons(e, stats[e.ID], epics, now, staleDays); len(rs) > 0 {
			out = append(out, EpicRevisitItem{Epic: *e, Reasons: rs})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := epicRank(out[i].Epic), epicRank(out[j].Epic)
		if ri != rj {
			return ri < rj
		}
		return out[i].Epic.ID < out[j].Epic.ID
	})
	return out
}

// RevisitItem pairs a task with the re-evaluation signals it triggered. It is the
// read-only counterpart to an actionable `next` row: the cli layer renders it
// (attaching the reasons in --json), the agent acts on it via the setters.
type RevisitItem struct {
	Task    core.Task
	Reasons []core.RevisitReason
}

// Revisit lists open tasks that may need a fresh judgment, in canonical order.
// It is purely read-only. A task surfaces when it has at least one signal —
// no_repo, value_unset, effort_unset, stale, or dep_done. The five EPIC signals
// (epic_all_done, epic_stuck, epic_stale, epic_dep_done, epic_review_due) are
// about a box rather than a task and are reported separately by RevisitEpics. Terminal-lane tasks
// (done/icebox/waiting) are skipped — there is nothing to re-evaluate about
// parked or finished work — except in a parked lane the board named in
// [revisit].parked_lanes, whose tasks keep the two signals that say a wait may
// be over (revisitReads, core.ParkedRevisitReasons). The query's filters restrict the result like
// List/Next, with one carve-out: a draft (repos == []) bypasses the board
// scope and any repo filter, so open drafts surface (as no_repo) regardless of
// scope. staleDays of 0 disables the stale signal (the rest still fire).
func (a *App) Revisit(o QueryOpts, staleDays int) ([]RevisitItem, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	doneIDs := a.doneSet(idx)
	now := a.Clock.Now()
	// Compile -q once. It receives THIS call's staleDays (which --stale-days
	// may have overridden), so `revisit -q is:stale` and revisit's own stale
	// signal use one threshold within one call.
	qpred, err := a.queryPred(o.Query, idx, queryRead{staleDays: staleDays})
	if err != nil {
		return nil, err
	}
	dueSkip := a.dueSkipLanes()
	var out []RevisitItem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		read, parked := a.revisitReads(t)
		if !read {
			continue
		}
		if !o.matchRevisit(t) {
			continue
		}
		reasons := revisitReasons(t, parked, now, staleDays, doneIDs, dueSkip)
		if len(reasons) == 0 {
			continue
		}
		if qpred != nil {
			ok, err := qpred(t)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, RevisitItem{Task: *t, Reasons: reasons})
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// revisitReads reports whether revisit reads t at all, and whether it reads it
// as PARKED. An open (non-terminal) task is read in full. A task in a terminal
// lane is out of sight unless the board named that lane in
// [revisit].parked_lanes — then it is read for the wait signals only. The ONE
// eligibility test: the list, the summary `sync` and `brief` print, and lint's
// reconcile-gap (revisitBlindLanes) all draw the line here.
func (a *App) revisitReads(t *core.Task) (read, parked bool) {
	if !a.Cfg.IsTerminal(t.Status) {
		return true, false
	}
	return a.Cfg.IsRevisitParked(t.Status), true
}

// revisitBlindLanes is the lanes revisitReads turns away, as the set
// core.StaleDepProblems skips: every terminal lane the board did not name in
// [revisit].parked_lanes.
func (a *App) revisitBlindLanes() map[string]bool {
	blind := make(map[string]bool, len(a.Cfg.Terminal))
	for l, on := range a.Cfg.Terminal {
		if on && !a.Cfg.IsRevisitParked(l) {
			blind[l] = true
		}
	}
	return blind
}

// revisitReasons is the signals for a task revisitReads let through: an open
// task's full set, or a parked task's two. dueSkip is dueSkipLanes — a due
// only counts as the parked task's own clock in a lane where its arrival
// raises something.
func revisitReasons(t *core.Task, parked bool, now time.Time, staleDays int, doneIDs, dueSkip map[string]bool) []core.RevisitReason {
	if parked {
		return core.ParkedRevisitReasons(*t, now, staleDays, doneIDs, !dueSkip[t.Status])
	}
	return core.RevisitReasons(*t, now, staleDays, doneIDs)
}

// UnreviewedRepo is one repo whose last human review is older than the
// [review].stale_after_days threshold — the per-repo half of the staleness
// nudge. Days is whole days since that review, so an agent can rank the backlog.
type UnreviewedRepo struct {
	Repo string `json:"repo"`
	Days int    `json:"days"`
}

// RevisitSummary is the "silent staleness" signals the session loop must not
// miss, within a scope: task ids whose dependency is already done (reconcile-on-
// close), task ids past the stale threshold, and repos whose human review has
// gone stale. Ids/repos are in canonical order.
type RevisitSummary struct {
	DepDone    []string         `json:"dep_done,omitempty"`   // task ids with >=1 dependency in the done lane
	Stale      []string         `json:"stale,omitempty"`      // task ids not updated within staleDays, a parked box's open members left out
	Unreviewed []UnreviewedRepo `json:"unreviewed,omitempty"` // repos past [review].stale_after_days (omitted when none, so the existing JSON shape is unchanged)
	// The epic keys carry EPIC ids, not task ids — the entities are distinct
	// and so are their id spaces. omitempty so a board with no boxes keeps a
	// minimal JSON shape.
	EpicAllDone   []string `json:"epic_all_done,omitempty"`   // open epic ids whose members are all in the done lane
	EpicStuck     []string `json:"epic_stuck,omitempty"`      // epic ids `next` reads that hold unfinished members and hand out none
	EpicStale     []string `json:"epic_stale,omitempty"`      // ACTIVE epic ids untouched for [revisit].stale_days
	EpicDepDone   []string `json:"epic_dep_done,omitempty"`   // parked epic ids whose deps are all closed — their turn to open
	EpicReviewDue []string `json:"epic_review_due,omitempty"` // standing epic ids whose last review is past [review].stale_after_days
}

// Empty reports whether nothing is worth surfacing (a clean board).
func (s RevisitSummary) Empty() bool {
	return len(s.DepDone) == 0 && len(s.Stale) == 0 && len(s.Unreviewed) == 0 &&
		len(s.EpicAllDone) == 0 && len(s.EpicStuck) == 0 && len(s.EpicStale) == 0 &&
		len(s.EpicDepDone) == 0 && len(s.EpicReviewDue) == 0
}

// RevisitSummary tallies the loop-visible signals over the tasks revisit reads
// (revisitReads: the open ones, plus those in a [revisit].parked_lanes lane)
// passing o.match — dep_done and stale — plus the five epic signals
// (epic_all_done, epic_stuck, epic_stale, epic_dep_done, epic_review_due),
// plus the repo-level unreviewed clock (unreviewedRepos, which is not a task
// signal at all). With a scope set (ScopeRepo/Repo),
// repo-less drafts are excluded — the difference from Revisit, which always
// surfaces drafts as no_repo; a board-wide o (no scope) counts them.
// staleDays <= 0 disables the stale half (matching core.RevisitReasons).
// The stale half skips a parked box's OPEN members (parkedBox), which Revisit
// still lists: the summary is a nudge, Revisit the read that answers when
// asked. It is purely read-only; it drives the `furrow sync` staleness nudge.
func (a *App) RevisitSummary(o QueryOpts, staleDays int) (RevisitSummary, error) {
	idx, err := a.load()
	if err != nil {
		return RevisitSummary{}, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return RevisitSummary{}, err
	}
	return a.revisitSummaryIn(idx, epics, o, staleDays)
}

// revisitSummaryIn is RevisitSummary over a snapshot the caller already holds.
func (a *App) revisitSummaryIn(idx *core.Index, epics []core.Epic, o QueryOpts, staleDays int) (RevisitSummary, error) {
	doneIDs := a.doneSet(idx)
	now := a.Clock.Now()
	byID := core.EpicsByID(epics)
	sum := RevisitSummary{}
	dueSkip := a.dueSkipLanes()
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		read, parked := a.revisitReads(t)
		if !read || !o.match(t) {
			continue
		}
		// A parked box's members are meant to sit untouched until the box
		// opens, the same reason epic_stale reads active boxes only. Revisit
		// and is:stale still list them: core.IsStale stays the one definition.
		// That is unstarted work waiting for the box's turn. A member in a
		// parked LANE revisit reads waits on something outside the box, and the
		// box's turn says nothing about it — it is counted whatever its box is
		// doing, or the last waiting member of a box nobody reopens is exactly
		// the task this read would lose.
		box := byID[t.Epic]
		boxWaitsItsTurn := !parked && box != nil && parkedBox(box)
		var depDone, stale bool
		for _, r := range revisitReasons(t, parked, now, staleDays, doneIDs, dueSkip) {
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
		if stale && !boxWaitsItsTurn {
			sum.Stale = append(sum.Stale, t.ID)
		}
	}
	// The epic signals ride the same pass so `sync` reports boxes and tasks from
	// one read. RevisitEpics owns the scope rules for boxes (a box's repos, not a
	// task's), so it is called rather than re-implemented here.
	eps := a.revisitEpicsFrom(idx, epics, o, staleDays)
	for _, it := range eps {
		for _, r := range it.Reasons {
			switch r.Code {
			case core.RevisitEpicAllDone:
				sum.EpicAllDone = append(sum.EpicAllDone, it.Epic.ID)
			case core.RevisitEpicStuck:
				sum.EpicStuck = append(sum.EpicStuck, it.Epic.ID)
			case core.RevisitEpicStale:
				sum.EpicStale = append(sum.EpicStale, it.Epic.ID)
			case core.RevisitEpicDepDone:
				sum.EpicDepDone = append(sum.EpicDepDone, it.Epic.ID)
			case core.RevisitEpicReviewDue:
				sum.EpicReviewDue = append(sum.EpicReviewDue, it.Epic.ID)
			}
		}
	}
	unrev, err := a.unreviewedRepos(o, now)
	if err != nil {
		return RevisitSummary{}, err
	}
	sum.Unreviewed = unrev
	return sum, nil
}

// unreviewedRepos returns the in-scope repos whose last HUMAN review is older
// than [review].stale_after_days (0 disables → nil). A repo never human-reviewed
// (LastReviewed nil, e.g. only agent-swept) does NOT nudge: the staleness clock
// starts at the first human review, so the nudge stays quiet until you opt a
// repo into a review cadence.
//
// Scoped like the rest of the summary, and it must apply BOTH repo filters the
// way QueryOpts.match does: the board scope (ScopeRepo) AND an explicit -r
// (Repo). Testing only ScopeRepo made `furrow brief -r X` scope its task
// signals to X while reporting the stale clock of every repo on the board —
// cli/scope.go clears ScopeRepo and sets Repo for an explicit -r, so the one
// filter this checked was always empty in exactly that case. A board-wide sync
// sets neither and considers every repo. The input records are already sorted
// by Repo, so the output is canonical.
func (a *App) unreviewedRepos(o QueryOpts, now time.Time) ([]UnreviewedRepo, error) {
	days := a.Cfg.ReviewStaleAfterDays
	if days <= 0 {
		return nil, nil
	}
	recs, err := a.Store.ListRepos()
	if err != nil {
		return nil, err
	}
	threshold := time.Duration(days) * 24 * time.Hour
	var out []UnreviewedRepo
	for _, rec := range recs {
		if o.ScopeRepo != "" && !strings.EqualFold(rec.Repo, o.ScopeRepo) {
			continue
		}
		if o.Repo != "" && !strings.EqualFold(rec.Repo, o.Repo) {
			continue
		}
		if rec.LastReviewed == nil {
			continue
		}
		if age := now.Sub(*rec.LastReviewed); age >= threshold {
			out = append(out, UnreviewedRepo{Repo: rec.Repo, Days: int(age.Hours() / 24)})
		}
	}
	return out, nil
}
