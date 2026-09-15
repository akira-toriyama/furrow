package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/recur"
)

// RepeatReport is what a close says about the series it advanced.
//
// The shape is the SAME whether an occurrence was generated or the series
// ended, because the alternative — omitting the key at the end — would leave a
// machine unable to tell "this task does not repeat" (no key at all) from "this
// was the last occurrence". Created and Due are null exactly when Completed;
// Skipped is reported either way, since a late close is what runs a bounded
// series out.
type RepeatReport struct {
	Created   *string    `json:"created"` // the successor's id; null when the series ended
	Due       *time.Time `json:"due"`     // the successor's promised instant; null when the series ended
	Skipped   int        `json:"skipped"` // occurrences that lapsed between the one this close settled and the close
	Completed bool       `json:"completed"`
}

// bodyLinkLine matches a body's leading `[[t-xxxx]]` back-link and nothing else,
// so a successor replaces the previous occurrence's link instead of stacking one
// more on top of it every cycle.
var bodyLinkLine = regexp.MustCompile(`^previous: \[\[[^\]\s]+\]\]\s*$`)

// repeatOnClose advances the series when a task carrying a recurrence rule is
// about to enter the done lane, and reports what it did. It returns (nil, nil)
// for every other write, which is almost all of them.
//
// It must be called BEFORE the note of this close is appended to the body (the
// successor copies the body as it stood, and a completion note belongs to the
// occurrence that earned it) and before applyLane stamps Closed.
//
// The rule is CONSUMED: the predecessor loses Repeat and RepeatAnchor whatever
// the outcome, and the successor — if there is one — carries them. That is what
// makes closing idempotent. furrow's no-op detection compares a task's own
// shard bytes, so it structurally cannot see that a close already minted a
// successor in another file; a predecessor that kept its rule would mint a
// second one on every reopen-then-close. With the rule held by exactly one task
// at a time, a re-close has nothing to act on.
// It writes NOTHING — not the index, not a body file. core.Index holds tasks BY
// VALUE, so appending the successor can move the backing array and invalidate
// every *core.Task the caller is holding, including the one being closed; and a
// body written here would outlive a later refusal, leaving a file on disk for a
// task that never existed, which only a human could find and remove. The
// successor and its prose therefore come back PENDING, for the caller to flush
// once the whole write is known to succeed.
func (a *App) planRepeat(idx *core.Index, t *core.Task, was, lane string, now time.Time, reserved map[string]bool) (*RepeatReport, *pendingSuccessor, error) {
	// A TRANSITION into the done lane, not the mere fact of ending there. A task
	// that is ALREADY closed cannot be closed again, and `set <closed-id> -s done
	// --repeat X` would otherwise arm a rule and immediately spend it — minting a
	// fresh successor on every invocation, from a task nothing is working on.
	if lane != a.Cfg.DoneLane || was == a.Cfg.DoneLane || t.Repeat == "" {
		return nil, nil, nil
	}
	if a.Cfg.DefaultLane == a.Cfg.DoneLane {
		// bindRepeat refuses this at bind time, but the board's config can change
		// afterwards — and a successor born in the done lane is closed at birth
		// holding a live rule, which kills the series in silence.
		return nil, nil, core.Validationf(t.ID, "this board's default lane (%q) is its done lane, so the next occurrence would be closed at birth — fix [lanes].default, or drop the rule with `furrow set %s --clear-repeat`", a.Cfg.DefaultLane, t.ID)
	}
	if t.Due == nil {
		// Without a due there is nothing to advance FROM: the search would fall
		// back to the wall clock and hand back the occurrence just closed, which
		// is defect #1's failure mode with no signal at all. Only a shard furrow
		// did not write can reach this; `lint` names it as repeat-invalid.
		return nil, nil, core.Validationf(t.ID, "task %s carries a repeat rule with no due, so the occurrence being closed has no date to advance from — rebind it with `furrow set %s --repeat <rule> --due <date>`, or drop it with `furrow set %s --clear-repeat`", t.ID, t.ID, t.ID)
	}
	if t.RepeatAnchor == nil {
		// Only reachable from a shard furrow did not write: every write path
		// binds the two together. Refusing beats inventing an anchor, which
		// would silently re-lattice the whole series on a guess.
		return nil, nil, core.Validationf(t.ID, "task %s carries a repeat rule with no repeat_anchor, so the series has no start to expand from — rebind it with `furrow set %s --repeat <rule> --due <date>`, or drop it with `furrow set %s --clear-repeat`", t.ID, t.ID, t.ID)
	}

	rule := t.Repeat
	// The anchor is expanded in the BOARD's calendar so occurrences keep their
	// wall clock (23:59:59 stays 23:59:59 across a DST boundary). recur takes
	// that calendar as its own argument; the conversion here is for the clock
	// reading below, which decides what this close settles.
	anchor := t.RepeatAnchor.In(a.loc())
	anchorUTC := *t.RepeatAnchor

	// Search from the occurrence being SETTLED, not from the wall clock. A bare
	// `--due 2026-09-11` binds 23:59:59 local, so a close at 14:32 that asked for
	// "the first occurrence after now" was handed back TODAY — the very instant
	// just completed — and the series advanced only when the operator was late.
	// A daily chore could never be cleared for the day, and a COUNT-bounded one
	// could never spend its count.
	//
	// What a close settles depends on what the operator promised. A bare-date
	// series (its anchor sits at 23:59:59 in the board's calendar) promises
	// DAYS, so the close settles the whole local day of the later of now and
	// the due: the day the work was done, or the day it was promised for while
	// that is still ahead. Two failure modes of an instant rule fall out of
	// that. A snooze (`set --due +1d`, the remedy `due-overdue` itself prints)
	// lands the due off-lattice at 17:20, and "the first occurrence after
	// 17:20" was that day's own 23:59:59 point. And a chore closed one
	// afternoon late got a successor due that same night, which the next
	// afternoon's close was late for again — every close late, forever, with a
	// due-overdue ERROR on the board's gate each day. Settling the day heals
	// the chain after one late close. Its cost is deliberate and documented: a
	// close just past midnight settles the NEW day, so closing yesterday's
	// daily at 00:10 consumes today's (re-date the successor with `set <id>
	// --due <today>` when that is not what was meant).
	//
	// A timed series (`--due …T21:00`) promises INSTANTS and is settled as
	// written: a close at 17:00 must not consume tonight's 21:00, so now stays
	// an instant there.
	//
	// Day boundaries are the board's calendar. On a board that declares none,
	// WHICH day a close settles depends on the closing machine's zone — the
	// reason `lint` errors repeat-no-timezone on a shared board.
	var after, lo, hi time.Time // the search start, and the lapse window (strictly between)
	if h, m, s := anchor.Clock(); h == 23 && m == 59 && s == 59 {
		loc := a.loc()
		dueDay, settledDay := t.Due.In(loc), t.Due.In(loc)
		if now.After(*t.Due) {
			settledDay = now.In(loc)
		}
		// The wall-clock construction ParseDue binds with — never next-midnight
		// minus a nanosecond or due+24h-1s, both of which misbehave on the days
		// a zone skips or repeats an hour.
		after = time.Date(settledDay.Year(), settledDay.Month(), settledDay.Day(), 23, 59, 59, 0, loc)
		lo = time.Date(dueDay.Year(), dueDay.Month(), dueDay.Day(), 23, 59, 59, 0, loc)
		hi = time.Date(settledDay.Year(), settledDay.Month(), settledDay.Day(), 0, 0, 0, 0, loc)
	} else {
		after = now
		if t.Due.After(after) {
			after = *t.Due
		}
		lo, hi = *t.Due, after
	}
	// What lapsed while the task sat open: the occurrences strictly between the
	// one this close settles and the day (or instant) of the close — never the
	// settled day's own point, never one still ahead. Reported on completion
	// too, since a late close is exactly what runs a bounded series out.
	skipped, err := recur.CountBetween(rule, anchorUTC, lo, hi, a.loc())
	if err != nil {
		return nil, nil, core.Validationf(t.ID, "%v", err)
	}
	next, ok, err := recur.Next(rule, anchorUTC, after, a.loc())
	if err != nil {
		return nil, nil, core.Validationf(t.ID, "%v", err)
	}
	if !ok {
		return &RepeatReport{Completed: true, Skipped: skipped}, nil, nil
	}

	// `reserved` carries the ids the SAME batch already handed out. uniqueID only
	// consults the index, and a pre-pass plans every successor before any of them
	// is inserted — so without this, two successors in one batch could draw the
	// same id, which both stores then refuse to save.
	id, err := a.uniqueIDExcluding(idx, reserved)
	if err != nil {
		return nil, nil, err
	}
	if reserved != nil {
		reserved[id] = true
	}
	body, err := a.Store.LoadBody(t.ID)
	if err != nil {
		return nil, nil, err
	}

	due := next.UTC()
	// Everything carries over except what the close settles (closed, reviewed),
	// what the rule computes (due), what belonged to THIS occurrence's run
	// (deps — a satisfied edge is not a promise about the next cycle), and its
	// POSITION: priority is relative to a lane, and the successor is born in a
	// different one, so insertSuccessors appends it there exactly as `add` would
	// — a copied number tied an existing task in the default lane and sorted
	// ahead of it.
	successor := core.Task{
		ID: id, Title: t.Title, Status: a.Cfg.DefaultLane,
		Value: cloneIntp(t.Value), Effort: cloneIntp(t.Effort),
		Labels:    append([]string(nil), t.Labels...),
		Repos:     append([]string(nil), t.Repos...),
		Deps:      nil,
		Refs:      append([]string(nil), t.Refs...),
		Checklist: resetChecklist(t.Checklist),
		Created:   now, Updated: now, Body: core.BodyPath(id),
		Epic: t.Epic, Due: &due,
		Repeat: rule, RepeatAnchor: &anchorUTC,
	}
	// The copied prose keeps pointing at the PREDECESSOR's attachments. Copying
	// them per cycle was the obvious answer and the wrong one: it duplicates
	// every blob into the shared board's git history once per occurrence,
	// forever. `archive` leaves behind any asset a live body still references
	// instead, so one copy serves the whole series.
	return &RepeatReport{Created: &id, Due: &due, Skipped: skipped},
		&pendingSuccessor{task: successor, body: successorBody(body, t.ID)}, nil
}

