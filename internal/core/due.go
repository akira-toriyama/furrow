package core

import (
	"fmt"
	"sort"
	"time"
)

// The due stamp — "look at this on that day" — and the two findings it raises.
//
// A due date is a promise about WHEN, and the board had nowhere to keep one: the
// date lived in a body paragraph and the reminder lived in a human's head. Two
// things follow from what the date is FOR, and they shape everything here:
//
//   - It is a reminder, not a deadline table. The signal has to fire where a
//     session already looks (brief) and be checked board-wide (lint), never only
//     inside the active epic's focus — the operational tasks that carry dates are
//     exactly the ones parked outside it.
//   - It is an INSTANT, not a day. "after 10:30" was the case that asked for the
//     field. A date-only input is bound to the END of its day by the write path
//     (app.ParseDue), so a task promised for the 4th is not overdue at 00:01 on
//     the 4th.

// TimeLayout is how furrow renders a stored (UTC) timestamp for a human: local
// wall clock plus the offset that disambiguates it. One layout, shared by the
// CLI's detail views and by the due findings below, so the date in a lint
// message reads exactly like the date in `furrow show`.
const TimeLayout = "2006-01-02 15:04 -07:00"

// The states a due stamp can be in relative to a given instant. Strings, not an
// enum, because they are also the two lint codes' suffixes and read as
// themselves in prose.
const (
	DueNone    = ""        // no due stamp
	DueLater   = "later"   // promised for a future day
	DueToday   = "today"   // falls on now's calendar day, still ahead
	DueOverdue = "overdue" // the instant has passed
)

// DueStateOf classifies a task's due stamp against now. "Today" is the calendar
// day BOTH instants fall on when read in loc — the OPERATOR's zone, passed in
// rather than taken from now, because furrow's production clock is UTC (see
// core.SystemClock): reading the day off the clock would tell a JST operator on
// the morning of the 4th that a task due that evening is not due today, which is
// the exact miss the field exists to prevent. A nil loc means UTC (time's own
// default), never a silent time.Local — core is pure and must not read process
// state. Overdue wins over today: a stamp that has passed is overdue even though
// it is still the same day.
func DueStateOf(t *Task, now time.Time, loc *time.Location) string {
	if t.Due == nil {
		return DueNone
	}
	if t.Due.Before(now) {
		return DueOverdue
	}
	if loc == nil {
		loc = time.UTC
	}
	ny, nm, nd := now.In(loc).Date()
	dy, dm, dd := t.Due.In(loc).Date()
	if ny == dy && nm == dm && nd == dd {
		return DueToday
	}
	return DueLater
}

// FormatDue renders a due stamp in loc, the same way `furrow show` prints a
// timestamp. Empty for a task with no date, so a caller can append it
// unconditionally.
func FormatDue(t *Task, loc *time.Location) string {
	if t.Due == nil {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.Due.In(loc).Format(TimeLayout)
}

// DueProblems reports the tasks whose promised day has arrived (`due-today`,
// warn) or passed (`due-overdue`, ERROR). It is deliberately board-wide — no
// epic scope, no `next` lane filter — because the work that carries a date is
// usually parked outside the focus: the case this exists for was a task in
// `waiting`, in a box nobody had activated, whose only remaining step was to look
// at something the next morning.
//
// Overdue is an error, not a warning, on the operator's own reasoning: dates are
// rare here, so a warning would sit in the noise and the check would rot. The
// escape hatch is not silence but the date itself — push it (`furrow set <id>
// --due +1d`) or clear it (`--clear-due`), both of which say something true
// about the promise.
//
// skipLanes are the lanes where a date raises nothing: the done lane (the promise
// is settled) plus the configured parked lanes ([due].ignore_lanes, "icebox" by
// default). Terminal lanes as a class are deliberately NOT skipped — a board may
// mark `waiting` terminal, and a waiting task is the archetype of one that still
// needs the reminder.
func DueProblems(idx *Index, now time.Time, loc *time.Location, skipLanes map[string]bool) []Problem {
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Due == nil || skipLanes[t.Status] {
			continue
		}
		switch DueStateOf(t, now, loc) {
		case DueOverdue:
			out = append(out, Problem{SevError, "due-overdue", t.ID, fmt.Sprintf(
				"due %s has passed — do it, or push the date (`furrow set %s --due +1d`) / drop it (`--clear-due`)",
				FormatDue(t, loc), t.ID)})
		case DueToday:
			out = append(out, Problem{SevWarn, "due-today", t.ID, fmt.Sprintf(
				"due today, %s", FormatDue(t, loc))})
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
