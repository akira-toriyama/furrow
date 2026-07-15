package app

import (
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// childrenMap indexes tasks by their parent id (pointers into idx.Tasks). The
// container revisit signals need the hierarchy, which the pure per-task
// core.RevisitReasons cannot see — this is why they live in the app layer.
func childrenMap(idx *core.Index) map[string][]*core.Task {
	m := map[string][]*core.Task{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Parent != "" {
			m[t.Parent] = append(m[t.Parent], t)
		}
	}
	return m
}

// containerReasons computes the container-only revisit signals for an OPEN
// container: children_done (has children, all done → consider closing) and
// stuck_container (open work under it but no actionable descendant). It returns
// nil for a non-container OR a container with zero children — a freshly-declared
// empty box must never nag (the flagship "declare the epic first" use-case).
func (a *App) containerReasons(t *core.Task, kids map[string][]*core.Task, idx *core.Index, doneIDs map[string]bool) []core.RevisitReason {
	if !a.Cfg.IsContainerType(t.Type) {
		return nil
	}
	children := kids[t.ID]
	if len(children) == 0 {
		return nil
	}
	allDone := true
	for _, k := range children {
		if k.Status != a.Cfg.DoneLane {
			allDone = false
			break
		}
	}
	if allDone {
		return []core.RevisitReason{{Code: core.RevisitChildrenDone, Detail: fmt.Sprintf("all %d children done — consider closing", len(children))}}
	}
	// Not all done: it is stuck iff there is open work under it but nothing
	// actionable anywhere in the subtree (recursing through sub-containers).
	if a.hasOpenDescendant(t.ID, kids, map[string]bool{}) && !a.hasActionableDescendant(t.ID, kids, idx, doneIDs, map[string]bool{}) {
		return []core.RevisitReason{{Code: core.RevisitStuckContainer, Detail: "open children but no actionable descendant"}}
	}
	return nil
}

// hasActionableDescendant reports whether any descendant of id is actionable
// (`next`'s own predicate). seen guards a merged-in parent cycle from looping.
func (a *App) hasActionableDescendant(id string, kids map[string][]*core.Task, idx *core.Index, doneIDs, seen map[string]bool) bool {
	for _, k := range kids[id] {
		if seen[k.ID] {
			continue
		}
		seen[k.ID] = true
		if a.actionable(idx, k, doneIDs) || a.hasActionableDescendant(k.ID, kids, idx, doneIDs, seen) {
			return true
		}
	}
	return false
}

// hasOpenDescendant reports whether any descendant of id sits in a non-terminal
// lane — genuinely open work, as opposed to parked (done/icebox/waiting).
func (a *App) hasOpenDescendant(id string, kids map[string][]*core.Task, seen map[string]bool) bool {
	for _, k := range kids[id] {
		if seen[k.ID] {
			continue
		}
		seen[k.ID] = true
		if !a.Cfg.IsTerminal(k.Status) || a.hasOpenDescendant(k.ID, kids, seen) {
			return true
		}
	}
	return false
}

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
	kids := childrenMap(idx)
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
		reasons = append(reasons, a.containerReasons(t, kids, idx, doneIDs)...)
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
	DepDone    []string         `json:"dep_done"`             // task ids with >=1 dependency in the done lane
	Stale      []string         `json:"stale"`                // task ids not updated within staleDays
	Unreviewed []UnreviewedRepo `json:"unreviewed,omitempty"` // repos past [review].stale_after_days (omitted when none, so the existing JSON shape is unchanged)
	// ChildrenDone / StuckContainer are the container signals. omitempty so a board
	// with no containers (every board before v5) keeps the exact prior JSON shape.
	ChildrenDone   []string `json:"children_done,omitempty"`   // open container ids whose children are all done
	StuckContainer []string `json:"stuck_container,omitempty"` // open container ids with open work but no actionable descendant
}

// Empty reports whether nothing is worth surfacing (a clean board).
func (s RevisitSummary) Empty() bool {
	return len(s.DepDone) == 0 && len(s.Stale) == 0 && len(s.Unreviewed) == 0 &&
		len(s.ChildrenDone) == 0 && len(s.StuckContainer) == 0
}

// RevisitSummary tallies the dep_done and stale signals over the open
// (non-terminal) tasks passing o.match. With a scope set (ScopeRepo/Repo),
// repo-less drafts are excluded — the difference from Revisit, which always
// surfaces drafts as no_repo; a board-wide o (no scope) counts them.
// staleDays <= 0 disables the stale half (matching core.RevisitReasons).
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
	kids := childrenMap(idx)
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
		for _, r := range a.containerReasons(t, kids, idx, doneIDs) {
			switch r.Code {
			case core.RevisitChildrenDone:
				sum.ChildrenDone = append(sum.ChildrenDone, t.ID)
			case core.RevisitStuckContainer:
				sum.StuckContainer = append(sum.StuckContainer, t.ID)
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
// repo into a review cadence. Scoped like the rest of the summary — a set
// ScopeRepo limits it to that repo; a board-wide sync considers every repo. The
// input records are already sorted by Repo, so the output is canonical.
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
		if o.ScopeRepo != "" && rec.Repo != o.ScopeRepo {
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