// pendingSuccessor is a generated occurrence that has not been committed yet:
// the task to insert and the prose to write, both held until the caller knows
// the whole write succeeds.
type pendingSuccessor struct {
	task core.Task
	body string
}

// flushSuccessors inserts the generated occurrences and writes their bodies. It
// must run after the LAST thing that can refuse and immediately before the index
// is saved: every *core.Task pointer the caller held is dead by then (the insert
// can move the index's backing array), and nothing is left on disk if an earlier
// step refused.
func (a *App) flushSuccessors(idx *core.Index, pending []*pendingSuccessor) error {
	if err := a.writeSuccessorFiles(pending); err != nil {
		return err
	}
	a.insertSuccessors(idx, pending)
	return nil
}

// writeSuccessorFiles puts the generated prose on disk, all of it in one store
// write. It is the only half that can FAIL; a caller that also appends a
// closing note composes the note into the same batch instead (moveMany,
// DoneNote), since a note is not idempotent and a failure after one had landed
// would leave it on the body and duplicate it on every retry.
func (a *App) writeSuccessorFiles(pending []*pendingSuccessor) error {
	return a.saveBodies(successorBodies(pending))
}

// successorBodies is the id -> body map of the generated occurrences, the
// shape a batch prose write takes; nil entries (a task that does not repeat)
// contribute nothing.
func successorBodies(pending []*pendingSuccessor) map[string]string {
	bodies := map[string]string{}
	for _, p := range pending {
		if p != nil {
			bodies[p.task.ID] = p.body
		}
	}
	return bodies
}

