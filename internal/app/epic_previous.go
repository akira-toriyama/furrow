package app

import (
	"strings"
)

// PreviousActive is the "return here" candidate `epic done`/`epic deactivate`
// attach to their output: among the open boxes sharing a free repo with the
// box being left, the most recently activated one — i.e. the focus a session
// in that repo most likely stepped away from. At is the activation stamp
// exactly as recordSwitch wrote it (minute precision, the writing machine's
// local wall clock, no zone).
type PreviousActive struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	At    string `json:"at"`
}

// PreviousActiveSuggest computes the previous-active suggestion for leaving
// exceptID (the box being closed/deactivated — the place being LEFT, so never
// a candidate itself). held is set instead when there is nothing to return
// to: every repo of the box being left still has an active box (the repo as
// the left box spells it -> that box's id).
//
// The mechanism is deliberately STATELESS: furrow records nothing new and
// keeps no "previous" pointer to go stale — it re-reads the activation log
// recordSwitch already writes into each epic's body (the same lines sync's
// switchLineRe publishes) and computes the answer fresh every time, per the
// operating rule "never record where to return; compute it, and when it
// cannot be computed, ask the human". Consequently:
//
//   - Only boxes sharing a FREE repo of the box being left are candidates — a
//     repo no other active box holds, folded as the slot map compares repos.
//     The answer is the focus a session in THAT repo stepped away from:
//     board-wide recency handed nearly every box on a central board another
//     repo's focus (t-b5q5). A repo still held already has its focus, so
//     closing a parked box beside it leaves nothing to return to (held), and
//     a box suggested there would be one `epic activate` refuses. An active
//     box never qualifies, since it holds its own repos. A box with no repo
//     shares none, so leaving it answers unknown.
//   - Only OPEN boxes are candidates: a closed box cannot be activated.
//   - Best-effort, never an error: an unreadable epic set or body is a silent
//     skip — a suggestion must not fail the mutation it rides on. nil means
//     UNKNOWN (no record found), and the caller says so rather than guessing;
//     activation records only exist since v6 (2026-07-29), so unknown is the
//     expected answer on older boxes.
//   - Stamps are minute-precision local time with no zone, so cross-machine
//     order can be wrong — acceptable for a suggestion, which the human
//     confirms by running `epic activate` (furrow never activates on its own).
//
// A same-stamp tie breaks to the smaller id, so the answer is deterministic.
func (a *App) PreviousActiveSuggest(exceptID string) (prev *PreviousActive, held map[string]string) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, nil
	}
	holder := map[string]string{} // folded repo -> the active box holding its slot
	var leaving []string
	for i := range epics {
		e := &epics[i]
		switch {
		case e.ID == exceptID:
			leaving = e.Repos
		case e.Active && e.IsOpen():
			for _, r := range e.Repos {
				holder[strings.ToLower(r)] = e.ID
			}
		}
	}
	var free []string
	for _, r := range leaving {
		if id, ok := holder[strings.ToLower(r)]; ok {
			if held == nil {
				held = map[string]string{}
			}
			held[r] = id
			continue
		}
		free = append(free, r)
	}
	if len(free) == 0 {
		return nil, held
	}
	for i := range epics {
		e := &epics[i]
		if e.ID == exceptID || !e.IsOpen() || !anyRepoMatch(e.Repos, free) {
			continue
		}
		body, err := a.Store.LoadBody(e.ID)
		if err != nil {
			continue
		}
		at := latestActivation(body)
		if at == "" {
			continue
		}
		if prev == nil || at > prev.At || (at == prev.At && e.ID < prev.ID) {
			prev = &PreviousActive{ID: e.ID, Title: e.Title, At: at}
		}
	}
	return prev, nil
}

// latestActivation returns the newest recordSwitch stamp in body ("" when it
// holds none), read with the same switchLineRe sync uses so the log's consumers
// can never disagree on what an activation line is. The
// "YYYY-MM-DD HH:MM" layout is zero-padded fixed-width, so plain string
// comparison IS chronological order (within one machine's zone — see the
// caveat on PreviousActiveSuggest).
func latestActivation(body string) string {
	latest := ""
	for _, line := range strings.Split(body, "\n") {
		if m := switchLineRe.FindStringSubmatch(line); m != nil && m[1] > latest {
			latest = m[1]
		}
	}
	return latest
}
