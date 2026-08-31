package core

import (
	"fmt"
	"time"
)

// Revisit reason codes — the machine-readable signal an agent keys on to decide
// what to fix. Stable strings: they are part of the `furrow revisit --json`
// contract.
const (
	RevisitValueUnset  = "value_unset"  // Value estimate is unset
	RevisitEffortUnset = "effort_unset" // Effort estimate is unset
	RevisitStale       = "stale"        // not updated within the stale threshold
	RevisitDepDone     = "dep_done"     // a dependency is already in the done lane
	RevisitNoRepo      = "no_repo"      // repos is empty — the task is a draft awaiting a repo
	// The EPIC signals are about a box, not a task: they need the member
	// index, so RevisitReasons (pure, per-task) does not compute them — the app
	// layer does. They live here so the `furrow revisit --json` reason vocabulary
	// has one home.
	RevisitEpicAllDone = "epic_all_done" // an open epic whose members are ALL done — consider closing it
	RevisitEpicStuck   = "epic_stuck"    // an open epic with open members but not one actionable (standing boxes exempt — that is an inbox's resting state)
	// RevisitEpicStale is the ACTIVE-epic-untouched signal. It is measured in DAYS
	// (the [revisit].stale_days clock every other signal uses), not in sessions:
	// furrow has no notion of a session, and inventing a second clock so that one
	// signal could use it would put two thresholds on the same board.
	RevisitEpicStale = "epic_stale"
	// RevisitEpicDepDone is dep_done's box-level twin (v7): a parked epic whose
	// Deps are ALL closed — every box it waited on is done, so it is this one's
	// turn to be opened. It never fires for the active epic (already open) and
	// stays quiet while any dep is open or dangling: a broken edge is lint's
	// epic-dep-missing, and "time to open" must not be said on a broken graph.
	RevisitEpicDepDone = "epic_dep_done"
	// RevisitEpicReviewDue is the STANDING box's review cadence (v9): its last
	// `furrow review <epic-ref>` is older than [review].stale_after_days — the
	// same clock the per-repo review nudge reads, so one board keeps one review
	// rhythm. It is the nag a standing box trades the finish-shaped ones for:
	// exempt from epic_all_done/epic_dep_done/epic_stuck, such a box is where a
	// member's premise quietly rots, and nothing else asks "is what's inside still
	// true?". A never-reviewed box stays quiet (the cadence is opted into by
	// the first review, exactly like a repo's clock).
	RevisitEpicReviewDue = "epic_review_due"
)

// RevisitCodeList returns every revisit signal code — the complete `furrow
// revisit --json` reason vocabulary, in the canonical per-task order (the
// epic signals last, since RevisitReasons cannot emit them). Like
// LintCodeList it is the single machine-readable registry the docs are checked
// against (`furrow vocab revisit-codes` → scripts/check-docs-vocab.sh): the
// 2026-07 audit found the prose copies of this list missing the container
// signals (v5's, now the epic ones) in three places. TestRevisitCodeListMatchesConstants pins this list
// to the const block above, so a new constant cannot be forgotten here.
func RevisitCodeList() []string {
	return []string{
		RevisitNoRepo,
		RevisitValueUnset,
		RevisitEffortUnset,
		RevisitStale,
		RevisitDepDone,
		RevisitEpicAllDone,
		RevisitEpicStuck,
		RevisitEpicStale,
		RevisitEpicDepDone,
		RevisitEpicReviewDue,
	}
}

// RevisitReason is one signal that a task's metadata may need a fresh judgment.
// Code is the stable machine key; Detail is human/agent-readable context. Detail
// is deliberately factual and never names a CLI verb — keeping core decoupled
// from the cli layer (the agent maps a code to the setter to run).
type RevisitReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// RevisitReasons computes the re-evaluation signals for t at time now, given the
// stale threshold in days and the set of ids currently in the done lane. It is
// pure: eligibility (e.g. excluding terminal-lane tasks) is the caller's job;
// this only reports signals. Reasons come back in a stable order
// (no_repo, value, effort, stale, then one dep_done per done dep in Deps order)
// so output is deterministic. A staleDays <= 0 disables the stale signal.
func RevisitReasons(t Task, now time.Time, staleDays int, doneIDs map[string]bool) []RevisitReason {
	rs := []RevisitReason{}
	if len(t.Repos) == 0 {
		rs = append(rs, RevisitReason{Code: RevisitNoRepo, Detail: "attached to no repo (draft)"})
	}
	if t.Value == nil {
		rs = append(rs, RevisitReason{Code: RevisitValueUnset, Detail: "value estimate missing"})
	}
	if t.Effort == nil {
		rs = append(rs, RevisitReason{Code: RevisitEffortUnset, Detail: "effort estimate missing"})
	}
	if IsStale(t, now, staleDays) {
		days := int(now.Sub(t.Updated).Hours() / 24)
		rs = append(rs, RevisitReason{Code: RevisitStale, Detail: fmt.Sprintf("no update in %dd (threshold %dd)", days, staleDays)})
	}
	for _, dep := range t.Deps {
		if doneIDs[dep] {
			rs = append(rs, RevisitReason{Code: RevisitDepDone, Detail: fmt.Sprintf("dep %s is done", dep)})
		}
	}
	return rs
}

// IsStale reports whether t has gone without an update for at least staleDays
// (staleDays <= 0 disables staleness — nothing is ever stale). THE one
// definition of "stale": revisit's stale signal and the query's `is:stale` both
// call it, so the two can never drift. It is a pure age test — it says nothing
// about the task being open; callers wanting revisit's view compose it with
// their own lane eligibility (e.g. `-q 'is:open is:stale'`).
func IsStale(t Task, now time.Time, staleDays int) bool {
	return staleDays > 0 && now.Sub(t.Updated) >= time.Duration(staleDays)*24*time.Hour
}
