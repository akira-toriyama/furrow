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
	// layer does (App.RevisitEpics, read as `furrow revisit --boxes`). They live
	// here so the `furrow revisit --json` reason vocabulary has one home.
	RevisitEpicAllDone = "epic_all_done" // an open epic whose members are ALL in the done lane — consider closing it
	RevisitEpicStuck   = "epic_stuck"    // an epic `next` reads that holds unfinished members and hands out none (standing and waiting boxes exempt)
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
// this only reports signals, and it is the OPEN task's set — a task in a parked
// lane revisit reads gets ParkedRevisitReasons instead. Reasons come back in a stable order
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
		rs = append(rs, staleReason(t, now, staleDays))
	}
	return append(rs, depDoneReasons(t, doneIDs)...)
}

// ParkedRevisitReasons is RevisitReasons for a task in a parked lane the board
// asked revisit to keep watching ([revisit].watch_lanes). Such a task is not
// work to weigh, so the draft and estimate signals stay out; what remains are
// the two that say its wait may be over:
//
//   - stale, withheld while the wait has a clock of its own — a due still
//     ahead (dueCounts: the lane is one [due] tracks, so the date speaks when
//     it arrives) or a dep revisit still reads (readIDs: its close is the
//     dep_done below, and until then the dep is itself in front of the
//     reader). A parked task with neither is the one no other read brings
//     back, and elapsed time is all that is left to ask "still waiting?" with;
//   - dep_done, one per done dep, exactly as for an open task.
//
// readIDs is the tasks revisit reads: the open ones and those parked in a
// watched lane, never a done one. A dep outside both sets — frozen in a lane
// nobody watches, or a dangling edge — will never report back, so it holds
// nothing back: waiting on it is waiting on nothing the board can see. Pure
// like RevisitReasons: which lanes are read is the caller's job.
func ParkedRevisitReasons(t Task, now time.Time, staleDays int, doneIDs, readIDs map[string]bool, dueCounts bool) []RevisitReason {
	ownClock := dueCounts && t.Due != nil && t.Due.After(now)
	for _, dep := range t.Deps {
		if readIDs[dep] {
			ownClock = true
		}
	}
	rs := []RevisitReason{}
	if !ownClock && IsStale(t, now, staleDays) {
		rs = append(rs, staleReason(t, now, staleDays))
	}
	return append(rs, depDoneReasons(t, doneIDs)...)
}

// staleReason words the stale signal; the caller has already tested IsStale.
func staleReason(t Task, now time.Time, staleDays int) RevisitReason {
	days := int(now.Sub(t.Updated).Hours() / 24)
	return RevisitReason{Code: RevisitStale, Detail: fmt.Sprintf("no update in %dd (threshold %dd)", days, staleDays)}
}

// depDoneReasons is one dep_done per done dep, in Deps order.
func depDoneReasons(t Task, doneIDs map[string]bool) []RevisitReason {
	var rs []RevisitReason
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
// about the task being open, and nothing about a parked task's wait having a
// clock of its own (ParkedRevisitReasons); callers wanting revisit's view
// compose it with their own lane eligibility (e.g. `-q 'is:open is:stale'`).
func IsStale(t Task, now time.Time, staleDays int) bool {
	return staleDays > 0 && now.Sub(t.Updated) >= time.Duration(staleDays)*24*time.Hour
}
