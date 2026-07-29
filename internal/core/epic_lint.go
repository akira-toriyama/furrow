package core

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// EpicProblems runs the consistency rules that need BOTH the task index and the
// epic set. They are separated from Validate for one reason: Validate's contract
// is "everything derivable from the Index alone", and the epic set is a second
// store read. Smuggling it in as another parameter would make every caller of
// Validate load epics whether or not it cared.
//
// Rules:
//   - epic-required   (error) an OPEN task with no epic, once the board has any
//   - epic-missing    (error) a task pointing at an epic that does not exist
//   - epic-closed     (warn)  an open task under a closed epic
//   - epic-multi-active (error) two active epics naming the same repo
//   - epic-no-active  (warn)  a repo with open work but no active epic
//   - epic-id-pattern / epic-duplicate-id (error) the id-shape backstops
//   - epic-dep-missing / epic-dep-cycle (error), epic-dep-open (warn) — the
//     epic dep graph rules (epic_deps.go)
//
// terminal is the configured terminal-lane set: a done or parked task predating
// the box it would have belonged to is not a defect, so those are exempt from
// epic-required. epicIDPattern may be nil (skip the shape check).
func EpicProblems(idx *Index, epics []Epic, terminal map[string]bool, epicIDPattern *regexp.Regexp) []Problem {
	var out []Problem

	byID := make(map[string]*Epic, len(epics))
	seen := map[string]int{}
	for i := range epics {
		e := &epics[i]
		byID[e.ID] = e

		seen[e.ID]++
		if seen[e.ID] == 2 {
			out = append(out, Problem{SevError, "epic-duplicate-id", e.ID, fmt.Sprintf("duplicate epic id: %s", e.ID)})
		}
		if epicIDPattern != nil && !epicIDPattern.MatchString(e.ID) {
			out = append(out, Problem{SevError, "epic-id-pattern", e.ID, fmt.Sprintf("epic id %q does not match the configured epic id pattern", e.ID)})
		}
	}

	// epic-required fires only once the board HAS a box. A board that never
	// declared one has not adopted the feature, and erroring on every task there
	// would be noise the reader learns to ignore — the failure mode a lint code
	// cannot recover from. Declaring the first epic is what turns membership into
	// a requirement.
	out = append(out, epicMembershipProblems(idx, byID, terminal, len(epics) > 0)...)
	out = append(out, activeEpicProblems(idx, epics, terminal)...)
	out = append(out, epicDepProblems(epics)...)

	sortProblems(out)
	return out
}

// epicMembershipProblems covers the task->epic edge: unfiled, dangling, and
// filed-under-a-closed-box.
func epicMembershipProblems(idx *Index, byID map[string]*Epic, terminal map[string]bool, requireMembership bool) []Problem {
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if terminal[t.Status] {
			continue // a finished task predating its box is not a defect
		}
		if t.Epic == "" {
			// An ERROR, not a warn, and deliberately not enforced at `add`: capture
			// stays frictionless, but an unfiled task is invisible to `next`'s epic
			// scoping, so nothing but lint would ever mention it again.
			if requireMembership {
				out = append(out, Problem{SevError, "epic-required", t.ID,
					"open task has no epic — file it with `furrow set " + t.ID + " -e <epic>`"})
			}
			continue
		}
		e, ok := byID[t.Epic]
		if !ok {
			out = append(out, Problem{SevError, "epic-missing", t.ID, fmt.Sprintf("epic %q does not exist", t.Epic)})
			continue
		}
		if !e.IsOpen() {
			// Warn, not error: closing a box ahead of its tail is untidy, not broken,
			// and the data is intact. It clears when the task closes, moves to a
			// terminal lane, is re-filed, or the epic is reopened.
			out = append(out, Problem{SevWarn, "epic-closed", t.ID, fmt.Sprintf(
				"epic %s is closed but this task is still open — re-file it (`furrow set %s -e <epic>`) or reopen %s",
				t.Epic, t.ID, t.Epic)})
		}
	}
	return out
}

// activeEpicProblems covers the per-repo `active` invariant, in both directions.
//
// The constraint is per REPO, not per board: reads are repo-scoped by default, so
// forcing a deactivate/activate every time you change repo would cost more than it
// buys. An epic naming several repos consumes a slot in EVERY one of them — a
// cross-repo epic really is "the current core" on both sides, and the arithmetic
// stays a count per repo rather than a special case.
func activeEpicProblems(idx *Index, epics []Epic, terminal map[string]bool) []Problem {
	var out []Problem

	// repo -> the open+active epics naming it.
	activeByRepo := map[string][]string{}
	for i := range epics {
		e := &epics[i]
		if !e.Active || !e.IsOpen() {
			continue // a closed epic holds no slot, however its flag was left
		}
		for _, r := range e.Repos {
			activeByRepo[r] = append(activeByRepo[r], e.ID)
		}
	}
	for repo, ids := range activeByRepo {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		// Only reachable by a merge: EpicActivate refuses the second one. This is the
		// git-merge backstop, the same role parent-cycle used to play.
		out = append(out, Problem{SevError, "epic-multi-active", repo, fmt.Sprintf(
			"%d epics are active for %s (%s) — deactivate all but one (`furrow epic deactivate <id>`)",
			len(ids), repo, strings.Join(ids, ", "))})
	}

	// The other direction: a repo carrying open work with no box open. furrow
	// deliberately does not pick the next epic when one is closed — choosing is the
	// judgement call — so this warning is what does the nagging until a human does.
	//
	// Like epic-required it stays quiet on a board that has declared no epics at
	// all: nagging a board to activate one of the zero boxes it has is noise, and a
	// warning that fires on every clean board is one the reader stops reading.
	if len(epics) == 0 {
		return out
	}
	openRepos := map[string]bool{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if terminal[t.Status] {
			continue
		}
		for _, r := range t.Repos {
			openRepos[r] = true
		}
	}
	var idle []string
	for repo := range openRepos {
		if len(activeByRepo[repo]) == 0 {
			idle = append(idle, repo)
		}
	}
	sort.Strings(idle)
	for _, repo := range idle {
		out = append(out, Problem{SevWarn, "epic-no-active", repo,
			"no active epic for " + repo + " — pick one with `furrow epic ls -r " + repo + "` + `furrow epic activate <id>`"})
	}
	return out
}

// sortProblems applies the shared deterministic ordering (errors before warns,
// then id, then message) so two runs over the same board print identically.
func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(a, b int) bool {
		if ps[a].Severity != ps[b].Severity {
			return ps[a].Severity < ps[b].Severity // "error" < "warn"
		}
		if ps[a].ID != ps[b].ID {
			return ps[a].ID < ps[b].ID
		}
		return ps[a].Msg < ps[b].Msg
	})
}
