package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The anchor move — the one write where a box's field changes a task's. A
// reschedule used to be the operator's arithmetic over every dated task (76
// dues, a body grep for the ones the other side had fixed, and one `--due-shift`
// per guess); here it is the box's day moving and every follower moving with
// it, in one previewed write. core owns the day arithmetic (core/anchor.go);
// this file owns which tasks move, which are refused, and which are kept.

// DueMove is one follower's due before and after an anchor move — the row a
// preview prints and the applied envelope carries. Title and Status ride along
// so the row can be read without a second lookup, exactly as a `set -q`
// preview row is.
type DueMove struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Status string    `json:"status"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// AnchorPlan is what an `epic set --anchor` / `--clear-anchor` would do
// (PlanAnchor) or did (EpicSet's report): the day before and after, the delta
// in calendar days, the followers whose dues move, and the ones deliberately
// left alone. Every slice is [] not null.
type AnchorPlan struct {
	Epic string `json:"epic"`
	From string `json:"from"` // "" on a first set
	To   string `json:"to"`   // "" on a clear
	Days int    `json:"days"` // 0 on a first set, a clear, or the same day
	// Moves are the open followers whose due shifts by Days.
	Moves []DueMove `json:"moves"`
	// Kept are the followers in the done lane: their due is history and stays
	// where the close left it, whatever the box's day does.
	Kept []string `json:"kept"`
	// Followers are the open followers a CLEAR leaves pointing at a box with
	// no anchor — disclosed here and by lint's anchor-unset, never silently
	// unmarked (the pointer is theirs to drop). Empty on a set.
	Followers []string `json:"followers"`
}

func newAnchorPlan(epic string) *AnchorPlan {
	return &AnchorPlan{Epic: epic, Moves: []DueMove{}, Kept: []string{}, Followers: []string{}}
}

// PlanAnchor is the read behind `epic set --anchor`'s preview: the plan the
// write would apply, computed the same way EpicSet computes it, so what the
// preview shows is what --yes writes. ref resolves like every epic reference.
func (a *App) PlanAnchor(ref, date string) (*AnchorPlan, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		return nil, err
	}
	var e *core.Epic
	for i := range epics {
		if epics[i].ID == id {
			e = &epics[i]
		}
	}
	if e == nil {
		return nil, core.NotFound(id)
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.planAnchorIn(idx, e, date)
}

// planAnchorIn computes the move for setting e's anchor to date against the
// loaded index. A first set (the box had no day) and a same-day set move
// nothing: the dues the followers already carry ARE their offsets from the day
// being declared. Otherwise every open follower shifts by the day delta
// (ShiftDue's calendar-day rule, so a 13:20 stays 13:20 across a DST
// boundary), a done follower is kept, and a follower the write could not move
// — no due, or a repeat rule — refuses the whole plan up front, naming every
// such id, so the operator fixes them in one pass rather than one per retry.
func (a *App) planAnchorIn(idx *core.Index, e *core.Epic, date string) (*AnchorPlan, error) {
	day, err := core.ParseAnchor(date)
	if err != nil {
		return nil, core.Validationf(e.ID, "--anchor %s", strings.TrimPrefix(err.Error(), "anchor "))
	}
	plan := newAnchorPlan(e.ID)
	plan.From, plan.To = e.Anchor, day.Format(core.AnchorLayout)
	if e.Anchor == "" || e.Anchor == plan.To {
		return plan, nil
	}
	days, err := core.AnchorDays(e.Anchor, plan.To)
	if err != nil {
		// The STORED day is what failed to parse — a hand-edit lint already
		// names (anchor-invalid). No delta exists to move the followers by.
		return nil, core.Validationf(e.ID, "epic %s carries anchor %q, which is not a calendar day (%s), so there is no delta to move its followers by — clear it first (`furrow epic set %s --clear-anchor`), then set the day", e.ID, e.Anchor, core.AnchorSpelling, e.ID)
	}
	plan.Days = days
	spelling := fmt.Sprintf("%+dd", days)
	var undated, repeating []string
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Anchor != e.ID {
			continue
		}
		switch {
		case t.Status == a.Cfg.DoneLane:
			plan.Kept = append(plan.Kept, t.ID)
		case t.Due == nil:
			undated = append(undated, t.ID)
		case t.Repeat != "":
			repeating = append(repeating, t.ID)
		default:
			shifted, err := ShiftDue(*t.Due, spelling, a.loc())
			if err != nil {
				return nil, core.Validationf(t.ID, "%s: %v", t.ID, err)
			}
			// UTC on both sides: the plan is emitted as JSON before any Save
			// normalizes it, and a row whose `to` carried the board zone beside
			// a UTC `from` read as two calendars.
			plan.Moves = append(plan.Moves, DueMove{ID: t.ID, Title: t.Title, Status: t.Status, From: t.Due.UTC(), To: shifted.UTC()})
		}
	}
	if len(undated)+len(repeating) > 0 {
		return nil, followerRefusalErr(e.ID, undated, repeating)
	}
	return plan, nil
}

// followerRefusalErr is the all-or-nothing refusal of an anchor move: a
// follower with no due, or one that repeats, cannot be moved, and moving the
// rest around it would leave the box's followers on two different days. Only
// a hand-edit or a merge can produce either state (the task write paths refuse
// both), and lint names them (anchor-undated, anchor-on-repeat) before this
// does; details carries each list so the fix is one selection.
func followerRefusalErr(epic string, undated, repeating []string) *core.Error {
	sort.Strings(undated)
	sort.Strings(repeating)
	var parts []string
	details := map[string]any{}
	if len(undated) > 0 {
		parts = append(parts, fmt.Sprintf("%d with no due (%s)", len(undated), strings.Join(undated, ", ")))
		details["undated"] = undated
	}
	if len(repeating) > 0 {
		parts = append(parts, fmt.Sprintf("%d repeating (%s)", len(repeating), strings.Join(repeating, ", ")))
		details["repeating"] = repeating
	}
	return &core.Error{
		Code: core.CodeValidation, Kind: core.KindValidation, Subject: epic,
		Msg: fmt.Sprintf("%d follower(s) of %s cannot be moved — %s — and the move is all-or-nothing; give them a due or drop their anchor/rule first (`furrow lint --code anchor-undated --code anchor-on-repeat` lists them)",
			len(undated)+len(repeating), epic, strings.Join(parts, "; ")),
		Details: details,
	}
}

// anchorFollowers lists the open tasks whose Anchor names epic, in index
// order — the disclosure a clear owes.
func (a *App) anchorFollowers(idx *core.Index, epic string) []string {
	ids := []string{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Anchor == epic && t.Status != a.Cfg.DoneLane {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// followerRepos is the union of the moved followers' repos — what the session
// write guard judges a move on, since the move is a write to each of them.
func followerRepos(idx *core.Index, plan *AnchorPlan) []string {
	var repos []string
	for _, m := range plan.Moves {
		if t, i := idx.Find(m.ID); i >= 0 {
			repos = unionRepos(repos, t.Repos)
		}
	}
	return repos
}

// applyAnchorMoves writes the plan's moves into the loaded index: each
// follower takes its shifted due and the batch's one `updated` instant. It
// saves nothing; the caller owns the store write.
func applyAnchorMoves(idx *core.Index, plan *AnchorPlan, now time.Time) {
	for _, m := range plan.Moves {
		t, i := idx.Find(m.ID)
		if i < 0 {
			continue
		}
		to := m.To
		t.Due = &to
		t.Updated = now
	}
}

// resolveAnchorRef resolves a task-side `--anchor` value: the box it names,
// which must exist and already carry a day — a task cannot follow a day the
// box has not declared, and declaring it first is one command. A value that
// parses as a calendar day is the likeliest slip (the two --anchor flags take
// different things), so that miss says where the day goes.
func (a *App) resolveAnchorRef(ref string, epics []core.Epic) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", core.Validationf("", "--anchor needs an epic (the box whose day this due follows); use --clear-anchor to remove one")
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		if _, perr := core.ParseAnchor(ref); perr == nil {
			return "", core.Validationf("", "--anchor on a task names the BOX whose day the due follows (an epic id or ref), not the day itself — the day lives on the box: `furrow epic set <epic> --anchor %s`, then `--anchor <epic>` here", ref)
		}
		return "", err
	}
	for i := range epics {
		if epics[i].ID != id {
			continue
		}
		if epics[i].Anchor == "" {
			return "", core.Validationf("", "epic %s has no anchor yet — give the box its day first: `furrow epic set %s --anchor <%s>`", id, id, core.AnchorSpelling)
		}
		return id, nil
	}
	return "", core.NotFound(id)
}

// undatedAnchorErr is `--anchor` on a task that carries no due: a pointer
// with nothing to move. Refused all-or-nothing like a batch miss, every
// undated id in details.undated; subject is the task when there is exactly one.
func undatedAnchorErr(undated []string, total int) *core.Error {
	subject := ""
	if total == 1 {
		subject = undated[0]
	}
	return &core.Error{
		Code: core.CodeValidation, Kind: core.KindValidation, Subject: subject,
		Msg:     fmt.Sprintf("%d of %d task(s) carry no due to follow the anchor with — nothing was set; narrow the selection to dated tasks (-q has:due) or promise one in the same write (--due <date>)", len(undated), total),
		Details: map[string]any{"undated": undated},
	}
}

// repeatingAnchorErr is `--anchor` on a task that repeats: a series follows
// its own repeat_anchor, and a box's day moving would shift this occurrence
// alone. Same all-or-nothing shape, ids in details.repeating.
func repeatingAnchorErr(repeating []string, total int) *core.Error {
	subject := ""
	if total == 1 {
		subject = repeating[0]
	}
	return &core.Error{
		Code: core.CodeValidation, Kind: core.KindValidation, Subject: subject,
		Msg:     fmt.Sprintf("%d of %d task(s) repeat, and a series follows its own repeat_anchor, not a box's day — nothing was set; narrow the selection (-q no:repeat) or drop the rule in the same write (--clear-repeat)", len(repeating), total),
		Details: map[string]any{"repeating": repeating},
	}
}
