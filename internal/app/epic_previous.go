package app

import (
	"strings"
)

// PreviousActive is the "return here" candidate `epic done`/`epic deactivate`
// attach to their output: the box that was most recently activated among the
// open, currently-inactive ones — i.e. the focus a session most likely stepped
// away from. At is the activation stamp exactly as recordSwitch wrote it
// (minute precision, the writing machine's local wall clock, no zone).
type PreviousActive struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	At    string `json:"at"`
}

// PreviousActiveSuggest computes the previous-active suggestion, excluding
// exceptID (the box being closed/deactivated — it is the place being LEFT).
//
// The mechanism is deliberately STATELESS: furrow records nothing new and
// keeps no "previous" pointer to go stale — it re-reads the activation log
// recordSwitch already writes into each epic's body (the same lines sync's
// switchLineRe publishes) and computes the answer fresh every time, per the
// operating rule "never record where to return; compute it, and when it
// cannot be computed, ask the human". Consequently:
//
//   - Only OPEN, currently-INACTIVE boxes are candidates: a closed box cannot
//     be activated, and a box that is active right now needs no returning to
//     (another repo's focus would otherwise always win on recency).
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
func (a *App) PreviousActiveSuggest(exceptID string) *PreviousActive {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil
	}
	var best *PreviousActive
	for i := range epics {
		e := &epics[i]
		if e.ID == exceptID || !e.IsOpen() || e.Active {
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
		if best == nil || at > best.At || (at == best.At && e.ID < best.ID) {
			best = &PreviousActive{ID: e.ID, Title: e.Title, At: at}
		}
	}
	return best
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
