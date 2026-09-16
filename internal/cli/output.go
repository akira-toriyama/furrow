package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"golang.org/x/term"
)

// JSON goes to stdout ONLY; logs, spinners, and errors go to stderr.
// These helpers are the single funnel for that rule.

// out is stdout; overridable in tests.
var out io.Writer = os.Stdout

// errOut is stderr; overridable in tests. The scope banner and other
// human-facing notices go here so stdout stays pure data (JSON/table).
var errOut io.Writer = os.Stderr

// out and errOut are the ONLY writers this package prints through — never
// cmd.OutOrStdout() / cmd.ErrOrStderr() / os.Stdout / os.Stderr in a command
// body. Three outlets meant a test harness could redirect two and structurally
// miss the third: the clamp note `set --value 9` owes the human reader vanished
// for months while 355 tests stayed green (t-hs4a). The two exceptions are not
// prints: isTTY asks os.Stdout whether it is a terminal, and `edit` hands the
// real stdio to $EDITOR.

// mustJSON marshals deterministically (SetEscapeHTML(false), 2-space indent) so
// CLI JSON output reads in the same byte style core.Marshal* writes a shard in.
func mustJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func printJSON(v any) {
	fmt.Fprintln(out, string(mustJSON(v)))
}

// printNDJSONValue writes one value as a compact JSON line (Encode adds the \n).
func printNDJSONValue(v any) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	fmt.Fprint(out, b.String())
}

// emitList is the one list-shaped emitter: --ndjson streams one view per
// compact line, --json prints the array (nil normalized to [] — an empty
// listing is a healthy result and must never read as null), and human mode
// runs the renderer the caller hands it. Nine copies of this three-way switch
// preceded it, one of them living in cmd_lint.go (t-3tq4).
func emitList[T any](views []T, human func()) {
	switch {
	case flagNDJSON:
		for _, v := range views {
			printNDJSONValue(v)
		}
	case flagJSON:
		if views == nil {
			views = []T{}
		}
		printJSON(views)
	default:
		human()
	}
}

// capNames is the "name a few, count the rest" rule the human lines share:
// the first maxNamed of n items are named and the remainder is a count. It
// returns how many to name and the ", +N more" suffix ("" when none). Three
// renderers carried the constant and the arithmetic (t-3tq4).
func capNames(n int) (named int, more string) {
	const maxNamed = 3
	if n <= maxNamed {
		return n, ""
	}
	return maxNamed, fmt.Sprintf(", +%d more", n-maxNamed)
}

// jsonMode reports whether machine output was requested in either form. It is
// the single predicate a command gates on so --ndjson is honored everywhere
// --json is — not just the list commands. (--ndjson wins when both are set;
// emitObject picks the exact shape.)
func jsonMode() bool { return flagJSON || flagNDJSON }

// emitObject writes a single value as the active machine format: indented under
// --json, compact one-line under --ndjson. It is the single-object twin of
// emitTasks — for commands whose machine payload is one object (a mutation's
// {before,after,changed}, an attach/init/edit result, the apply report, the
// version block). Callers gate on jsonMode() first; a list-shaped command uses
// emitTasks / a per-line loop instead.
func emitObject(v any) {
	if flagNDJSON {
		printNDJSONValue(v)
		return
	}
	printJSON(v)
}

// isTTY reports whether stdout is a terminal, so a path that would otherwise
// hand control to an interactive program prints its output instead when nobody
// is there to drive it.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stateGlyph is the one-character state summary shared by `ls` (flat) and `ls
// --tree`, classified against the BOARD's lane vocabulary (which lane is done,
// which are terminal — both configurable), never a hardcoded lane name:
//
//	★  ready to pick up — in a next lane, every dep done
//	✓  done
//	~  parked in a terminal lane that is not done (icebox, waiting)
//	·  open, but not available: blocked by a dep, or not in a next lane
//
// NOTE the ★ no longer means "exactly what `furrow next` would hand you": next
// ALSO scopes to the active epic, so ★ is a strict superset. Making the glyph
// epic-aware was the alternative and it is worse — a mark whose meaning shifts
// with whichever box happens to be open cannot be read at a glance, and `ls` is
// the board-wide view by design.
func stateGlyph(a *app.App, actionable bool, status string) string {
	switch {
	case actionable:
		return "★"
	case status == a.Cfg.DoneLane:
		return "✓"
	case a.Cfg.IsTerminal(status):
		return "~"
	default:
		return "·"
	}
}