// insertSuccessors adds the generated occurrences to the index. In-memory and
// infallible by construction, so it can run at the last possible moment — after
// every *core.Task pointer the caller held is dead, since inserting can move
// core.Index's backing array.
//
// The priority is assigned HERE, one insert at a time, not in planRepeat: a
// batch plans every successor before any is inserted, so a number computed
// there would be the same for all of them — the tie this exists to avoid.
func (a *App) insertSuccessors(idx *core.Index, pending []*pendingSuccessor) {
	for _, p := range pending {
		if p != nil {
			p.task.Priority = idx.NextPriority(p.task.Status, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
			idx.Add(p.task)
		}
	}
}

// consumeRepeat strips the rule from the task the close is settling. Always
// paired with planRepeat: whether the series continues or ended, the closed
// occurrence stops carrying it, and that is what makes a re-close inert.
func consumeRepeat(t *core.Task) {
	t.Repeat = ""
	t.RepeatAnchor = nil
}

// successorBody is the predecessor's prose with a back-link on top. A leading
// link from the PREVIOUS occurrence is replaced rather than pushed down, so a
// body that has recurred a hundred times still opens with exactly one link.
//
// The line carries a literal `previous: ` marker, and ONLY that shape is
// replaced. A bare leading `[[t-…]]` is something an operator may well have
// written — a pointer to the task this one came out of — and overwriting it
// destroyed their link while leaving the prose that referred to it.
func successorBody(body, prevID string) string {
	link := "previous: [[" + prevID + "]]"
	head, rest, _ := strings.Cut(body, "\n")
	if bodyLinkLine.MatchString(head) {
		return link + "\n" + rest
	}
	if strings.TrimSpace(body) == "" {
		return link + "\n"
	}
	return link + "\n\n" + body
}

// resetChecklist copies the items with every box unchecked: the steps are the
// chore's, the ticks were this occurrence's.
func resetChecklist(items []core.ChecklistItem) []core.ChecklistItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]core.ChecklistItem, len(items))
	for i, it := range items {
		out[i] = core.ChecklistItem{Text: it.Text, Done: false}
	}
	return out
}

