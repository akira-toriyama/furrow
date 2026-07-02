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
)

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
	if staleDays > 0 {
		if age := now.Sub(t.Updated); age >= time.Duration(staleDays)*24*time.Hour {
			days := int(age.Hours() / 24)
			rs = append(rs, RevisitReason{Code: RevisitStale, Detail: fmt.Sprintf("no update in %dd (threshold %dd)", days, staleDays)})
		}
	}
	for _, dep := range t.Deps {
		if doneIDs[dep] {
			rs = append(rs, RevisitReason{Code: RevisitDepDone, Detail: fmt.Sprintf("dep %s is done", dep)})
		}
	}
	return rs
}
