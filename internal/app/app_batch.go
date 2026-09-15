// The batch mutators — moveMany (move/done over several ids), Set/SetMany and
// applySet — and their shared id resolution and result assembly. One
// all-or-nothing index write per call.

package app

import (
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// moveMany is the batch move: an optional note appended to every moved task's
// body (skipped when empty). Bodies are written only after every id has
// resolved, so a failed batch touches neither lanes nor prose.
func (a *App) moveMany(ids []string, lane, note string) ([]*core.Task, []*RepeatReport, error) {
	reports := map[string]*RepeatReport{}
	var successors []*pendingSuccessor
	reservedIDs := map[string]bool{}
	if !a.Cfg.IsLane(lane) {
		return nil, nil, a.unknownLaneErr("", lane)
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	order, err := a.resolveBatch(idx, ids, "moved")
	if err != nil {
		return nil, nil, err
	}
	// The guard runs over the WHOLE batch before the loop writes anything: a
	// `--note` lands on each body as the loop goes, so a refusal on the third
	// id must not leave the first two annotated.
	for _, id := range order {
		t, _ := idx.Find(id)
		if err := a.guardTask(t); err != nil {
			return nil, nil, err
		}
	}
	now := a.Clock.Now()
	// A PRE-PASS, for the reason the guardTask pre-pass above exists: a `--note`
	// lands on each body as the mutating loop goes, so a refusal on the third id
	// must not leave the first two annotated. Planning writes nothing, so it can
	// all happen before anything does.
	for _, id := range order {
		t, _ := idx.Find(id)
		// The successor copies the body as it stood, and a completion note
		// belongs to the occurrence that earned it — so this reads before the
		// loop below appends one.
		rep, succ, rerr := a.planRepeat(idx, t, t.Status, lane, now, reservedIDs)
		if rerr != nil {
			return nil, nil, rerr
		}
		reports[id] = rep
		successors = append(successors, succ)
	}
	// Every body this close writes — the generated successors' and the note on
	// each closed task — is composed first and lands in ONE store write: a note
	// is not idempotent, so a per-id loop that failed on the third body left the
	// first two annotated, closed nothing, and duplicated them on the retry
	// (t-5n2x). The prose is the only step left that can fail; after it, the
	// index write is all-or-nothing on its own.
	bodies := successorBodies(successors)
	if note != "" {
		for _, id := range order {
			next, err := a.appendedBody(id, note)
			if err != nil {
				return nil, nil, err
			}
			bodies[id] = next
		}
	}
	if err := a.saveBodies(bodies); err != nil {
		return nil, nil, err
	}
	for _, id := range order {
		t, _ := idx.Find(id)
		before, err := core.MarshalTask(t)
		if err != nil {
			return nil, nil, err
		}
		if reports[id] != nil {
			consumeRepeat(t)
		}
		a.applyLane(t, lane)
		// A note is prose: it changed the task's content without touching the
		// shard, so a close that carries one always stamps (stampIfChanged's
		// comparison structurally cannot see a body). Without one, a move to the
		// lane the task is already in must leave the clock alone.
		if note != "" {
			t.Updated = now
			continue
		}
		changed, err := shardChanged(t, before)
		if err != nil {
			return nil, nil, err
		}
		if changed {
			t.Updated = now
		}
	}
	// After the loop: every *core.Task above is dead, so inserting cannot
	// invalidate a pointer still in use (core.Index holds tasks by value). The
	// files are already written; this half cannot fail.
	a.insertSuccessors(idx, successors)
	if err := a.Store.Save(idx); err != nil {
		return nil, nil, err
	}
	out, reps := collectBatch(idx, order, reports)
	return out, reps, nil
}

// MoveMany is the batch lane write plus the series reports a close
// produces — one entry per id, in the same order as the tasks, nil where the
// task carried no recurrence rule. It is the ONE move entry point: the
// single-id and report-less variants that once wrapped it were called by
// tests alone and are gone (t-3gz4).
// note is a POINTER so "no note asked for" and "an empty note asked for" stay
// distinct: an empty `--note ""` is bad usage (exit 2), never a silent plain
// close, and a `note != ""` test would have quietly turned it into one.
func (a *App) MoveMany(ids []string, lane string, note *string) ([]*core.Task, []*RepeatReport, error) {
	text := ""
	if note != nil {
		var err error
		if text, err = normalizeNote("", *note); err != nil {
			return nil, nil, err
		}
	}
	return a.moveMany(ids, lane, text)
}

// DoneMany is MoveMany fixed to the done lane — the close path the CLI
// renders series reports from; a `--note` rides in as note.
func (a *App) DoneMany(ids []string, note *string) ([]*core.Task, []*RepeatReport, error) {
	return a.MoveMany(ids, a.Cfg.DoneLane, note)
}

// SetOpts is the combined-edit payload for Set — the routine triage edits in
// one write. A nil pointer / empty slice / false flag means "leave that facet
// alone"; ClearValue/ClearEffort explicitly unset an estimate (distinct from
// "leave alone"). Priority sets the sparse integer directly; Before/After
// compute it relative to a lane-mate in the DESTINATION lane (so a cross-column
// drop — lane plus position — is one write: `-s <lane> --before <ref>`); the
// three are mutually exclusive. Deps keep their own command.
type SetOpts struct {
	Status      *string  // move to this lane (validated like Move)
	Priority    *int     // set the sparse priority directly
	Before      string   // place immediately before this task (in the destination lane)
	After       string   // place immediately after this task (in the destination lane)
	Value       *int     // set the value estimate
	ClearValue  bool     // unset the value estimate (wins over Value)
	Effort      *int     // set the effort estimate
	ClearEffort bool     // unset the effort estimate (wins over Effort)
	AddLabels   []string // labels to union on
	RmLabels    []string // labels to drop
	// AddRepos/RmRepos attach and detach repos — the repos-field mirror of the
	// label pair, with Rerepo's strict resolution (full owner/repo, or a short
	// name naming exactly one known repo). Removing the last repo leaves a
	// DRAFT (repos == [] is first-class), exactly as `repo <id> --rm` does.
	AddRepos []string
	RmRepos  []string
	// Epic re-files the task into a box: an epic REFERENCE, resolved by Set
	// through ResolveEpic exactly as Add resolves its own — storing a raw ref
	// would file the task under a box that does not exist. A non-nil pointer to ""
	// unfiles it (the deliberate escape, still a lint error while the task is
	// open); nil leaves membership untouched.
	Epic *string
	// Due is the raw `--due` spelling (see ParseDue) — a non-nil pointer sets or
	// re-dates the promise, which is also the snooze (`--due +1d`). nil leaves it
	// untouched; ClearDue removes it and wins, exactly like ClearValue over Value.
	Due      *string
	ClearDue bool
	// Repeat is the raw `--repeat` spelling (see recur.Compile). A non-nil
	// pointer binds or rebinds the rule, anchoring it to the task's due AFTER
	// this same call's `--due` is applied — so `set --due <d> --repeat <r>` is
	// one coherent write. ClearRepeat removes rule and anchor and wins.
	Repeat      *string
	ClearRepeat bool
}

// optFlag pairs a flag as the operator spells it with whether the options
// requested it. An options struct lists its fields as these ONCE, and both
// "is there anything to do?" and the refusal that names every flag read that
// list — the hand-written message forgot --repeat/--clear-repeat (v10) and
// --standing/--pinned (v7) in turn, each time a field was added to the struct
// and to empty() but not to the prose (t-8sgn). TestSetOptsVocabulary pins
// the table's length to the struct's field count.
type optFlag struct {
	name string
	set  bool
}

func anyRequested(fs []optFlag) bool {
	for _, f := range fs {
		if f.set {
			return true
		}
	}
	return false
}

// flagNames renders the vocabulary for a refusal message: `a / b / c`.
func flagNames(fs []optFlag) string {
	names := make([]string, 0, len(fs))
	for _, f := range fs {
		names = append(names, f.name)
	}
	return strings.Join(names, " / ")
}

// requested is SetOpts' optFlag table, one entry per field, in flag order.
func (o SetOpts) requested() []optFlag {
	return []optFlag{
		{"-s", o.Status != nil},
		{"--priority", o.Priority != nil},
		{"--before", o.Before != ""},
		{"--after", o.After != ""},
		{"--value", o.Value != nil},
		{"--clear-value", o.ClearValue},
		{"--effort", o.Effort != nil},
		{"--clear-effort", o.ClearEffort},
		{"--add-label", len(o.AddLabels) > 0},
		{"--rm-label", len(o.RmLabels) > 0},
		{"--add-repo", len(o.AddRepos) > 0},
		{"--rm-repo", len(o.RmRepos) > 0},
		{"-e", o.Epic != nil},
		{"--due", o.Due != nil},
		{"--clear-due", o.ClearDue},
		{"--repeat", o.Repeat != nil},
		{"--clear-repeat", o.ClearRepeat},
	}
}

// empty reports whether o requests no change at all — Set rejects that rather
// than silently touching only the `updated` stamp.
func (o SetOpts) empty() bool { return !anyRequested(o.requested()) }

// Set applies the edits to one task and returns the series report a
// `set -s done` produces when the task carries a recurrence rule (nil for
// every other edit).
func (a *App) Set(id string, o SetOpts) (*core.Task, []core.PriorityChange, *RepeatReport, error) {
	if err := a.validateSetOpts(id, o); err != nil {
		return nil, nil, nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, nil, err
	}
	due, err := a.resolveDue(o)
	if err != nil {
		return nil, nil, nil, err
	}
	var (
		renumbered []core.PriorityChange
		successor  *pendingSuccessor
		report     *RepeatReport
	)
	// applySet does the whole edit against the task mutateInPost hands it (the
	// repo guard's both sides and the stamp are the skeleton's); the successor
	// a `-s done` mints goes in through the post hook — inserting moves the
	// index's backing array, so it must wait until every task pointer is dead.
	saved, err := a.mutateInPost(idx, id, func(*core.Task) error {
		renumbered, successor, report, err = a.applySet(idx, id, o, due, nil)
		return err
	}, func(idx *core.Index) error {
		return a.flushSuccessors(idx, []*pendingSuccessor{successor})
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return saved, renumbered, report, nil
}

// validateSetOpts checks everything about the OPTIONS that does not need the
// index — so Set and SetMany reject a bad request identically, before either
// touches the store.
func (a *App) validateSetOpts(id string, o SetOpts) error {
	if o.Status != nil && !a.Cfg.IsLane(*o.Status) {
		return a.unknownLaneErr(id, *o.Status)
	}
	if o.Epic != nil && *o.Epic != "" {
		if _, err := a.ResolveEpic(*o.Epic); err != nil {
			return err
		}
	}
	if o.Due != nil {
		// Bind the spelling here, not only in applySet: a bulk SetMany must reject
		// a malformed date before it writes ANY of its tasks (the all-or-nothing
		// contract), and a single Set must fail before it loads.
		if _, err := a.parseDue(*o.Due); err != nil {
			return err
		}
	}
	if o.empty() {
		return core.Validationf(id, "set needs at least one change (%s)", flagNames(o.requested()))
	}
	if err := requireNonBlank(id, "--add-label", o.AddLabels); err != nil {
		return err
	}
	if err := requireNonBlank(id, "--rm-label", o.RmLabels); err != nil {
		return err
	}
	if err := requireNonBlank(id, "--add-repo", o.AddRepos); err != nil {
		return err
	}
	if err := requireNonBlank(id, "--rm-repo", o.RmRepos); err != nil {
		return err
	}
	if (o.Priority != nil && (o.Before != "" || o.After != "")) || (o.Before != "" && o.After != "") {
		return core.Validationf(id, "--priority, --before, and --after are mutually exclusive")
	}
	return nil
}

// SetMany applies the same edits to every id in one write and returns the
// per-id series reports, one entry per id in the same order — the batch twin
// of Set, so a bulk `set -s done` owes the same receipt a single one does. A
// close is a close whatever the arity.
func (a *App) SetMany(ids []string, o SetOpts) ([]*core.Task, []*RepeatReport, error) {
	if err := a.validateSetOpts("", o); err != nil {
		return nil, nil, err
	}
	if len(ids) > 1 && (o.Priority != nil || o.Before != "" || o.After != "") {
		return nil, nil, core.Validationf("", "--priority/--before/--after position ONE task; set them in a separate single-id call")
	}
	// One instant for the whole batch (see resolveDue): a bulk snooze promises
	// every id for the same moment.
	due, err := a.resolveDue(o)
	if err != nil {
		return nil, nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	order, err := a.resolveBatch(idx, ids, "set")
	if err != nil {
		return nil, nil, err
	}
	var successors []*pendingSuccessor
	reports := map[string]*RepeatReport{}
	reservedIDs := map[string]bool{}
	for _, id := range order {
		t, _ := idx.Find(id)
		reposBefore := append([]string(nil), t.Repos...)
		_, succ, rep, serr := a.applySet(idx, id, o, due, reservedIDs)
		if serr != nil {
			return nil, nil, serr
		}
		successors = append(successors, succ)
		reports[id] = rep
		if err := a.guardRepos(id, unionRepos(reposBefore, t.Repos), ""); err != nil {
			return nil, nil, err
		}
	}
	// Held to the end: inserting moves core.Index's backing array, and the loop
	// above is holding task pointers into it.
	if err := a.flushSuccessors(idx, successors); err != nil {
		return nil, nil, err
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, nil, err
	}
	out, reps := collectBatch(idx, order, reports)
	return out, reps, nil
}

// applySet mutates one task in an ALREADY-LOADED index and returns any respace
// the relative placement caused. It saves nothing: the caller owns the write, so
// a batch is one Save. id must already resolve.
func (a *App) applySet(idx *core.Index, id string, o SetOpts, due *time.Time, reserved map[string]bool) ([]core.PriorityChange, *pendingSuccessor, *RepeatReport, error) {
	var successor *pendingSuccessor
	var report *RepeatReport
	relRef, relBefore := o.Before, true
	if relRef == "" {
		relRef, relBefore = o.After, false
	}
	t, _ := idx.Find(id)
	before, err := core.MarshalTask(t)
	if err != nil {
		return nil, nil, nil, err
	}
	// Pre-flight the relative placement against the DESTINATION lane before any
	// mutation, so a bad target aborts with nothing half-applied (the plan
	// itself re-checks after the lane move, when id and ref must already
	// agree).
	if relRef != "" {
		if relRef == id {
			return nil, nil, nil, core.Validationf(id, "--before/--after must name a different task")
		}
		rt, ri := idx.Find(relRef)
		if ri < 0 {
			return nil, nil, nil, a.notFoundTask(relRef)
		}
		dest := t.Status
		if o.Status != nil {
			dest = *o.Status
		}
		if rt.Status != dest {
			return nil, nil, nil, core.Validationf(id, "relative target %s is in lane %q, not the destination lane %q — relative order only exists within one lane", relRef, rt.Status, dest)
		}
	}
	nextLabels := t.Labels
	if len(o.AddLabels) > 0 || len(o.RmLabels) > 0 {
		nextLabels = labelDelta(t.Labels, o.AddLabels, o.RmLabels)
		if a.Cfg.LabelsRequired && len(nextLabels) == 0 {
			return nil, nil, nil, core.Validationf(id, "a label is required ([labels].required); this set would remove the last one")
		}
	}
	// The repos pair rides the same set algebra as labels (labelDelta), behind
	// Rerepo's strict resolution — a bare name resolves against the board's
	// repo universe or fails, never a silent new repo. Removing every repo
	// leaves a first-class DRAFT.
	nextRepos := t.Repos
	if len(o.AddRepos) > 0 || len(o.RmRepos) > 0 {
		universe := repoUniverse(idx, a.BoardRepos)
		addR, err := resolveRepoArgs(o.AddRepos, id, universe)
		if err != nil {
			return nil, nil, nil, err
		}
		rmR, err := resolveRepoArgs(o.RmRepos, id, universe)
		if err != nil {
			return nil, nil, nil, err
		}
		nextRepos = labelDelta(t.Repos, addR, rmR)
	}
	laneBefore := t.Status
	if o.Status != nil {
		// The lane lands HERE, before the position block: `--before/--after`
		// resolve against the DESTINATION lane, which is what a cross-column
		// drop means. Only the series advance waits until the end.
		a.applyLane(t, *o.Status)
	}
	var renumbered []core.PriorityChange
	switch {
	case o.Priority != nil:
		t.Priority = *o.Priority
	case relRef != "":
		target, changes, err := idx.PlanRelativePriority(id, relRef, relBefore, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
		if err != nil {
			return nil, nil, nil, err
		}
		t.Priority = target
		for _, c := range changes {
			ct, _ := idx.Find(c.ID)
			ct.Priority = c.To
		}
		renumbered = changes
	}
	switch {
	case o.ClearValue:
		t.Value = nil
	case o.Value != nil:
		t.Value = cloneIntp(o.Value)
	}
	switch {
	case o.ClearEffort:
		t.Effort = nil
	case o.Effort != nil:
		t.Effort = cloneIntp(o.Effort)
	}
	switch {
	case o.ClearDue:
		t.Due = nil
	case due != nil:
		// The instant the CALLER resolved (resolveDue), not a fresh parse: a bulk
		// set must stamp every task with the same promise, and `--due +1d` snoozes
		// from now rather than from a stamp that may already be days in the past.
		d := *due
		t.Due = &d
	}
	switch {
	case o.ClearRepeat:
		t.Repeat = ""
		t.RepeatAnchor = nil
	case o.Repeat != nil:
		if err := a.bindRepeat(t.ID, t, *o.Repeat); err != nil {
			return nil, nil, nil, err
		}
	}
	// A repeating task's anchor is its due: dropping the date would leave the
	// rule with nothing to expand from, so furrow refuses rather than silently
	// ending the series or inventing a start.
	if t.Repeat != "" && t.Due == nil {
		return nil, nil, nil, core.Validationf(id, "task %s repeats, so it must keep a due date — drop the rule first with `furrow set %s --clear-repeat`", id, id)
	}
	if o.Epic != nil {
		// Already validated in validateSetOpts; resolve again to store the ID, not
		// whatever spelling the caller used.
		if *o.Epic == "" {
			t.Epic = ""
		} else {
			id, err := a.ResolveEpic(*o.Epic)
			if err != nil {
				return renumbered, nil, nil, err
			}
			t.Epic = id
		}
	}
	t.Labels = nextLabels
	t.Repos = nextRepos
	// The close runs LAST, so the successor is copied from the task as THIS
	// write leaves it: `set -s done --clear-repeat` ends the series instead of
	// handing it on, `set -s done --repeat <rule>` carries the NEW rule forward
	// instead of binding it onto the task it just closed, and a successor born
	// beside `--add-label`/`-e` inherits the edited values rather than a
	// pre-edit snapshot (which could leave it violating `epic-required`).
	if o.Status != nil {
		r, succ, rerr := a.planRepeat(idx, t, laneBefore, *o.Status, a.Clock.Now(), reserved)
		if rerr != nil {
			return renumbered, nil, nil, rerr
		}
		if r != nil {
			consumeRepeat(t)
		}
		report, successor = r, succ
	}
	// The end-state invariant, checked where the end state is known: a task that
	// is closed cannot carry a live rule. `set -s done --repeat X` is fine — the
	// close above consumed it and handed X to the successor — but `set
	// <closed-id> --repeat X` would arm a rule on a task nothing will ever close
	// again, the state `add -s done --repeat` already refuses, and it forks a
	// series in two when the predecessor's own successor is still running.
	if t.Repeat != "" && t.Status == a.Cfg.DoneLane {
		return renumbered, nil, nil, core.Validationf(id, "task %s is closed, so a repeat rule on it could never fire — reopen it first (`furrow move %s %s`), or drop the rule", id, id, a.Cfg.DefaultLane)
	}
	if err := a.stampIfChanged(t, before); err != nil {
		return renumbered, nil, nil, err
	}
	return renumbered, successor, report, nil
}

// resolveBatch is the batch mutators' id resolution: the ids deduped to their
// first occurrence, in input order, every one present — or the all-or-nothing
// miss (batchMissingErr, verb-worded) with nothing resolved. moveMany and
// SetMany carried it twice, 15 of 16 lines the same (t-gq4v).
func (a *App) resolveBatch(idx *core.Index, ids []string, verb string) ([]string, error) {
	order, missing := []string{}, []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, i := idx.Find(id); i < 0 {
			missing = append(missing, id)
			continue
		}
		order = append(order, id)
	}
	if len(missing) > 0 {
		return nil, a.batchMissingErr(missing, len(order)+len(missing), verb)
	}
	return order, nil
}

// collectBatch is the batch mutators' result: the saved tasks and their series
// reports, one per id in order.
func collectBatch(idx *core.Index, order []string, reports map[string]*RepeatReport) ([]*core.Task, []*RepeatReport) {
	out := make([]*core.Task, 0, len(order))
	reps := make([]*RepeatReport, 0, len(order))
	for _, id := range order {
		saved, _ := idx.Find(id)
		out = append(out, saved)
		reps = append(reps, reports[id])
	}
	return out, reps
}
