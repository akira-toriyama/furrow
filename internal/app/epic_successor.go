package app

import (
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// The declared successor's read side (schema v12): what `epic done` proposes
// from the closed box's declaration, and what `brief` names when no box is
// active. The declaration itself is written by EpicSet (checkSuccessor).

// The reasons a declared successor is proposed with a caveat, or not at all.
// "" (Reason omitted) is the plain proposal: open it. The CLI prints the
// human line per reason; a consumer branches on the field, never the text.
const (
	SuccessorActive   = "active"    // the box is already active: no switch to make
	SuccessorSlotHeld = "slot-held" // another box holds a repo slot it needs (Held)
	SuccessorNoRepo   = "no-repo"   // it names no repo, so it cannot be activated
	SuccessorClosed   = "closed"    // it (and every box its own chain leads to) is closed
	SuccessorMissing  = "missing"   // the declared id names no box
)

// SuccessorSuggest is `epic done`'s answer from the closed box's declaration:
// the box to open next (ID), resolved from what was declared (Declared) by
// walking past closed boxes along their own chains (Skipped), with the one
// Reason it cannot simply be activated, the deps it would still cross
// (OpenDeps, computed after the close so the box just closed is not among
// them) and, when slot-held, the holders (Held: repo -> epic). Like
// PreviousActive it is display data computed after the write, never a gate.
type SuccessorSuggest struct {
	Declared string            `json:"declared"`
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	State    string            `json:"state"` // open | closed | missing
	Reason   string            `json:"reason,omitempty"`
	Skipped  []string          `json:"skipped"`
	OpenDeps []string          `json:"open_deps"`
	Held     map[string]string `json:"held,omitempty"`
}

// SuccessorSuggest resolves the declared successor of closedID. nil when the
// box declares none; best-effort, never an error (an unreadable epic set is a
// silent nil, as PreviousActiveSuggest treats it).
func (a *App) SuccessorSuggest(closedID string) *SuccessorSuggest {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil
	}
	return successorSuggestIn(epics, closedID)
}

// successorSuggestIn is SuccessorSuggest over a loaded epic set. The walk
// follows successor pointers from the declaration until an open box or a
// dead end: a missing id, an empty pointer, or an id already visited (a cycle
// a merge landed). A dead end past the declared box reports the DECLARED box
// as closed — the plan as written is what the human must re-judge, and lint's
// epic-successor-closed already names it while the declarer is open.
func successorSuggestIn(epics []core.Epic, closedID string) *SuccessorSuggest {
	byID := core.EpicsByID(epics)
	from := byID[closedID]
	if from == nil || from.Successor == "" {
		return nil
	}
	s := &SuccessorSuggest{Declared: from.Successor, Skipped: []string{}, OpenDeps: []string{}}
	seen := map[string]bool{closedID: true}
	for id := from.Successor; ; {
		if seen[id] {
			return s.closedDeadEnd(byID)
		}
		seen[id] = true
		e, ok := byID[id]
		switch {
		case !ok && id == s.Declared:
			s.ID, s.State, s.Reason = id, "missing", SuccessorMissing
			return s
		case !ok:
			return s.closedDeadEnd(byID)
		case e.IsOpen():
			s.proposeOpen(e, epics)
			return s
		}
		s.Skipped = append(s.Skipped, id)
		if id = e.Successor; id == "" {
			return s.closedDeadEnd(byID)
		}
	}
}

func (s *SuccessorSuggest) closedDeadEnd(byID map[string]*core.Epic) *SuccessorSuggest {
	s.ID, s.Title, s.State, s.Reason = s.Declared, byID[s.Declared].Title, "closed", SuccessorClosed
	return s
}

// proposeOpen fills the proposal for the open box the walk reached, with the
// caveat the activation would meet: the same per-repo slot rule
// `epic activate` enforces (heldSlots), judged on the epic set AFTER the
// close, so the box just closed holds nothing.
func (s *SuccessorSuggest) proposeOpen(e *core.Epic, epics []core.Epic) {
	s.ID, s.Title, s.State = e.ID, e.Title, "open"
	if deps := openEpicDeps(e, epics); len(deps) > 0 {
		s.OpenDeps = deps
	}
	switch held := heldSlots(e, epics); {
	case e.Active:
		s.Reason = SuccessorActive
	case len(e.Repos) == 0:
		s.Reason = SuccessorNoRepo
	case len(held) > 0:
		s.Reason, s.Held = SuccessorSlotHeld, held
	}
}

// Proposed reports whether the suggestion names a box that is, or can simply
// be made, the focus: the plain proposal and the already-active case. The
// other reasons leave the freed slot uncovered, so the computed previous
// speaks for it.
func (s *SuccessorSuggest) Proposed() bool {
	return s != nil && (s.Reason == "" || s.Reason == SuccessorActive)
}

// uncoveredRepos is the subset of `repos` (the closed box's) the proposed
// successor does not span — the freed slots the declaration says nothing
// about, which the computed previous answers for. Every repo when nothing is
// proposed.
func (s *SuccessorSuggest) uncoveredRepos(repos []string, epics []core.Epic) []string {
	if !s.Proposed() {
		return repos
	}
	var covers []string
	if e := core.EpicsByID(epics)[s.ID]; e != nil {
		covers = e.Repos
	}
	var out []string
	for _, r := range repos {
		if !anyRepoMatch(covers, []string{r}) {
			out = append(out, r)
		}
	}
	return out
}

// UncoveredRepos is uncoveredRepos over the store's epic set (nil on a read
// failure, i.e. nothing to answer for — a suggestion must not fail the
// mutation it rides on).
func (a *App) UncoveredRepos(s *SuccessorSuggest, repos []string) []string {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil
	}
	return s.uncoveredRepos(repos, epics)
}

// NextUp is brief's "nothing active, but a plan says what opens next": the
// box a closed one declared to follow it, when that box can simply be
// activated now. After is the closed declarer, AfterClosed when it closed.
type NextUp struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	After       string    `json:"after"`
	AfterClosed time.Time `json:"after_closed"`
}

// nextUpIn picks, among the closed boxes in the repo scope ("" = board-wide)
// whose declaration resolves to a plain proposal, the most recently closed
// one's successor — the latest decision wins, a same-instant tie to the
// smaller declarer id. nil when no plan is open to take.
func nextUpIn(epics []core.Epic, repo string) *NextUp {
	var best *NextUp
	for i := range epics {
		e := &epics[i]
		if e.IsOpen() || e.Successor == "" || (repo != "" && !anyRepoMatch(e.Repos, []string{repo})) {
			continue
		}
		s := successorSuggestIn(epics, e.ID)
		if s == nil || s.Reason != "" {
			continue
		}
		at := *e.Closed
		if best == nil || at.After(best.AfterClosed) || (at.Equal(best.AfterClosed) && strings.Compare(e.ID, best.After) < 0) {
			best = &NextUp{ID: s.ID, Title: s.Title, After: e.ID, AfterClosed: at}
		}
	}
	return best
}