// bindRepeat compiles an operator spelling onto a task and anchors it.
//
// The anchor is the task's CURRENT due, which is why every caller applies the
// `--due` of the same write first: `add --due <d> --repeat <r>` and `set --due
// <d> --repeat <r>` must mean the same thing as doing the two in either order.
// A rule with no due is refused rather than anchored on now — a shard whose
// stored anchor was invented is a shard that lies about what the operator asked
// for, and the series would silently re-lattice the first time it was touched.
func (a *App) bindRepeat(t *core.Task, spec string) error {
	if strings.TrimSpace(spec) == "" {
		return core.Validationf(t.ID, "--repeat needs a rule (e.g. `monthly on 15`, `every 2 weeks on mon,thu`); use --clear-repeat to remove one")
	}
	if t.Due == nil {
		return core.Validationf(t.ID, "--repeat needs a --due: the date of the FIRST occurrence is what the rule counts from")
	}
	if a.Cfg.DefaultLane == a.Cfg.DoneLane {
		// The successor is born in the default lane. If that IS the done lane it
		// would be closed at birth holding a live rule — the state `add -s done
		// --repeat` refuses — and nothing would ever fire it.
		return core.Validationf(t.ID, "this board's default lane (%q) is its done lane, so a generated occurrence would be closed at birth — recurrence needs a board whose [lanes].default is open", a.Cfg.DefaultLane)
	}
	line, err := recur.Compile(spec, func(text string) (time.Time, error) {
		return ParseDue(text, a.Clock.Now(), a.loc())
	})
	if err != nil {
		return core.Validationf(t.ID, "%v", err)
	}
	if err := recur.Bindable(line, *t.Due, a.loc()); err != nil {
		return core.Validationf(t.ID, "%v", err)
	}
	t.Repeat = line
	anchor := *t.Due
	t.RepeatAnchor = &anchor
	return nil
}

