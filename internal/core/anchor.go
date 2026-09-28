package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The anchor pair (schema v11): an epic's calendar day and the tasks whose
// dues follow it. This file holds what is derivable from the two fields alone
// — the day's spelling, the arithmetic between days, and the lint rules — so
// the app layer owns only WHEN a move happens (an `epic set --anchor` write)
// and the CLI only how it is shown.
//
// Two rules shape everything here:
//   - The day is a DAY. AnchorLayout is the one spelling, parsed in UTC because
//     no zone can change which day "2026-11-21" is; the board's calendar enters
//     only when a due (an instant) is measured against it.
//   - Nothing is recomputed from an offset. A follower keeps its absolute due;
//     a move applies the day delta once (app.ShiftDue's calendar-day rule, so
//     the wall clock survives a DST boundary), and D-N is derived for display
//     from due − anchor, never stored.

// AnchorLayout is the one spelling an epic's anchor takes: a calendar day.
// AnchorSpelling is the same layout as prose says it, for messages.
const (
	AnchorLayout   = "2006-01-02"
	AnchorSpelling = "YYYY-MM-DD"
)

// ParseAnchor binds an anchor spelling to its day (midnight UTC — a carrier for
// the date, never an instant a due is compared to). Whitespace-trimmed; any
// other shape is a validation error naming the layout, since the value came
// from an operator's flag or a hand-edited shard.
func ParseAnchor(s string) (time.Time, error) {
	d, err := time.Parse(AnchorLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, Validationf("", "anchor %s is not a calendar day — spell it %s (2026-11-21)", strconv.Quote(s), AnchorSpelling)
	}
	return d, nil
}

// AnchorDays is the calendar-day distance from one anchor spelling to
// another: +7 when `to` is a week after `from`, negative when earlier. Both
// are parsed with ParseAnchor, so an unparsable spelling is that error.
func AnchorDays(from, to string) (int, error) {
	f, err := ParseAnchor(from)
	if err != nil {
		return 0, err
	}
	t, err := ParseAnchor(to)
	if err != nil {
		return 0, err
	}
	return int(t.Sub(f).Hours() / 24), nil
}

// AnchorOffsetDays is a follower's D-N: the calendar days from the anchor's
// day to the day the due falls on in loc — the board's calendar, because a due
// is bound there (a bare `--due 2026-11-07` is the END of the 7th in that
// zone, and reading it elsewhere would put it on the 8th). Negative before
// the anchor (D-14 is -14), zero on the day. A nil loc means UTC.
func AnchorOffsetDays(anchor string, due time.Time, loc *time.Location) (int, error) {
	a, err := ParseAnchor(anchor)
	if err != nil {
		return 0, err
	}
	if loc == nil {
		loc = time.UTC
	}
	y, m, d := due.In(loc).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int(day.Sub(a).Hours() / 24), nil
}

// FormatAnchorOffset renders an offset the way a schedule names it: D-14, D0,
// D+3.
func FormatAnchorOffset(days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("D-%d", -days)
	case days > 0:
		return fmt.Sprintf("D+%d", days)
	}
	return "D0"
}

// AnchorProblems runs the consistency rules over the anchor pair. Every state
// here is one the write paths refuse, so a finding means a hand-edit or a
// merge landed it — the backstop role every shard-integrity lint plays:
//
//   - anchor-invalid   (error) an epic's anchor is not a calendar day; a move
//     cannot compute a delta from it
//   - anchor-missing   (error) a task follows an epic that does not exist
//   - anchor-unset     (warn)  a task follows an epic that has no anchor — the
//     pointer has nothing to move with (an `epic set --clear-anchor` leaves
//     this state on purpose, disclosed)
//   - anchor-undated   (warn)  a follower with no due — nothing to move
//   - anchor-on-repeat (warn)  a follower that repeats — a series follows its
//     own repeat_anchor, and a move would advance this occurrence and leave
//     the series behind
//
// A task in the done lane is exempt from the task-side rules: its due is
// history, a move never touches it, and the pointer it keeps is a record of
// what it followed.
func AnchorProblems(idx *Index, epics []Epic, doneLane string) []Problem {
	var out []Problem
	byID := make(map[string]*Epic, len(epics))
	for i := range epics {
		e := &epics[i]
		byID[e.ID] = e
		if e.Anchor == "" {
			continue
		}
		if _, err := ParseAnchor(e.Anchor); err != nil {
			out = append(out, Problem{SevError, "anchor-invalid", e.ID, fmt.Sprintf(
				"anchor %q is not a calendar day (%s), so `epic set --anchor` cannot compute a move from it — clear it (`furrow epic set %s --clear-anchor`) and set the day again",
				e.Anchor, AnchorSpelling, e.ID)})
		}
	}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Anchor == "" || t.Status == doneLane {
			continue
		}
		e, ok := byID[t.Anchor]
		switch {
		case !ok:
			out = append(out, Problem{SevError, "anchor-missing", t.ID, fmt.Sprintf(
				"follows epic %q, which does not exist — point it at a box (`furrow set %s --anchor <epic>`) or drop it (`--clear-anchor`)", t.Anchor, t.ID)})
		case e.Anchor == "":
			out = append(out, Problem{SevWarn, "anchor-unset", t.ID, fmt.Sprintf(
				"follows epic %s, which has no anchor — give the box its day (`furrow epic set %s --anchor <%s>`) or drop the pointer (`furrow set %s --clear-anchor`)", t.Anchor, t.Anchor, AnchorSpelling, t.ID)})
		}
		if t.Due == nil {
			out = append(out, Problem{SevWarn, "anchor-undated", t.ID, fmt.Sprintf(
				"follows an anchor but carries no due, so a move has nothing to shift — promise a date (`furrow set %s --due <date>`) or drop the pointer (`--clear-anchor`)", t.ID)})
		}
		if t.Repeat != "" {
			out = append(out, Problem{SevWarn, "anchor-on-repeat", t.ID, fmt.Sprintf(
				"follows an anchor AND repeats — a series follows its own repeat_anchor, and an anchor move would shift this occurrence alone; drop one of the two (`furrow set %s --clear-anchor` or `--clear-repeat`)", t.ID)})
		}
	}
	sortProblems(out)
	return out
}
