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
// was the last occurrence". Created and Due are null exactly when Completed.
type RepeatReport struct {
	Created   *string    `json:"created"` // the successor's id; null when the series ended
	Due       *time.Time `json:"due"`     // the successor's promised instant; null when the series ended
	Skipped   int        `json:"skipped"` // occurrences that elapsed between this task's due and the next
	Completed bool       `json:"completed"`
}

// bodyLinkLine matches a body's leading `[[t-xxxx]]` back-link and nothing else,
// so a successor replaces the previous occurrence's link instead of stacking one
// more on top of it every cycle.
var bodyLinkLine = regexp.MustCompile(`^\s*\[\[[^\]\s]+\]\]\s*$`)

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
func (a *App) planRepeat(idx *core.Index, t *core.Task, lane string, now time.Time) (*RepeatReport, *pendingSuccessor, error) {
	if lane != a.Cfg.DoneLane || t.Repeat == "" {
		return nil, nil, nil
	}
	if t.RepeatAnchor == nil {
		// Only reachable from a shard furrow did not write: every write path
		// binds the two together. Refusing beats inventing an anchor, which
		// would silently re-lattice the whole series on a guess.
		return nil, nil, core.Validationf(t.ID, "task %s carries a repeat rule with no repeat_anchor, so the series has no start to expand from — rebind it with `furrow set %s --repeat <rule> --due <date>`, or drop it with `furrow set %s --clear-repeat`", t.ID, t.ID, t.ID)
	}

	rule := t.Repeat
	// The anchor is expanded in the BOARD's calendar so occurrences keep their
	// wall clock (23:59:59 stays 23:59:59 across a DST boundary).
	anchor := t.RepeatAnchor.In(a.loc())
	anchorUTC := *t.RepeatAnchor

	// Search from the occurrence being SETTLED, not from the wall clock. A bare
	// `--due 2026-09-11` binds 23:59:59 local, so a close at 14:32 that asked for
	// "the first occurrence after now" was handed back TODAY — the very instant
	// just completed — and the series advanced only when the operator was late.
	// A daily chore could never be cleared for the day, and a COUNT-bounded one
	// could never spend its count. Taking the later of the two keeps the late
	// close jumping past the lapsed cycles, and makes an on-time or early close
	// advance exactly one step.
	after := now
	if t.Due != nil && t.Due.After(after) {
		after = *t.Due
	}
	next, ok, err := recur.Next(rule, anchor, after)
	if err != nil {
		return nil, nil, core.Validationf(t.ID, "%v", err)
	}
	if !ok {
		return &RepeatReport{Completed: true}, nil, nil
	}

	// What lapsed while the task sat open: occurrences after the one it was
	// promised for, before the one it is handing on.
	from := anchor
	if t.Due != nil {
		from = t.Due.In(a.loc())
	}
	skipped, err := recur.CountBetween(rule, anchor, from, next)
	if err != nil {
		return nil, nil, core.Validationf(t.ID, "%v", err)
	}

	id, err := a.uniqueID(idx)
	if err != nil {
		return nil, nil, err
	}
	body, err := a.Store.LoadBody(t.ID)
	if err != nil {
		return nil, nil, err
	}

	due := next.UTC()
	// Everything carries over except what the close settles (closed, reviewed),
	// what the rule computes (due), and what belonged to THIS occurrence's run
	// (deps — a satisfied edge is not a promise about the next cycle).
	successor := core.Task{
		ID: id, Title: t.Title, Status: a.Cfg.DefaultLane, Priority: t.Priority,
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
	for _, p := range pending {
		if p == nil {
			continue
		}
		if err := a.saveBody(p.task.ID, p.body); err != nil {
			return err
		}
		idx.Add(p.task)
	}
	return nil
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
func successorBody(body, prevID string) string {
	link := "[[" + prevID + "]]"
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
	line, err := recur.Compile(spec, func(text string) (time.Time, error) {
		return ParseDue(text, a.Clock.Now(), a.loc())
	})
	if err != nil {
		return core.Validationf(t.ID, "%v", err)
	}
	t.Repeat = line
	anchor := *t.Due
	t.RepeatAnchor = &anchor
	return nil
}

// RepeatWarning returns the one thing worth saying at bind time, or "".
//
// A day-of-month past 28 is legal and RFC 5545 answers it by SKIPPING the
// months that lack it — so `monthly on 31` lands 7 times a year. That is a
// defensible thing to ask for, so it is not an error; it is also almost never
// what the operator meant, so it is not silent either.
func RepeatWarning(line string) string {
	day, ok := recur.SkipsMonths(line)
	if !ok {
		return ""
	}
	return fmt.Sprintf("note: day %d does not exist in every month, so the rule skips those months (RFC 5545); `monthly on last` is the rule that always lands", day)
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