// RepeatWarnings returns what is worth saying at bind time, and nothing when
// there is nothing to say.
//
// Both notes describe a rule that is legal, RFC-correct, and almost never what
// the operator meant — so neither is an error, and neither is silent:
//
//   - A day of the month past 28 is answered by RFC 5545 with a SKIP of the
//     period that lacks it: `monthly on 31` lands 7 times a year, and a rule
//     landing on February 29 lands once in four. The leap-day one needs the
//     note most — its feedback loop is four years long.
//   - An anchor the rule does not land on stands OUTSIDE the series
//     (recur.OffLattice), so a `for n times` rule hands out n occurrences AFTER
//     it — one task more than the count reads like.
//
// Both can fire on one bind (`monthly on 31` anchored on the 15th), which is
// why this is a slice: they are independent facts about the same rule, and
// picking one would leave the other to be discovered by a close.
func (a *App) RepeatWarnings(t *core.Task) []string {
	if t.Repeat == "" || t.RepeatAnchor == nil {
		return nil
	}
	// The anchor is stored UTC but the series is expanded in the BOARD's
	// calendar, so the day these ask about has to be read there too — otherwise
	// the notes are inverted on any board whose offset crosses a date boundary.
	// Every recur entry point takes the calendar itself; the local anchor here
	// is only for the wording.
	anchor := t.RepeatAnchor.In(a.loc())
	var out []string
	if skip, ok := recur.Skips(t.Repeat, *t.RepeatAnchor, a.loc()); ok {
		out = append(out, skipNote(t.Repeat, anchor, skip, a.loc()))
	}
	if first, ok := recur.OffLattice(t.Repeat, anchor, a.loc()); ok {
		out = append(out, fmt.Sprintf("note: the anchor %s is not a date this rule lands on, so it is one occurrence OUTSIDE the series and `for n times` hands out n MORE after it; the rule's own first date is %s, and anchoring there folds it in",
			anchor.Format(core.TimeLayout), first.Format(core.TimeLayout)))
	}
	return out
}

// skipNote words the skip by the PERIOD that lacks the day, because the remedy
// differs: a month that lacks day 31 is answered by `on last`, a year that
// lacks February 29 is not — `on last` is a monthly spelling, and the rule that
// lands every year is February's last day, which any frequency can name.
func skipNote(line string, anchor time.Time, skip recur.Skip, loc *time.Location) string {
	if skip.Period == recur.Years {
		return fmt.Sprintf("note: February 29 exists only in leap years, so the rule skips the years without one (RFC 5545)%s; anchoring on February 28, or naming February's last day (`BYMONTH=2;BYMONTHDAY=-1`), lands every year", nextOccurrenceClause(line, anchor, loc))
	}
	// The remedy is `on last`, spelled against whatever rule they typed — naming
	// a full `monthly on last` prescribed a different FREQUENCY to anyone who
	// wrote `every 3 months`.
	return fmt.Sprintf("note: day %d does not exist in every month, so the rule skips those months (RFC 5545); anchoring on the last day instead (`… on last`) always lands", skip.Day)
}

// nextOccurrenceClause names the date the leap-day note is about, because a
// four-year gap is the kind of thing an operator has to SEE to disbelieve —
// `every 3 years` from 2028-02-29 next lands in 2040. Empty when the rule
// cannot say (a stored rule the expander refuses): a note missing its date
// still beats no note.
func nextOccurrenceClause(line string, anchor time.Time, loc *time.Location) string {
	next, ok, err := recur.Next(line, anchor, anchor, loc)
	if err != nil || !ok {
		return ""
	}
	return " — the next occurrence is " + next.Format(dueDateLayout)
}

// CompileRepeat exposes the spelling→RRULE compilation to a front-end that
// wants to say something about a rule BEFORE the write (the CLI's
// day-of-month note). It never writes; the authoritative compile still happens
// on the write path, so the two cannot disagree.
func CompileRepeat(a *App, spec string) (string, error) {
	return recur.Compile(spec, func(text string) (time.Time, error) {
		return ParseDue(text, a.Clock.Now(), a.loc())
	})
}
