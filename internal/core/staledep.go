package core

import (
	"fmt"
	"sort"
	"time"
)

// StaleDepProblems reports each open task that has fallen out of sync with a
// dependency that has since shipped: a non-terminal task X with a dep D in the
// done lane whose Closed time is strictly after X.Updated. That gap means D was
// completed after X was last touched — so X has NOT been reconciled since its
// dependency landed (the "reconcile-on-close" backstop). The motivating case is
// an epic whose slice is wired as a dep (the house convention): when the slice
// closes, the epic should be reconciled or closed, and this catches the ones
// nobody went back to.
//
// It is the lint twin of the `dep_done` revisit signal, but time-gated so it
// stays quiet: a task that WAS updated after its dep closed (i.e. already
// reconciled) does not warn, and touching the task clears the warning. A warn,
// never an error — an un-reconciled epic is advisory, not a broken invariant.
//
// Terminal (done/parked) tasks are skipped: there is nothing to reconcile about
// finished or parked work. A done dep with no Closed stamp is skipped too —
// without a timestamp there is no gap to measure (a legacy/hand-edited done task
// is the only way that arises). Only edges to ids in doneIDs are considered, so
// an unknown or not-yet-done dep contributes nothing (Validate reports unknown
// deps separately). Findings are one per (task, stale dep), matching Validate's
// per-dep style, in a deterministic order (by task id, then message).
// ParentDoneProblems is reconcile-gap's twin on the OTHER edge: an open task whose
// PARENT is already done. The epic was closed with work still under it — either the
// child belongs somewhere else now, or the parent was closed too early.
//
// Deps and parent both express "this belongs to that", and the house convention
// wires an epic's slices as deps — which is why reconcile-gap only ever looked at
// deps, and why the hierarchy could rot unwatched: nothing reported a done epic
// with live children, and there was no way to re-parent them even once you noticed
// (that is the gap `furrow parent` closes). Now that re-parenting is one command,
// the state is worth naming.
//
// Deliberately NOT time-gated the way reconcile-gap is: a stale dep is a moment
// (the dep closed after you last looked), while a live child under a done parent is
// a STANDING misfiling — it does not become correct by touching the child. It stays
// a warn: the data is intact, and closing an epic ahead of its tail is a legitimate
// (if untidy) thing to do. It clears when the child closes, moves to a terminal
// lane, is re-parented, or the parent is reopened. An unknown parent is skipped —
// Validate's parent-missing already owns that.
func ParentDoneProblems(idx *Index, terminal, doneIDs map[string]bool) []Problem {
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Parent == "" || terminal[t.Status] {
			continue
		}
		if !doneIDs[t.Parent] {
			continue
		}
		out = append(out, Problem{SevWarn, "parent-done", t.ID, fmt.Sprintf(
			"parent %s is done but this task is still open — the epic closed with work left under it; re-parent it (`furrow parent %s <new-parent>` or `--rm`) or reopen %s",
			t.Parent, t.ID, t.Parent)})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func StaleDepProblems(idx *Index, terminal, doneIDs map[string]bool) []Problem {
	closedOf := make(map[string]*time.Time, len(idx.Tasks))
	for i := range idx.Tasks {
		closedOf[idx.Tasks[i].ID] = idx.Tasks[i].Closed
	}

	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if terminal[t.Status] {
			continue
		}
		for _, dep := range t.Deps {
			if !doneIDs[dep] {
				continue // not a done dependency
			}
			closed := closedOf[dep]
			if closed == nil {
				continue // no Closed stamp -> no gap to measure
			}
			if closed.After(t.Updated) {
				out = append(out, Problem{SevWarn, "reconcile-gap", t.ID, fmt.Sprintf(
					"dep %s is done but closed after this task's last update — reconcile or close this task", dep)})
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].ID != out[b].ID {
			return out[a].ID < out[b].ID
		}
		return out[a].Msg < out[b].Msg
	})
	return out
}
