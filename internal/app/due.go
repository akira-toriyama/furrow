package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// dueLayouts are the absolute spellings `--due` accepts, tried in order after
// the relative offset. RFC3339 first (an exact instant, offset included — what
// `furrow show` prints back), then the zoneless wall-clock forms, which are read
// in the OPERATOR's zone: a date typed by a human means their wall clock, and
// the stored stamp is UTC either way.
var dueLayouts = []struct {
	layout string
	zoned  bool
}{
	{time.RFC3339, true},
	{"2006-01-02T15:04:05", false},
	{"2006-01-02T15:04", false},
	{"2006-01-02 15:04:05", false},
	{"2006-01-02 15:04", false},
}

// dueDateLayout is the date-only spelling, handled apart from the list above
// because it does NOT mean midnight — see ParseDue.
const dueDateLayout = "2006-01-02"

// loc is the zone furrow reads a wall-clock date in and draws the "today"
// boundary at: App.Loc, or time.Local when unset. Deliberately NOT the Clock's
// zone — the production Clock is UTC by contract (core.SystemClock), so a day
// boundary taken from it would tell a JST operator on the morning of the 4th
// that a task due that evening is not due today.
func (a *App) loc() *time.Location {
	if a.Loc == nil {
		return time.Local
	}
	return a.Loc
}

// ParseDue binds a `--due` spelling to an instant. now anchors the relative
// offsets; loc is the zone a zoneless spelling is read in. Four shapes:
//
//	2026-08-04                — the END of that day (23:59:59, in loc)
//	2026-08-04T10:30          — that wall-clock minute in loc ("2026-08-04 10:30" too)
//	2026-08-04T10:30:00+09:00 — an exact instant (RFC3339)
//	+1d / +2h / -30m          — a signed offset from now, the snooze spelling
//
// The date-only rule is the load-bearing one: read as midnight, a task promised
// for the 4th would be overdue from 00:01 ON the 4th — the check would fire a
// day early, every time, and the operator would learn to ignore it. The whole
// day is what "due on the 4th" means, so the bound is its last second.
//
// The offset spelling is deliberately the same ±N{m,h,d,w} the `-q` date
// qualifiers take (parseRelativeOffset), so `--due +1d` and `-q due:<=+1d` cannot
// drift apart. It is measured from NOW, not from the task's existing due stamp:
// snoozing an already-overdue task by +1d must land in the future, which a
// due-relative offset would not.
func ParseDue(s string, now time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, core.Validationf("", "--due needs a date (2026-08-04, 2026-08-04T10:30, an RFC3339 instant, or an offset like +1d); to remove one use --clear-due")
	}
	if d, ok := parseRelativeOffset(s); ok {
		return normDue(now.Add(d)), nil
	}
	if t, err := time.Parse(dueDateLayout, s); err == nil {
		// The END of the day, not its start (see the doc comment). 23:59:59 is the
		// last whole second furrow ever stamps — the same convention `ls --until`
		// and the `-q` date bounds already use for a bare day.
		//
		// Built as a WALL-CLOCK 23:59:59, not midnight + 24h - 1s: a DST day is 23
		// or 25 hours long, so the arithmetic form lands at 00:59:59 the next day
		// (spring forward) or 22:59:59 the same day (fall back).
		//
		// And the y/m/d come from parsing in UTC — the LITERAL the operator typed —
		// never from a ParseInLocation of the same string: where local midnight does
		// not exist (Santiago springs forward AT 00:00 on 2026-09-06, as does Havana
		// each March), ParseInLocation normalizes BACKWARD to 23:00 the previous day,
		// so reading Date() off it silently promises the day before and the task is
		// overdue for every hour of the day it was promised for.
		y, mo, d := t.Date()
		return normDue(time.Date(y, mo, d, 23, 59, 59, 0, loc)), nil
	}
	for _, l := range dueLayouts {
		if l.zoned {
			if t, err := time.Parse(l.layout, s); err == nil {
				return normDue(t), nil
			}
			continue
		}
		if t, err := time.ParseInLocation(l.layout, s, loc); err == nil {
			return normDue(t), nil
		}
	}
	return time.Time{}, core.Validationf("", "--due %s is not a date: use YYYY-MM-DD (the whole day), YYYY-MM-DDTHH:MM, an RFC3339 instant, or a signed offset like +1d/+2h", strconv.Quote(s))
}

