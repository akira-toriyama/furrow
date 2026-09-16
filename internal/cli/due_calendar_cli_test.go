package cli

import (
	"strings"
	"testing"
)

// The board promises a date in ITS calendar; the viewer sits a zone east of it.
// Every calendar-bound stamp below is the same instant — 2030-09-20T14:59:59Z —
// which reads as the 20th in Asia/Tokyo and the 21st at +12:00, so any
// rendering that slipped back to the viewer's zone shows up as a wrong DATE and
// not merely a wrong offset.
const (
	boardTZ       = "Asia/Tokyo"
	dueSpelling   = "2030-09-20"
	dueInCalendar = "2030-09-20 23:59 +09:00"
	dueInViewer   = "2030-09-21 02:59 +12:00"
)

func initCalendarBoard(t *testing.T) {
	t.Helper()
	pinLocalTZ(t, 12*3600) // the viewer, +12:00
	initStore(t)
	if out, code := run(t, "config", "set", "due.timezone", boardTZ); code != 0 {
		t.Fatalf("config set due.timezone exit = %d:\n%s", code, out)
	}
}

// TestDueRendersInBoardCalendar pins the half of t-1x7s that [due].timezone
// took back: a due is a promise the BOARD made in its calendar, so it renders
// there. Rendered in the viewer's zone it printed a different date from the one
// lint and brief classify — `show` said "(today)" beside tomorrow.
func TestDueRendersInBoardCalendar(t *testing.T) {
	initCalendarBoard(t)
	id := addTask(t, "chore", "--due", dueSpelling)

	out, code := run(t, "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "due:      "+dueInCalendar) {
		t.Errorf("show must render the due in the board's calendar (%s):\n%s", dueInCalendar, out)
	}
	if strings.Contains(out, dueInViewer) {
		t.Errorf("show rendered the due in the viewer's zone (%s):\n%s", dueInViewer, out)
	}
	// The event instants are NOT calendar-bound: they stay where git log is.
	if !strings.Contains(out, "+12:00") {
		t.Errorf("created/updated must stay in the viewer's zone (+12:00):\n%s", out)
	}

	row, code := run(t, "ls")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, row)
	}
	if !strings.Contains(row, dueSpelling+" 23:59") {
		t.Errorf("the ls row must carry the board's date (%s 23:59):\n%s", dueSpelling, row)
	}
}

// TestRepeatStampsRenderInBoardCalendar covers the two stamps a series prints:
// the anchor `show` names, and the next due a close reports. Both are lattice
// points in the board's calendar — the zone recur expands them in — so a
// viewer's-zone rendering would disagree with the very rule that produced them.
func TestRepeatStampsRenderInBoardCalendar(t *testing.T) {
	initCalendarBoard(t)
	id := addTask(t, "watering", "--due", dueSpelling, "--repeat", "weekly")

	out, code := run(t, "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(since "+dueInCalendar+")") {
		t.Errorf("the repeat anchor must render in the board's calendar (%s):\n%s", dueInCalendar, out)
	}

	out, code = run(t, "done", id)
	if code != 0 {
		t.Fatalf("done exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "repeat: next due 2030-09-27 23:59 +09:00") {
		t.Errorf("the series line must name the next occurrence in the board's calendar:\n%s", out)
	}
}
