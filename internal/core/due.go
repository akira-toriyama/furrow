package core

import (
	"fmt"
	"strings"
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
//
// terminal is the caller's lane vocabulary (as in ReadyBlockedProblems) and only
// shapes the REMEDY: a task parked in a terminal lane is not being worked, so its
// date is a reminder to chase whoever it waits on, and "do it" is the one remedy
// that is wrong there. The same overdue state in an open lane offers the work
// itself. Closing is named in both — a finished wait is closed from where it sits.
func DueProblems(idx *Index, now time.Time, loc *time.Location, skipLanes, terminal map[string]bool) []Problem {
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Due == nil || skipLanes[t.Status] {
			continue
		}
		switch DueStateOf(t, now, loc) {
		case DueOverdue:
			msg := fmt.Sprintf(
				"due %s has passed — do it, close it (`furrow done %s`), or push the date (`furrow set %s --due +1d`) / drop it (`--clear-due`)",
				FormatDue(t, loc), t.ID, t.ID)
			if terminal[t.Status] {
				msg = fmt.Sprintf(
					"due %s has passed while the task is parked in %q, where the date is a reminder rather than work to do — chase or escalate, close it (`furrow done %s`), or push the date (`furrow set %s --due +1d`) / drop it (`--clear-due`)",
					FormatDue(t, loc), t.Status, t.ID, t.ID)
			}
			out = append(out, Problem{SevError, "due-overdue", t.ID, msg})
		case DueToday:
			out = append(out, Problem{SevWarn, "due-today", t.ID, fmt.Sprintf(
				"due today, %s", FormatDue(t, loc))})
		}
	}
	sortProblems(out)
	return out
}

// DueInversionProblems reports each dated task that waits on a dependency
// promised for a LATER instant than its own due (`due-inversion`, warn): the
// task cannot close before the dep it waits on, so its date is broken on paper
// before any work is late. One finding per task, naming every inverted dep with
// its date — the remedy is per task (re-date one side, or drop the edge).
// skipLanes is the set DueProblems takes, applied to BOTH ends of the edge: a
// date in the done lane or a parked lane is not a live promise, and a parked dep
// is already ready-blocked's finding. A dep with no date is silent here —
// nothing says when it lands, so nothing contradicts. The seed of the board at
// akira-toriyama/furrow-test carried one such edge (t-fejrz → t-ehf4f) until a
// rebuild found it by hand; this is the check that would have said so.
func DueInversionProblems(idx *Index, loc *time.Location, skipLanes map[string]bool) []Problem {
	byID := make(map[string]*Task, len(idx.Tasks))
	for i := range idx.Tasks {
		byID[idx.Tasks[i].ID] = &idx.Tasks[i]
	}
	var out []Problem
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Due == nil || skipLanes[t.Status] {
			continue
		}
		var later []string
		for _, dep := range t.Deps {
			d, ok := byID[dep]
			if !ok || d.Due == nil || skipLanes[d.Status] || !d.Due.After(*t.Due) {
				continue
			}
			later = append(later, fmt.Sprintf("%s (due %s)", d.ID, FormatDue(d, loc)))
		}
		if len(later) == 0 {
			continue
		}
		out = append(out, Problem{SevWarn, "due-inversion", t.ID, fmt.Sprintf(
			"due %s, but it waits on %s, promised later — a task cannot close before the dep it waits on: re-date one side (`furrow set %s --due <date>`) or drop the edge (`furrow dep %s <dep> --rm`)",
			FormatDue(t, loc), strings.Join(later, ", "), t.ID, t.ID)})
	}
	sortProblems(out)
	return out
}
