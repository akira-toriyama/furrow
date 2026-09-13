package app

// EpicOpenMember is one member `epic done` leaves behind: a task still in a
// NON-TERMINAL lane under the box that just closed — exactly the set
// core.EpicProblems raises `epic-closed` for, so the disclosure and the lint
// warning can never count different things.
//
// Repeat is the member's recurrence rule ("" when it does not recur) and is the
// reason this type exists rather than a bare count. A plain member's
// `epic-closed` clears the moment the task closes; a REPEATING member's does
// not — closing it mints the next occurrence, which inherits `epic` (repeat.go:
// the successor carries everything the close did not settle) and so re-raises
// the warn under the same closed box every cycle. Not a defect to refuse: the
// remedy is one `furrow set <live-id> -e <open-epic>`, which every later
// occurrence inherits in turn. Disclosing it at close time is what makes that
// choice visible while the operator is still looking.
type EpicOpenMember struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Repeat carries no omitempty on purpose: "" is the ANSWER "this member does
	// not recur", and a consumer must read it rather than infer it from a key
	// that is not there.
	Repeat string `json:"repeat"`
}

// EpicOpenMembers lists the box's members still in a non-terminal lane, in the
// canonical read order `epic show` prints (lane, priority, id) — it goes through
// ListItems for exactly that reason, so two views of the same members can never
// disagree about their order. Board-wide: no repo scope narrows it, because the
// box being closed is not scoped either and a member hidden by the reader's cwd
// would be left behind in silence.
//
// It is DISPLAY data in the shape of PreviousActiveSuggest: computed AFTER the
// write, so it can never affect the mutation it rides on. It differs on one
// point — a read failure returns nil, NOT an empty slice, and the caller reports
// that it could not look. An unreadable board rendering as "nothing left open"
// would be a false all-clear, which is worse than no disclosure at all.
func (a *App) EpicOpenMembers(epicID string) []EpicOpenMember {
	items, err := a.ListItems(QueryOpts{Epic: epicID})
	if err != nil {
		return nil
	}
	out := []EpicOpenMember{}
	for i := range items {
		t := &items[i].Task
		if a.Cfg.IsTerminal(t.Status) {
			continue // a done or parked member predating the close is not left behind
		}
		out = append(out, EpicOpenMember{ID: t.ID, Title: t.Title, Status: t.Status, Repeat: t.Repeat})
	}
	return out
}

// RepeatingMembers returns the subset that carries a recurrence rule — the
// members whose `epic-closed` re-raises itself every cycle instead of clearing
// when the task closes.
func RepeatingMembers(members []EpicOpenMember) []EpicOpenMember {
	out := []EpicOpenMember{}
	for _, m := range members {
		if m.Repeat != "" {
			out = append(out, m)
		}
	}
	return out
}