// humanTime renders an EVENT INSTANT for HUMAN output in the viewer's local time
// zone with an explicit offset (e.g. "2026-07-17 12:22 +09:00"), so `show`
// lines up with `git log` instead of reading as a phantom UTC midnight. Storage
// and the --json/--ndjson views stay UTC RFC3339 (mustJSON) — this is
// presentation only, applied inline so the caller's *core.Task is never mutated.
//
// "When did this happen" is what belongs here: created, updated, closed,
// reviewed, a commit time. A due and a repeat anchor are calendar-bound and go
// through calendarTime instead.
func humanTime(t time.Time) string {
	return t.Local().Format(core.TimeLayout)
}

// calendarTime renders a CALENDAR-BOUND stamp — a due, a repeat anchor — in the
// BOARD's calendar (app.Calendar), with the same explicit offset humanTime
// carries, which is what makes the two visibly different calendars rather than
// one silently wrong one.
//
// The board declares that calendar ([due].timezone) precisely so a date does not
// depend on the machine reading it: `--due 2026-09-20` binds the end of the 20th
// THERE, and lint, brief's bands and recur's lattice all read it back there. A
// viewer's-zone rendering of the same instant prints a different DATE east of it
// — "(today)" beside tomorrow's date, an `ls` row a day off from the `lint` line
// about the same task. On a board that declares no calendar this is time.Local,
// exactly as before.
func calendarTime(a *app.App, t time.Time) string {
	return t.In(a.Calendar()).Format(core.TimeLayout)
}

// waitingUntil renders a box's EpicWait for the human rows: the earliest parked
// due in the board's calendar (the same calendarTime `show` prints a task's due
// with) and the member carrying it, so "until when" and "on whose account" are
// one glance.
func waitingUntil(a *app.App, w *app.EpicWait) string {
	return fmt.Sprintf("⏳ waiting until %s (%s)", calendarTime(a, w.Until), w.Task)
}

// dueTag is dueDetail's one-cell form for a table row: "due 2026-08-04 10:30",
// or "overdue …" once the instant has passed. The offset is dropped (the row is
// already wide, and `show` carries the exact stamp); the two spellings share the
// substring "due", so one grep finds every dated row and a longer one finds only
// the late ones.
//
// The date is read in the board's calendar, like the word beside it: a row that
// classified in one zone and printed in another said "due" and a date that the
// same board's lint line did not agree with.
func dueTag(a *app.App, t *core.Task) string {
	if t.Due == nil {
		return ""
	}
	word := "due"
	if a.DueDisplayState(t) == core.DueOverdue {
		word = "overdue"
	}
	return word + " " + t.Due.In(a.Calendar()).Format("2006-01-02 15:04")
}

// repeatTag marks a row whose close MINTS the next occurrence. A fixed word,
// never the rule: the shard stores the compiled RRULE (the operator's spelling
// is not kept) and internal/recur has no RRULE-to-prose direction, so a row
// could only print FREQ=WEEKLY;BYDAY=MO — width spent on a detail `show` already
// carries, where the row needs one bit: closing this is not the end of it.
//
// It rides WIDER than dueTag, deliberately. A date is guaranteed a surface
// (brief's due band is exactly the dates that have arrived, and lint errors on
// them board-wide), so a band may omit it; a rule has no such surface — a
// weekly chore due next month appears only as an ordinary row — and the one
// place it matters is the row a close is launched from. So every human view
// that renders a task AS a task (id + lane + title) tags it: `ls` flat and
// `--tree`, `next`, `revisit`, all three of brief's bands, and `epic show`'s
// members. `search` is the exception — its MATCH column is a snippet, not a
// title cell, and a tag there would read as part of the matched text.
func repeatTag(t *core.Task) string {
	if t.Repeat == "" {
		return ""
	}
	return "repeats"
}

// withTags appends the non-empty row tags, two spaces apart — the one spacing
// rule every human row renderer shares, so the tags cannot drift into different
// gaps on different views.
func withTags(s string, tags ...string) string {
	for _, tag := range tags {
		if tag != "" {
			s += "  " + tag
		}
	}
	return s
}