// normDue puts a parsed instant into the on-disk shape — UTC, whole seconds —
// at the ONE boundary every write path crosses. core.Canonicalize enforces the
// same thing on the way to a shard, but only fsstore canonicalizes on save; a
// memstore-backed caller (every app test, and any future front-end) would
// otherwise read back the zone-carrying stamp it just wrote and the two stores
// would disagree about a field's value.
func normDue(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// parseDue is ParseDue bound to this app's clock and zone — the form every
// write path uses, so a spelling can never mean one thing on `add` and another
// on `set`.
func (a *App) parseDue(s string) (time.Time, error) {
	return ParseDue(s, a.Clock.Now(), a.loc())
}

// resolveDue binds a set's `--due` spelling to ONE instant for the whole call,
// so `set <a> <b> <c> --due +1d` promises all three for the same moment instead
// of drifting a second apart across the loop. nil when the call sets no date
// (including the --clear-due case, which applySet handles first).
func (a *App) resolveDue(o SetOpts) (*time.Time, error) {
	if o.ClearDue || o.Due == nil {
		return nil, nil
	}
	d, err := a.parseDue(*o.Due)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// dueSkipLanes is the lane set where a due date raises nothing: the done lane
// (structural — the promise is settled the moment the task closes) plus the
// configured parked lanes ([due].ignore_lanes, icebox by default). Terminal
// lanes as a CLASS are deliberately not skipped: `waiting` is terminal on the
// shipped config, and a waiting task whose last step is "look at this tomorrow"
// is the case the field exists for.
func (a *App) dueSkipLanes() map[string]bool {
	skip := make(map[string]bool, len(a.Cfg.DueIgnoreLanes)+1)
	for l := range a.Cfg.DueIgnoreLanes {
		skip[l] = true
	}
	skip[a.Cfg.DoneLane] = true
	return skip
}

// DueDisplayState is the classification a RENDERER may show: core.DueStateOf,
// except that a task in a lane where due dates raise nothing (the done lane, the
// configured parked lanes) never reads as overdue or due-today — it still shows
// its date, just without an alarm. Otherwise one binary would make two
// statements about the same task: `ls` calling a task finished a week EARLY
// "overdue" (its lane is done, so lint and brief are silent), and every archived
// task — all of them done-lane — mislabelled the same way.
func (a *App) DueDisplayState(t *core.Task) string {
	if t.Due == nil {
		return core.DueNone
	}
	if a.dueSkipLanes()[t.Status] {
		return core.DueLater
	}
	return core.DueStateOf(t, a.Clock.Now(), a.loc())
}

// DueSummary is what `furrow brief` leads with: the tasks whose promised instant
// has PASSED and the ones whose day is TODAY, most-overdue first. Board-wide as
// to epics — the focus scope must never hide a date — but scoped by repo like
// every other brief section.
type DueSummary struct {
	Overdue []ListItem `json:"overdue"`
	Today   []ListItem `json:"today"`
}

// Empty reports whether there is nothing to say (no section to print).
func (d DueSummary) Empty() bool { return len(d.Overdue) == 0 && len(d.Today) == 0 }

// Total is the count both surfaces quote ("N due").
func (d DueSummary) Total() int { return len(d.Overdue) + len(d.Today) }

// Due returns the arrived due dates under o's scope — the read behind brief's
// leading section and `next`'s one-line hint. The "what has arrived" rule is
// written once, in core.DueStateOf, which `lint` consumes through
// core.DueProblems, so the session-start read and the checker can disagree about
// neither the day boundary nor the skipped lanes.
//
// Scoping matches `revisit`, not `ls`: a repo filter applies to tasks that HAVE
// repos, and an open DRAFT passes it (matchRevisit). Without that carve-out a
// dated draft — "note it on the board, attach it later", the very shape this
// field was asked for — would be invisible in brief on every repo-scoped board
// while lint (which is board-wide) errored on it, i.e. one `furrow brief` would
// print a due section of 1 above a lint ride-along counting 2.
//
// Ordering is by due instant ascending, so the longest-overdue task leads.
func (a *App) Due(o QueryOpts) (DueSummary, error) {
	idx, err := a.load()
	if err != nil {
		return DueSummary{}, err
	}
	doneIDs := a.doneSet(idx)
	now, loc := a.Clock.Now(), a.loc()
	skip := a.dueSkipLanes()
	var sum DueSummary
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Due == nil || skip[t.Status] || !o.matchRevisit(t) {
			continue
		}
		state := core.DueStateOf(t, now, loc)
		if state != core.DueOverdue && state != core.DueToday {
			continue
		}
		actionable, blockedBy := a.factsFor(idx, t, doneIDs)
		it := ListItem{Task: *t, Actionable: actionable, BlockedBy: blockedBy}
		if state == core.DueOverdue {
			sum.Overdue = append(sum.Overdue, it)
		} else {
			sum.Today = append(sum.Today, it)
		}
	}
	byDue := func(items []ListItem) {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Task.Due.Before(*items[j].Task.Due) })
	}
	byDue(sum.Overdue)
	byDue(sum.Today)
	return sum, nil
}
