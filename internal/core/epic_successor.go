package core

import "fmt"

// The declared-successor rules (schema v12). The write path (app.EpicSet)
// refuses a self-reference, a closed target, a cycle, a declaration that
// contradicts a dep, and a standing declarer; lint is the git-merge backstop
// for what the write path cannot see land.

// EpicSuccessorReaches reports whether following successor declarations from
// `from` arrives at `to` — the cycle test `epic set --successor` runs before
// declaring to→from. Unknown ids end the walk; a visited set ends a cycle
// already on disk.
func EpicSuccessorReaches(epics []Epic, from, to string) bool {
	byID := EpicsByID(epics)
	seen := map[string]bool{}
	for id := from; id != "" && !seen[id]; {
		if id == to {
			return true
		}
		seen[id] = true
		e, ok := byID[id]
		if !ok {
			return false
		}
		id = e.Successor
	}
	return false
}

// epicSuccessorProblems runs the rules over the declarations alone. Called
// from EpicProblems so every lint caller gets them for free.
//
//   - epic-successor-missing (error) an OPEN box naming a successor that does
//     not exist — the epic twin of epic-dep-missing: ids are never reused, so
//     this is a typo or a half-merged hand edit, and the plan it stands for
//     cannot be proposed when the box closes.
//   - epic-successor-closed  (warn)  an OPEN box whose declared successor has
//     already closed. `epic done` would walk past it along its own chain, but
//     the plan as written is stale, and prose that named a box which closed
//     first is exactly what this field replaced — so the declarer is nudged to
//     re-point or clear it while it can still be judged.
//   - epic-successor-dep     (error) an OPEN box that waits (epic dep, through
//     any chain) on the box it declares to open after it — the contradiction
//     both write paths refuse (`epic set --successor`, `epic dep`), landed by
//     a merge of two consistent halves.
//   - epic-successor-standing (warn) an OPEN standing box carrying a
//     declaration: it never closes, so the plan never fires, and `epic set`
//     refuses either flag on it until one is cleared.
//
// Only OPEN declarers are judged: a closed box's pointer is history, and a
// target that closes after its declarer did is the ordinary order of events.
// No cycle rule: the write path refuses the closing edge, and a cycle landing
// through a merge is walked with a visited set everywhere it is read.
func epicSuccessorProblems(epics []Epic) []Problem {
	var out []Problem
	byID := EpicsByID(epics)
	for i := range epics {
		e := &epics[i]
		if e.Successor == "" || !e.IsOpen() {
			continue
		}
		s, ok := byID[e.Successor]
		switch {
		case !ok:
			out = append(out, Problem{SevError, "epic-successor-missing", e.ID, fmt.Sprintf(
				"declared successor %q does not exist — re-point it (`furrow epic set %s --successor <epic>`) or clear it (`--clear-successor`)", e.Successor, e.ID)})
			continue
		case !s.IsOpen():
			out = append(out, Problem{SevWarn, "epic-successor-closed", e.ID, fmt.Sprintf(
				"declared successor %s is already closed — `epic done %s` would walk past it; re-point it (`furrow epic set %s --successor <epic>`) or clear it (`--clear-successor`)", e.Successor, e.ID, e.ID)})
		}
		if EpicDependsOn(epics, e.ID, e.Successor) {
			out = append(out, Problem{SevError, "epic-successor-dep", e.ID, fmt.Sprintf(
				"waits on %s and declares it as the successor — a box cannot open after one that opens after it; drop the dep (`furrow epic dep %s %s --rm`) or the declaration (`furrow epic set %s --clear-successor`)", e.Successor, e.ID, e.Successor, e.ID)})
		}
		if e.Standing {
			out = append(out, Problem{SevWarn, "epic-successor-standing", e.ID, fmt.Sprintf(
				"is standing (never closes) but declares successor %s — the plan never fires; clear it (`furrow epic set %s --clear-successor`) or the flag (`--standing=false`)", e.Successor, e.ID)})
		}
	}
	return out
}
