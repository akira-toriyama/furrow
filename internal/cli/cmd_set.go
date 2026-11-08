package cli

import (
	"strings"

	"github.com/akira-toriyama/furrow/v10/internal/app"
	"github.com/akira-toriyama/furrow/v10/internal/core"
	"github.com/spf13/cobra"
)

// newSetCmd combines the routine triage edits into one write, so triaging a task
// no longer means running move + reorder + value + effort + label + epic as
// separate commands.
func newSetCmd() *cobra.Command {
	var f setFlags
	cmd := &cobra.Command{
		Use:   "set [<id>...]",
		Short: "Apply several triage edits at once (lane, priority, value, effort, labels, repos, epic, due, repeat, anchor)",
		Long: "Combine the routine triage edits into a single write: move a lane (-s),\n" +
			"position the task (--priority, or --before/--after a task in the destination\n" +
			"lane — so a cross-lane drop is lane + position in ONE write), set or clear\n" +
			"the 1..5 value/effort estimates, add/remove labels, attach/detach repos\n" +
			"(--add-repo/--rm-repo — the same strict resolution `furrow repo --add` uses, so a short name must\n" +
			"match exactly one known repo; removing the last repo leaves a first-class\n" +
			"DRAFT), and file the task under\n" +
			"an epic (-e), and set or clear the due date (--due/--clear-due, where\n" +
			"--due +1d is the snooze, measured from now, and a bare date or datetime is\n" +
			"read in the board's [due].timezone — an RFC3339 instant carries its own\n" +
			"zone, and `ls --json` prints stored dues as UTC instants; on a repeating\n" +
			"task it moves THIS occurrence only — the series anchor never moves — and\n" +
			"--clear-due there is exit 2; --due-shift +7d instead moves the due each\n" +
			"task ALREADY carries by that offset — the reschedule, when the date the\n" +
			"promises were derived from moved: every selected due keeps its distance\n" +
			"to its neighbours, where one shared --due would flatten them onto a\n" +
			"single instant. Units m/h/d/w: days and weeks are calendar days in the\n" +
			"board's zone, so the wall clock survives a DST boundary (a clock the\n" +
			"target day skips lands the gap's width later, never earlier); minutes\n" +
			"and hours are exact. A task with no due is refused, every undated id\n" +
			"named in details.undated and nothing written, and it never rides with\n" +
			"--due/--clear-due; on a repeating task it, too, moves this occurrence\n" +
			"only), and bind or drop a recurrence rule (--repeat,\n" +
			"anchored to the due of the same write — so `--due <date> --repeat <rule>`\n" +
			"in ONE write is how a series is re-anchored, --due alone never is, and\n" +
			"--repeat alone re-anchors the rule to the due the task already carries;\n" +
			"--clear-repeat), and mark the due as FOLLOWING a box's day (--anchor <epic>:\n" +
			"the box must already carry one, `furrow epic set --anchor YYYY-MM-DD`; when\n" +
			"that day moves, this due moves with it by the same calendar-day delta —\n" +
			"the reschedule, one command instead of a --due-shift per guess. The\n" +
			"pointer needs a due (a task with none is refused, every undated id in\n" +
			"details.undated, nothing written — promise one in the same write with\n" +
			"--due) and never sits beside a repeat rule (a series follows its own\n" +
			"repeat_anchor; details.repeating); --clear-due drops it with the due;\n" +
			"--clear-anchor drops it alone, the due stays. `show` prints the due as\n" +
			"D-N against the box's day; `-q anchor:<epic>` / `has:anchor` select the\n" +
			"followers, `has:due no:anchor` the dates the other side or the calendar\n" +
			"fixed) — instead of running move + reorder + value +\n" +
			"effort + label as separate commands. At least one change is required; an unknown lane is\n" +
			"exit 2 with the configured lanes in candidates (like move/add) and an\n" +
			"unresolvable -e epic exits 2 with the known boxes,\n" +
			"a relative target outside the destination lane is exit 2, and under\n" +
			"[labels].required a set that would strip the last label is refused. A\n" +
			"relative placement that has to respace the lane does so in the same write\n" +
			"and reports the neighbors in `renumbered`, exactly like reorder. A --repeat\n" +
			"bind whose rule skips the months (or years) lacking its day, or whose anchor\n" +
			"is not a date the rule lands on, says so: a stderr note, and the same\n" +
			"messages in the envelope's `repeat_warnings` (absent when there are none) —\n" +
			"on the CLOSED task's when the write also closes it, since the rule passes\n" +
			"to the occurrence its `repeat` names. An empty --due '' is exit 2, never a\n" +
			"silent clear (--clear-due is the clear).\n\n" +
			"Several ids apply the SAME edits to all of them in ONE all-or-nothing\n" +
			"write (bulk triage): a miss sets NOTHING and exits 1 with every miss in\n" +
			"details.missing, and --json is ALWAYS an array of envelopes, one per id\n" +
			"(a single id is a one-element array). The position flags\n" +
			"(--priority/--before/--after) apply to ONE task and are exit 2 for two or\n" +
			"more ids.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters — -q/-l/-r, ANDed under the board scope exactly as in ls. The\n" +
			"selection previews until --yes ({dry_run: true, tasks} in JSON — plus the\n" +
			"rows the apply would refuse under `refused`, and with --due-shift each\n" +
			"landing under `moves` {id, from, to}; a flag the apply would refuse on its\n" +
			"own, like a box or rule that does not resolve, is exit 2 before any\n" +
			"preview), applies\n" +
			"as the same single all-or-nothing write, matches-nothing is exit 0 with a\n" +
			"stderr note, and it refuses to combine with ids, --expect-updated, or the\n" +
			"position flags (a position places ONE task) — the full contract is\n" +
			"spelled out in `furrow done --help`. With --due-shift the human preview\n" +
			"prints each row's current due and the one it would get (→); a row with\n" +
			"no due is marked refused, the count line then says 0 will be written\n" +
			"and JSON adds `refused`, since --yes would exit 2. An explicit id list\n" +
			"gets that preview through `-q 'id:<a>,<b>'` — under the board scope,\n" +
			"so add -r '' to see every id the id form would write; there is no\n" +
			"dry-run flag on the id form. A `set` that wrote a due echoes it on the\n" +
			"human line, in the board's calendar.",
		Example: "  furrow set t-k3m9p -s ready --value 4 --effort 2 --add-label bug\n" +
			"  furrow set t-k3m9p -s ready --before t-x1y2z\n" +
			"  furrow set t-k3m9p -e e-v0zdq\n" +
			"  furrow set t-k3m9p t-x1y2z t-9f2qr -s backlog --add-label triaged\n" +
			"  furrow set t-a1b2c t-b2c3d t-c3d4e --add-repo owner/app   # bulk attach, one write\n" +
			"  furrow set t-k3m9p --due 2026-08-04     # promise it for that whole day\n" +
			"  furrow set t-k3m9p --due +1d            # snooze a day from now\n" +
			"  furrow set t-k3m9p --due-shift +7d      # a week later than it is now promised for\n" +
			"  furrow set -q 'id:t-k3m9p,t-x1y2z' -r '' --due-shift -1d   # ids, previewed via -q id: (-r '' = whole board)\n" +
			"  furrow set t-k3m9p --due 2026-11-07 --anchor e-v0zdq   # promised for D-14 of the box's day, and follows it\n" +
			"  furrow set -q 'epic:e-v0zdq has:due no:repeat' --anchor e-v0zdq   # preview: mark the dues that follow the box's day\n" +
			"  furrow ls -q 'has:due no:anchor'        # the dates the other side (or the calendar) fixed\n" +
			"  furrow set t-k3m9p --clear-value --rm-label wip\n" +
			"  furrow set -q 'status:inbox label:bug' -e e-v0zdq        # preview\n" +
			"  furrow set -q 'status:inbox label:bug' -e e-v0zdq --yes  # one write",
		Args: f.sel.args,
		RunE: func(cmd *cobra.Command, args []string) error { return runSet(cmd, args, &f) },
	}
	cmd.Flags().StringVarP(&f.status, "status", "s", "", "move to this lane")
	cmd.Flags().StringVarP(&f.epicRef, "epic", "e", "", "re-file under this epic (id, unique id prefix, or unique title substring; \"\" unfiles)")
	cmd.Flags().IntVarP(&f.priority, "priority", "p", 0, "set the sparse priority directly (a value already held in the lane is disclosed in a note)")
	cmd.Flags().StringVar(&f.before, "before", "", "place immediately before this task (in the destination lane)")
	cmd.Flags().StringVar(&f.after, "after", "", "place immediately after this task (in the destination lane)")
	cmd.Flags().IntVar(&f.value, "value", 0, "set the 1..5 value estimate")
	cmd.Flags().IntVar(&f.effort, "effort", 0, "set the 1..5 effort estimate")
	cmd.Flags().BoolVar(&f.clearValue, "clear-value", false, "clear the value estimate")
	cmd.Flags().BoolVar(&f.clearEffort, "clear-effort", false, "clear the effort estimate")
	cmd.Flags().StringVar(&f.due, "due", "", "set the due date: 2026-08-04 (that whole day), 2026-08-04T10:30, an RFC3339 instant, or an offset like +1d (the snooze)")
	cmd.Flags().BoolVar(&f.clearDue, "clear-due", false, "clear the due date")
	cmd.Flags().StringVar(&f.dueShift, "due-shift", "", "move the due each task already carries by a signed offset: +7d, -1w, +2h (m/h/d/w; days and weeks are calendar days in the board's zone, hours and minutes exact); refused on a task with no due")
	cmd.Flags().StringVar(&f.repeatSpec, "repeat", "", "recur when closed: daily | every 2 weeks on mon,thu | monthly on last fri | ... (the task must carry a due; a raw RRULE line also works, minus a DTSTART)")
	cmd.Flags().BoolVar(&f.clearRepeat, "clear-repeat", false, "stop this task recurring (drops the rule and its anchor)")
	cmd.Flags().StringVar(&f.anchorRef, "anchor", "", "make the due follow this epic's day (id, unique prefix, or unique title substring; the box must carry one): when the day moves, so does the due; needs a due, refused beside --repeat")
	cmd.Flags().BoolVar(&f.clearAnchor, "clear-anchor", false, "stop the due following a box's day (the due stays)")
	// Without this the rebind is silently discarded: applySetSeries's switch puts
	// --clear-repeat first, so `--repeat X --clear-repeat` looked like it took.
	cmd.MarkFlagsMutuallyExclusive("repeat", "clear-repeat")
	cmd.MarkFlagsMutuallyExclusive("anchor", "clear-anchor")
	cmd.MarkFlagsMutuallyExclusive("anchor", "clear-due")
	cmd.MarkFlagsMutuallyExclusive("anchor", "repeat")
	// StringSlice, not StringArray: these edit the SAME field `label --add` does
	// (cmd_mutate.go's newLabelCmd), and comma is how every label surface splits —
	// `-l a,b` is OR on reads. As StringArray, `set --add-label "a,b"` stored the
	// single label "a,b", which NO read filter can match (`-l "a,b"` means a OR b),
	// so the write produced data unreachable by the tool that wrote it.
	cmd.Flags().StringSliceVar(&f.addLabels, "add-label", nil, "add a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&f.rmLabels, "rm-label", nil, "remove a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&f.addRepos, "add-repo", nil, "attach a repo (owner/repo or a unique short name; repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&f.rmRepos, "rm-repo", nil, "detach a repo (same forms; removing the last one leaves a draft; repeatable; comma-separated)")
	cmd.MarkFlagsMutuallyExclusive("value", "clear-value")
	cmd.MarkFlagsMutuallyExclusive("effort", "clear-effort")
	cmd.MarkFlagsMutuallyExclusive("due", "clear-due", "due-shift")
	cmd.MarkFlagsMutuallyExclusive("priority", "before", "after")
	addSelectorFlags(cmd, &f.sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

// setFlags is the storage behind `set`'s flags — one struct, so the command's
// phases (opts, the selection, the apply) are functions with a name and a
// length of their own instead of one closure over twenty locals.
type setFlags struct {
	status      string
	value       int
	effort      int
	clearValue  bool
	clearEffort bool
	addLabels   []string
	rmLabels    []string
	addRepos    []string
	rmRepos     []string
	epicRef     string
	priority    int
	before      string
	after       string
	due         string
	clearDue    bool
	dueShift    string
	repeatSpec  string
	clearRepeat bool
	anchorRef   string
	clearAnchor bool
	sel         writeSelector
}

// opts is the edit the flags describe. A pointer field is set only when its
// flag was passed, so the app can tell "clear" from "untouched".
func (f *setFlags) opts(cmd *cobra.Command) app.SetOpts {
	o := app.SetOpts{
		Before:      f.before,
		After:       f.after,
		AddLabels:   f.addLabels,
		RmLabels:    f.rmLabels,
		AddRepos:    f.addRepos,
		RmRepos:     f.rmRepos,
		ClearValue:  f.clearValue,
		ClearEffort: f.clearEffort,
		ClearDue:    f.clearDue,
		ClearRepeat: f.clearRepeat,
		ClearAnchor: f.clearAnchor,
	}
	changed := cmd.Flags().Changed
	if changed("repeat") {
		// The bind-time note needs the anchor the rule will be bound to, which
		// is the task's due — so it is emitted after the write, off the stored
		// fields, rather than guessed at here.
		o.Repeat = &f.repeatSpec
	}
	if changed("status") {
		o.Status = &f.status
	}
	if changed("priority") {
		p := f.priority
		o.Priority = &p
	}
	if changed("value") {
		v := f.value
		o.Value = &v
	}
	if changed("effort") {
		e := f.effort
		o.Effort = &e
	}
	if changed("epic") {
		o.Epic = &f.epicRef
	}
	if changed("due") {
		o.Due = &f.due
	}
	if changed("due-shift") {
		o.DueShift = &f.dueShift
	}
	if changed("anchor") {
		o.Anchor = &f.anchorRef
	}
	return o
}

// setHasEdit is the app's own at-least-one-change rule, asked before a
// selection is resolved so the preview never shows a write the apply would
// refuse for carrying nothing.
func setHasEdit(o app.SetOpts) bool {
	return o.Status != nil || o.Value != nil || o.Effort != nil || o.Epic != nil || o.Due != nil ||
		o.DueShift != nil || o.Anchor != nil || o.ClearAnchor ||
		o.ClearValue || o.ClearEffort || o.ClearDue || o.ClearRepeat || o.Repeat != nil ||
		len(o.AddLabels) > 0 || len(o.RmLabels) > 0 ||
		len(o.AddRepos) > 0 || len(o.RmRepos) > 0
}

// runSet is `set`'s RunE: vet the flags, turn a selection into ids (or stop
// at its preview), then apply the one edit to every id in a single write.
func runSet(cmd *cobra.Command, args []string, f *setFlags) error {
	a, err := openApp()
	if err != nil {
		return err
	}
	o := f.opts(cmd)
	// subject names the task in a flag-validation error; a -q/-l/-r
	// selection has no single task to blame, so it stays "".
	subject := ""
	if len(args) > 0 {
		subject = args[0]
	}
	// An empty --due is exit 2, never a silent clear: a caller building
	// `--due "$WHEN"` with an unset $WHEN has a bug, and --clear-due already
	// spells the clear. Checked before the generic guard so the message can
	// name the flag that DOES mean "remove it".
	if err := emptyFlagRemedyErr(cmd, subject,
		flagRemedy{"due", "pass a date, or use --clear-due to remove it"},
		flagRemedy{"due-shift", "pass a signed offset like +7d"},
		flagRemedy{"anchor", "pass the epic whose day the due follows, or use --clear-anchor to remove it"},
	); err != nil {
		return err
	}
	if err := emptyFlagErr(cmd, subject, "before", "after", "add-label", "rm-label", "add-repo", "rm-repo", "status"); err != nil {
		return err
	}
	if err := f.sel.guard(cmd, args); err != nil {
		return err
	}
	if f.sel.active(cmd) {
		ids, answered, err := setSelect(cmd, a, &f.sel, o)
		if err != nil || answered {
			return err
		}
		args = ids
	}
	return setApply(cmd, a, args, o)
}

// setSelect turns `set`'s -q/-l/-r selection into the ids to write: the
// flag-only refusals first (so the preview never shows a write --yes would
// refuse), then the preview unless --yes. answered is true when the call has
// already said everything there is to say — a preview printed, or an empty
// selection disclosed — and nothing is to be written.
func setSelect(cmd *cobra.Command, a *app.App, sel *writeSelector, o app.SetOpts) (ids []string, answered bool, err error) {
	if cmd.Flags().Changed("priority") || cmd.Flags().Changed("before") || cmd.Flags().Changed("after") {
		return nil, false, core.Validationf("", "the position flags place ONE task; they cannot ride a -q/-l/-r selection")
	}
	// Vet what CAN be vetted before resolving, so the preview never
	// shows a write the apply would refuse: an edit must exist (the
	// app's own at-least-one-change rule), and a -s lane must be real.
	if !setHasEdit(o) {
		return nil, false, core.Validationf("", "a selection needs at least one edit flag (-s, --value, --add-label, -e, --due, …) to apply")
	}
	// Everything the apply judges WITHOUT the index — the lane, the
	// epic and anchor refs, the due/shift/repeat spellings, the flag
	// combinations — is judged here, so the preview never shows a
	// write that --yes would refuse for its flags alone (a `-e
	// nosuchbox` used to preview "would set N" at exit 0).
	if err := a.VetSet(o); err != nil {
		return nil, false, err
	}
	tail := setPreviewTail(a, o)
	tasks, err := sel.resolve(cmd, a)
	if err != nil {
		return nil, false, err
	}
	if !sel.yes {
		emitSelectPreview(a, "set", tasks, tail)
		return nil, true, nil
	}
	if len(tasks) == 0 {
		emitEmptySelection()
		return nil, true, nil
	}
	return taskIDs(tasks), false, nil
}

// setPreviewTail composes the per-row refusals a selection preview shows, one
// tail per flag that has them; several flags compose, each row carrying every
// tag and refused by any.
func setPreviewTail(a *app.App, o app.SetOpts) *previewTail {
	var tails []*previewTail
	if o.DueShift != nil {
		tails = append(tails, &previewTail{
			tag:     func(t *core.Task) string { return shiftPreviewTag(a, t, *o.DueShift) },
			refused: func(t *core.Task) bool { return !strings.HasPrefix(shiftPreviewTag(a, t, *o.DueShift), "→") },
			move:    func(t *core.Task) (string, string, bool) { return shiftPreviewMove(a, t, *o.DueShift) },
			key:     "refused",
			why:     "cannot be shifted",
			remedy:  "narrow the selection to the rows that can be shifted (-q has:due) — --yes would exit 2",
		})
	}
	if o.Anchor != nil {
		// The pointer's two invariants, so the preview marks the rows
		// --yes would refuse (undated unless --due rides along,
		// repeating unless --clear-repeat does) instead of promising
		// them; the epic itself was resolved by VetSet.
		tails = append(tails, &previewTail{
			tag:     func(t *core.Task) string { return anchorPreviewTag(t, o) },
			refused: func(t *core.Task) bool { return anchorPreviewTag(t, o) != "" },
			key:     "refused",
			why:     "cannot follow an anchor",
			remedy:  "narrow the selection to the rows that can follow (-q 'has:due no:repeat') — --yes would exit 2",
		})
	}
	if o.Repeat != nil {
		// The rule's two invariants from the row's side: it counts from
		// a due, and it cannot sit on a follower.
		tails = append(tails, &previewTail{
			tag:     func(t *core.Task) string { return repeatPreviewTag(t, o) },
			refused: func(t *core.Task) bool { return repeatPreviewTag(t, o) != "" },
			key:     "refused",
			why:     "cannot take the rule",
			remedy:  "narrow the selection to the rows that can repeat (-q 'has:due no:anchor') — --yes would exit 2",
		})
	}
	if o.ClearDue {
		// A rule counts from the due, so a repeating row keeps it (the
		// apply refuses unless the same write drops the rule); the
		// table test found this one — the preview promised the write.
		tails = append(tails, &previewTail{
			tag:     func(t *core.Task) string { return clearDuePreviewTag(t, o) },
			refused: func(t *core.Task) bool { return clearDuePreviewTag(t, o) != "" },
			key:     "refused",
			why:     "cannot lose the due",
			remedy:  "narrow the selection to the rows that do not repeat (-q no:repeat) or drop the rule in the same write (--clear-repeat) — --yes would exit 2",
		})
	}
	return composeTails(tails...)
}

// setApply writes o to every id in one all-or-nothing write and renders the
// envelopes. One path whatever the arity: a single id still emits a
// one-element ARRAY (the always-array rule — `set <id>...` has array
// cardinality by signature). Only the respace receipt is arity-specific,
// because App.Set alone reports one (a several-id set carries no position).
func setApply(cmd *cobra.Command, a *app.App, ids []string, o app.SetOpts) error {
	// The clamped disclosure is per ENVELOPE, whatever the arity: the
	// batch arm used to route through the annotation-free
	// emitMutationMany, so `set <id> <id> --value 9` wrote 5 twice with
	// no stderr note and no clamped key — breaking the README's "an
	// explicit arg is never silently rounded". The note prints once
	// (the clamp is the same for every id); the envelopes each carry it.
	warnedClamp := false
	clampExtra := func(after *core.Task) map[string]any {
		if !warnedClamp {
			warnClamp("value", o.Value, after.Value)
			warnClamp("effort", o.Effort, after.Effort)
			warnedClamp = true
		}
		clamped := map[string]any{}
		if e := clampEntry(o.Value, after.Value); e != nil {
			clamped["value"] = e
		}
		if e := clampEntry(o.Effort, after.Effort); e != nil {
			clamped["effort"] = e
		}
		if len(clamped) == 0 {
			return nil
		}
		return map[string]any{"clamped": clamped}
	}
	// The due a write bound is echoed on the human line: the one field
	// whose stored value the caller could not have typed (a snooze is
	// from now, a shift from a stamp they may not have read).
	var dueEcho func(*core.Task) string
	if o.Due != nil || o.DueShift != nil {
		dueEcho = func(t *core.Task) string { return dueTag(a, t) }
	}
	var renumbered []core.PriorityChange
	rs, closed := newSeriesReports(), []*core.Task(nil)
	mutate := func() ([]*core.Task, error) {
		var ts []*core.Task
		var reps []*app.RepeatReport
		if len(ids) == 1 {
			t, ch, rep, err := a.Set(ids[0], o)
			if err != nil {
				return nil, err
			}
			renumbered = ch
			ts, reps = []*core.Task{t}, []*app.RepeatReport{rep}
		} else {
			var err error
			if ts, reps, err = a.SetMany(ids, o); err != nil {
				return nil, err
			}
		}
		rs.collect(ts, reps)
		if cmd.Flags().Changed("repeat") {
			rs.bind(a, ts)
		}
		closed = ts
		return ts, nil
	}
	annotate := func(after *core.Task) map[string]any {
		extra := mergeExtra(clampExtra(after), respaceExtra(renumbered, after.Status))
		return mergeExtra(extra, rs.annotate(after))
	}
	if err := emitMutationManyTagged(cmd, a, "set", ids, mutate, annotate, dueEcho); err != nil {
		return err
	}
	// `set -s done` closes like `done` does, so it owes the same receipt:
	// without it the occurrence it just minted is invisible until a later
	// read, and a machine could not tell it from a task that never repeated.
	rs.noteBound(closed)
	rs.print(a, closed)
	return nil
}
