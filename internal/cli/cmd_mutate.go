package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// emitMutation runs a single-task edit on id and reports it. In machine mode
// (--json or --ndjson) it snapshots the task before the change and prints
// {before, after, changed}, so an agent sees the effect inline without a
// follow-up `show`. The pre-fetch is skipped (and harmless) in human mode; the
// mutate closure is the authoritative source of any not-found / validation error.
func emitMutation(a *app.App, verb, id string, mutate func() (*core.Task, error)) error {
	return emitMutationWith(a, verb, id, mutate, nil)
}

// emitMutationWith is emitMutation plus an optional `annotate`: given the
// resulting task it returns extra top-level fields to merge into the --json
// {before,after,changed} envelope (and may write a human note to stderr). Used
// by value/effort/set to surface a `clamped` estimate.
func emitMutationWith(a *app.App, verb, id string, mutate func() (*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	var before *core.Task
	if jsonMode() {
		if b, _, err := a.Get(id); err == nil {
			before = b
		}
	}
	after, err := mutate()
	if err != nil {
		return err
	}
	var extra map[string]any
	if annotate != nil {
		extra = annotate(after)
	}
	printMutation(verb, before, after, extra)
	return nil
}

// emitMutationMany is emitMutation for a multi-id batch: one {before,after,
// changed} envelope per task, in the batch's (deduped) input order — --json an
// array (a single id keeps the classic object via each command's len==1 path,
// the show arity convention), --ndjson one envelope per line, human mode one
// verb line per task. Befores come from one batch read; a miss there is
// harmless because the mutate closure is the authority and fails the whole
// batch before anything is printed.
func emitMutationMany(a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error)) error {
	return emitMutationManyWith(a, verb, ids, mutate, nil)
}

// emitMutationManyWith is emitMutationMany plus optional extra top-level
// fields merged into EVERY task's envelope — the batch twin of
// emitMutationWith's annotate (done --note surfaces `appended` on each).
func emitMutationManyWith(a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error), extra map[string]any) error {
	befores := map[string]*core.Task{}
	if jsonMode() {
		if items, _, err := a.GetBatch(ids, false); err == nil {
			for i := range items {
				t := items[i].Task
				befores[t.ID] = &t
			}
		}
	}
	after, err := mutate()
	if err != nil {
		return err
	}
	if jsonMode() {
		envs := make([]any, 0, len(after))
		for _, t := range after {
			env := map[string]any{
				"before":  befores[t.ID],
				"after":   t,
				"changed": changedFields(befores[t.ID], t),
			}
			for k, v := range extra {
				env[k] = v
			}
			envs = append(envs, env)
		}
		if flagNDJSON {
			for _, e := range envs {
				printNDJSONValue(e)
			}
			return nil
		}
		printJSON(envs)
		return nil
	}
	for _, t := range after {
		fmt.Fprintf(out, "%s %s  %s\n", verb, t.ID, t.Title)
	}
	return nil
}

// readTextArg resolves a free-text argument that may be "-" — the shared
// `-`=stdin convention across `add --body`, `note <text>`, and `done --note`,
// so `-` never means one thing in one command and a literal dash in another.
// A value other than "-" is returned verbatim (including ""); "-" reads ALL of
// stdin and returns it unmodified (callers trim for display as needed).
func readTextArg(cmd *cobra.Command, s string) (string, error) {
	if s != "-" {
		return s, nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", core.Internalf("", "read stdin: %v", err)
	}
	return string(data), nil
}

func newDoneCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "done <id>...",
		Short: "Move tasks into the done lane (stamps closed)",
		Long: "Close one or more tasks in a single index write, all-or-nothing: a batch\n" +
			"with an unknown id closes NOTHING and exits 1 with every miss in\n" +
			"details.missing (the show batch shape). With --json, one id keeps the\n" +
			"classic {before,after,changed} object and ≥2 ids emit an array of them;\n" +
			"--ndjson streams one envelope per line at any arity.\n\n" +
			"--note \"<text>\" records the closing word in the same command: the text is\n" +
			"appended to EVERY closed task's body as a new paragraph (the note command's\n" +
			"contract — updated advances, nothing is deduped) and the envelope gains the\n" +
			"same `appended` key. Pass `-` to read the note from stdin; an empty note is\n" +
			"exit 2, never a silent plain close.",
		Example: "  furrow done t-k3m9p\n" +
			"  furrow done t-k3m9p --note \"→ continued in t-x7q2\"\n" +
			"  furrow done t-k3m9p t-x7q2 t-9d4n   # triage sweep, one write",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("note") {
				if len(args) == 1 {
					return emitMutation(a, "done", args[0], func() (*core.Task, error) { return a.Done(args[0]) })
				}
				return emitMutationMany(a, "done", args, func() ([]*core.Task, error) { return a.DoneMany(args) })
			}
			text, terr := readTextArg(cmd, note)
			if terr != nil {
				return terr
			}
			// `changed` tracks metadata only, so surface the note's effect the
			// way the note command does.
			appended := map[string]any{"appended": strings.TrimRight(text, "\n")}
			if len(args) == 1 {
				return emitMutationWith(a, "done", args[0],
					func() (*core.Task, error) { return a.DoneNote(args[0], text) },
					func(after *core.Task) map[string]any { return appended })
			}
			return emitMutationManyWith(a, "done", args,
				func() ([]*core.Task, error) { return a.DoneManyNote(args, text) }, appended)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "append this closing note to each task's body (`-` reads stdin)")
	return cmd
}

func newMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <id>... <lane>",
		Short: "Move tasks to a lane",
		Long: "Move one or more tasks to <lane> (the LAST argument) in a single index\n" +
			"write, all-or-nothing: a batch with an unknown id moves NOTHING and exits 1\n" +
			"with every miss in details.missing; an unknown lane is exit 2 with the\n" +
			"configured lanes in candidates. With --json, one id keeps the classic\n" +
			"{before,after,changed} object and ≥2 ids emit an array of them; --ndjson\n" +
			"streams one envelope per line at any arity.",
		Example: "  furrow move t-k3m9p in-progress\n" +
			"  furrow move t-k3m9p t-x7q2 backlog   # triage sweep, one write",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			ids, lane := args[:len(args)-1], args[len(args)-1]
			if len(ids) == 1 {
				return emitMutation(a, "moved", ids[0], func() (*core.Task, error) { return a.Move(ids[0], lane) })
			}
			return emitMutationMany(a, "moved", ids, func() ([]*core.Task, error) { return a.MoveMany(ids, lane) })
		},
	}
}

func newNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "note <id> <text>",
		Short: "Append a paragraph to a task's body and advance its updated time",
		Long: "Append <text> as a new paragraph to bodies/<id>.md AND stamp the task's\n" +
			"`updated` time, in one command — the in-band way to record progress, stop-points,\n" +
			"and next steps across sessions. Unlike hand-editing the file (what `furrow\n" +
			"edit` hands an agent), it keeps `updated` honest, so `furrow lint`'s\n" +
			"reconcile-gap check does not misfire on a task whose progress lives only in\n" +
			"its body. Pass `-` as <text> to read the note from stdin (for multi-line or\n" +
			"long notes).",
		Example: "  furrow note t-k3m9p \"検証完了。次: アダプタ選定。\"\n" +
			"  git log -1 --format=%B | furrow note t-k3m9p -",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			text, terr := readTextArg(cmd, args[1])
			if terr != nil {
				return terr
			}
			return emitMutationWith(a, "noted", args[0],
				func() (*core.Task, error) { return a.AddNote(args[0], text) },
				func(after *core.Task) map[string]any {
					// `changed` tracks metadata fields only, so a note (body +
					// updated) would show changed:[] — surface the effect instead.
					return map[string]any{"appended": strings.TrimRight(text, "\n")}
				})
		},
	}
}

func newReorderCmd() *cobra.Command {
	var before, after string
	cmd := &cobra.Command{
		Use:   "reorder <id> [<priority>]",
		Short: "Set a task's priority — absolute, or relative with --before/--after",
		Long: "Order a task within its lane. With an absolute <priority>, set the sparse\n" +
			"integer directly (lower = higher up). With --before/--after <id>, compute it:\n" +
			"the task is slotted immediately before/after that task in its lane (both must\n" +
			"share a lane — relative order across lanes is meaningless). When the sparse\n" +
			"gap next to the target is exhausted, the whole lane is respaced in the same\n" +
			"single write (all-or-nothing); --json then adds a `renumbered` array with the\n" +
			"neighbors' {id, from, to} moves, and a note names the count on stderr.",
		Example: "  furrow reorder t-k3m9p 90\n" +
			"  furrow reorder t-k3m9p --before t-x1y2z\n" +
			"  furrow reorder t-k3m9p --after t-x1y2z",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id := args[0]
			ref, isBefore := before, true
			if ref == "" {
				ref, isBefore = after, false
			}
			switch {
			case len(args) == 2 && ref != "":
				return core.Validationf(id, "give an absolute <priority> or --before/--after, not both")
			case len(args) == 1 && ref == "":
				return core.Validationf(id, "provide a <priority>, or --before/--after <id>")
			case ref != "":
				var changes []core.PriorityChange
				return emitMutationWith(a, "reordered", id,
					func() (*core.Task, error) {
						t, ch, err := a.ReorderRelative(id, ref, isBefore)
						changes = ch
						return t, err
					},
					func(t *core.Task) map[string]any { return respaceExtra(changes, t.Status) })
			default:
				prio, err := atoiArg("priority", args[1])
				if err != nil {
					return err
				}
				return emitMutation(a, "reordered", id, func() (*core.Task, error) { return a.Reorder(id, prio) })
			}
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "place immediately before this task (same lane)")
	cmd.Flags().StringVar(&after, "after", "", "place immediately after this task (same lane)")
	cmd.MarkFlagsMutuallyExclusive("before", "after")
	return cmd
}

// respaceExtra reports a relative move's lane respace: a stderr note plus the
// envelope's `renumbered` key. Nil when nothing else moved — the key must not
// appear on a plain midpoint insert. Shared by reorder and set so the two
// relative paths can never diverge on the report.
func respaceExtra(changes []core.PriorityChange, lane string) map[string]any {
	if len(changes) == 0 {
		return nil
	}
	fmt.Fprintf(errOut, "note: gap exhausted — respaced %d other task(s) in lane %q\n", len(changes), lane)
	return map[string]any{"renumbered": changes}
}

// newEstimateCmd builds the shared `value`/`effort` setter: `furrow <name> <id>
// <1-5>` records a coarse estimate (clamped into 1..5), `--clear` unsets it.
// value and effort together drive ROI = value/effort for picking the next task.
func newEstimateCmd(name string, set func(*app.App, string, *int) (*core.Task, error), get func(*core.Task) *int) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   name + " <id> <1-5>",
		Short: "Set a task's " + name + " estimate (coarse 1..5), or clear it with --clear",
		Long: "Record a coarse 1..5 " + name + " estimate on a task; out-of-range scores are\n" +
			"clamped into 1..5. With --clear, remove the estimate (back to unset, so intake\n" +
			"stays frictionless). value and effort together derive ROI = value/effort, the\n" +
			"signal for picking the next task — sort with: furrow ls --json | jq 'sort_by(.value/.effort)'.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id := args[0]
			var v *int
			switch {
			case clear:
				if len(args) != 1 {
					return core.Validationf(id, "--clear takes no score argument")
				}
			default:
				if len(args) != 2 {
					return core.Validationf(id, "provide a 1-5 score, or --clear to unset")
				}
				n, err := atoiArg(name, args[1])
				if err != nil {
					return err
				}
				v = &n
			}
			return emitMutationWith(a, name, id,
				func() (*core.Task, error) { return set(a, id, v) },
				func(after *core.Task) map[string]any {
					// An out-of-range score is silently clamped to 1..5 on write;
					// signal it (stderr note + a `clamped` envelope key) so an agent
					// that recorded 9 knows it was stored as 5.
					stored := get(after)
					warnClamp(name, v, stored)
					if e := clampEntry(v, stored); e != nil {
						return map[string]any{"clamped": map[string]any{name: e}}
					}
					return nil
				})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the estimate (back to unset)")
	return cmd
}

func newValueCmd() *cobra.Command {
	return newEstimateCmd("value",
		func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetValue(id, v) },
		func(t *core.Task) *int { return t.Value })
}

func newEffortCmd() *cobra.Command {
	return newEstimateCmd("effort",
		func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetEffort(id, v) },
		func(t *core.Task) *int { return t.Effort })
}

func newCheckCmd() *cobra.Command {
	var (
		adds   []string
		off    bool
		rm     bool
		reword string
	)
	cmd := &cobra.Command{
		Use:   "check <id> [item-index]",
		Short: "Toggle, add, remove, or reword a checklist item",
		Long: "Edit a task's checklist. With no mode flag, mark the item at the given\n" +
			"zero-based index done (--off unchecks). --add appends one or more items\n" +
			"(repeatable, text verbatim). --rm deletes the item at the index. --reword\n" +
			"replaces the text of the item at the index (keeping its done state). The\n" +
			"mode flags are mutually exclusive; an out-of-range index is exit 2.",
		Example: "  furrow check t-k3m9p 0            # mark item 0 done\n" +
			"  furrow check t-k3m9p 0 --off     # uncheck item 0\n" +
			"  furrow check t-k3m9p --add \"write tests\" --add \"update docs\"\n" +
			"  furrow check t-k3m9p 1 --rm      # delete item 1\n" +
			"  furrow check t-k3m9p 1 --reword \"revised step\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// An explicitly-passed empty value is a validation error, NEVER a silent
			// mode switch: dropping a blank --add (the old behavior) let
			// `check <id> <idx> --add ""` fall through to the toggle path and mark
			// item <idx> done at exit 0 — a value silently switching the command's
			// mode. Same rule as `done --note ""` (exit 2, never a silent plain close).
			if cmd.Flags().Changed("add") {
				for _, s := range adds {
					if strings.TrimSpace(s) == "" {
						return core.Validationf(args[0], "--add needs non-empty text (an empty value is exit 2, never a mode switch)")
					}
				}
			}
			if cmd.Flags().Changed("reword") && strings.TrimSpace(reword) == "" {
				return core.Validationf(args[0], "--reword needs non-empty text")
			}

			// index parses the required zero-based item index for the modes that
			// target an existing item (toggle / --off / --rm / --reword).
			index := func() (int, error) {
				if len(args) != 2 {
					return 0, core.Validationf(args[0], "provide a checklist item index")
				}
				return atoiArg("item-index", args[1])
			}
			verb := "checked"
			var mutate func() (*core.Task, error)
			switch {
			case len(adds) > 0:
				verb = "checklist+"
				mutate = func() (*core.Task, error) { return a.AddChecks(args[0], adds) }
			case rm:
				verb = "checklist-"
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.RemoveCheck(args[0], i)
				}
			case cmd.Flags().Changed("reword"):
				verb = "checklist~"
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.RewordCheck(args[0], i, reword)
				}
			default:
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.Check(args[0], i, !off)
				}
			}
			return emitMutation(a, verb, args[0], mutate)
		},
	}
	cmd.Flags().StringArrayVar(&adds, "add", nil, "append a checklist item with this text (repeatable)")
	cmd.Flags().BoolVar(&off, "off", false, "uncheck instead of check")
	cmd.Flags().BoolVar(&rm, "rm", false, "delete the checklist item at the index")
	cmd.Flags().StringVar(&reword, "reword", "", "replace the text of the item at the index")
	cmd.MarkFlagsMutuallyExclusive("add", "rm", "reword", "off")
	return cmd
}

func newDepCmd() *cobra.Command {
	var rm, list bool
	cmd := &cobra.Command{
		Use:   "dep <id> [<dep-id>...]",
		Short: "Add/remove a task's dependencies, or list them both ways with --list",
		Long: "Make <id> depend on each <dep-id> (id waits on them). Several dep-ids in one\n" +
			"call apply in a single write. With --rm, remove those dependencies instead.\n" +
			"Every dep must exist; adding is acyclic and idempotent, and the batch is\n" +
			"all-or-nothing (a bad dep-id aborts without a partial change).\n\n" +
			"With --list, don't mutate — read <id>'s dependency neighborhood in BOTH\n" +
			"directions: what it depends_on (its own deps — what it waits on) and what it\n" +
			"blocks (the reverse edge — the tasks waiting on it), each resolved to\n" +
			"id+title+lane. --json/--ndjson emit one object with both arrays. The\n" +
			"reverse edge is the local, no-server twin of \"what unblocks if I finish this\".",
		Example: "  furrow dep t-k3m9p t-a1b2c\n" +
			"  furrow dep t-k3m9p t-a1b2c t-d4e5f    # depend on both in one write\n" +
			"  furrow dep t-k3m9p t-a1b2c --rm\n" +
			"  furrow dep t-k3m9p --list --json      # what it waits on and what it blocks",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("list") {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.MinimumNArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if list {
				res, err := a.DepList(args[0])
				if err != nil {
					return err
				}
				return emitDepList(res)
			}
			id, deps := args[0], args[1:]
			verb := "dep+"
			mutate := func() (*core.Task, error) { return a.AddDeps(id, deps) }
			if rm {
				verb = "dep-"
				mutate = func() (*core.Task, error) { return a.RemoveDeps(id, deps) }
			}
			return emitMutation(a, verb, id, mutate)
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the dependencies instead of adding them")
	cmd.Flags().BoolVar(&list, "list", false, "read-only: list what <id> depends on and what depends on it (both directions)")
	cmd.MarkFlagsMutuallyExclusive("list", "rm")
	return cmd
}

// newSetCmd combines the routine triage edits (lane, value, effort, labels) into
// one write, so triaging a task no longer means running move + value + effort +
// label as four separate commands.
func newSetCmd() *cobra.Command {
	var (
		status      string
		value       int
		effort      int
		clearValue  bool
		clearEffort bool
		addLabels   []string
		rmLabels    []string
		typ         string
		priority    int
		before      string
		after       string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Apply several triage edits at once (lane, priority, value, effort, labels)",
		Long: "Combine the routine triage edits into a single write: move a lane (-s),\n" +
			"position the task (--priority, or --before/--after a task in the destination\n" +
			"lane — so a cross-lane drop is lane + position in ONE write), set or clear\n" +
			"the 1..5 value/effort estimates, and add/remove labels — instead of running\n" +
			"move + reorder + value + effort + label as separate commands. At least one\n" +
			"change is required; an unknown lane is exit 2 with candidates (like move),\n" +
			"a relative target outside the destination lane is exit 2, and under\n" +
			"[labels].required a set that would strip the last label is refused. A\n" +
			"relative placement that has to respace the lane does so in the same write\n" +
			"and reports the neighbors in `renumbered`, exactly like reorder.",
		Example: "  furrow set t-k3m9p -s ready --value 4 --effort 2 --add-label bug\n" +
			"  furrow set t-k3m9p -s ready --before t-x1y2z\n" +
			"  furrow set t-k3m9p --clear-value --rm-label wip",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o := app.SetOpts{
				Before:      before,
				After:       after,
				AddLabels:   addLabels,
				RmLabels:    rmLabels,
				ClearValue:  clearValue,
				ClearEffort: clearEffort,
			}
			if cmd.Flags().Changed("status") {
				o.Status = &status
			}
			if cmd.Flags().Changed("priority") {
				p := priority
				o.Priority = &p
			}
			if cmd.Flags().Changed("value") {
				v := value
				o.Value = &v
			}
			if cmd.Flags().Changed("effort") {
				e := effort
				o.Effort = &e
			}
			if cmd.Flags().Changed("type") {
				o.Type = &typ
			}
			var renumbered []core.PriorityChange
			return emitMutationWith(a, "set", args[0],
				func() (*core.Task, error) {
					t, ch, err := a.Set(args[0], o)
					renumbered = ch
					return t, err
				},
				func(after *core.Task) map[string]any {
					extra := map[string]any{}
					clamped := map[string]any{}
					warnClamp("value", o.Value, after.Value)
					warnClamp("effort", o.Effort, after.Effort)
					if e := clampEntry(o.Value, after.Value); e != nil {
						clamped["value"] = e
					}
					if e := clampEntry(o.Effort, after.Effort); e != nil {
						clamped["effort"] = e
					}
					if len(clamped) > 0 {
						extra["clamped"] = clamped
					}
					for k, v := range respaceExtra(renumbered, after.Status) {
						extra[k] = v
					}
					if len(extra) == 0 {
						return nil
					}
					return extra
				})
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "move to this lane")
	cmd.Flags().StringVar(&typ, "type", "", "set the work-item type (a value from [types].order, e.g. epic)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "set the sparse priority directly")
	cmd.Flags().StringVar(&before, "before", "", "place immediately before this task (in the destination lane)")
	cmd.Flags().StringVar(&after, "after", "", "place immediately after this task (in the destination lane)")
	cmd.Flags().IntVar(&value, "value", 0, "set the 1..5 value estimate")
	cmd.Flags().IntVar(&effort, "effort", 0, "set the 1..5 effort estimate")
	cmd.Flags().BoolVar(&clearValue, "clear-value", false, "clear the value estimate")
	cmd.Flags().BoolVar(&clearEffort, "clear-effort", false, "clear the effort estimate")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "add a label (repeatable)")
	cmd.Flags().StringArrayVar(&rmLabels, "rm-label", nil, "remove a label (repeatable)")
	cmd.MarkFlagsMutuallyExclusive("value", "clear-value")
	cmd.MarkFlagsMutuallyExclusive("effort", "clear-effort")
	cmd.MarkFlagsMutuallyExclusive("priority", "before", "after")
	return cmd
}

func newLabelCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "label <id>",
		Short: "Add and/or remove labels on a task",
		Long: "Add labels with --add and remove them with --remove (both repeatable and\n" +
			"combinable in one call). Adding a label already present, or removing one\n" +
			"already absent, is a no-op. Provide at least one --add or --remove.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutation(a, "labeled", args[0], func() (*core.Task, error) {
				return a.Relabel(args[0], add, remove)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "label to add (repeatable)")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "label to remove (repeatable)")
	return cmd
}

func newRefCmd() *cobra.Command {
	var add, rm []string
	cmd := &cobra.Command{
		Use:   "ref <id>",
		Short: "Add and/or remove refs (file:line or URL) on a task",
		Long: "Edit a task's refs after creation — the counterpart to `add --ref`. Add\n" +
			"refs with --add and remove them with --rm (both repeatable and combinable\n" +
			"in one call). Adding a ref already present, or removing one already absent,\n" +
			"is a no-op. Refs keep the order you gave them (they are a sequence, not a\n" +
			"sorted set like labels): --add appends at the end.",
		Example: "  furrow ref t-k3m9p --add internal/cli/root.go:42\n" +
			"  furrow ref t-k3m9p --add https://example.com/spec --rm docs/old.md:10",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutation(a, "ref", args[0], func() (*core.Task, error) {
				return a.Reref(args[0], add, rm)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "ref to add (file:line or URL; repeatable)")
	cmd.Flags().StringSliceVar(&rm, "rm", nil, "ref to remove (exact match; repeatable)")
	return cmd
}

func newRetitleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retitle <id> <title...>",
		Short: "Rename a task (updates the shard title and the body heading)",
		Long: "Set a task's one-line title. The title lives in two places — the task\n" +
			"shard's title field and the body's leading `# ` heading — and retitle\n" +
			"updates both so they never drift (the shard is the source of truth; a body\n" +
			"with no leading heading is left untouched). The remaining args are joined\n" +
			"with spaces, so the title need not be quoted:\n\n" +
			"  furrow retitle t-k3m9p a clearer, shorter title",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id, title := args[0], strings.Join(args[1:], " ")
			return emitMutation(a, "retitled", id, func() (*core.Task, error) { return a.Retitle(id, title) })
		},
	}
}

func newRepoCmd() *cobra.Command {
	var add, rm []string
	cmd := &cobra.Command{
		Use:   "repo <id>",
		Short: "Attach and/or detach repos (owner/repo) on a task",
		Long: "Attach repos with --add and detach them with --rm (both repeatable and\n" +
			"combinable in one call). Each value must be a full owner/repo, or a short\n" +
			"name matching exactly one repo already known to the board (case-insensitive,\n" +
			"at a '/' boundary); anything else is a validation error — never a silent new\n" +
			"repo. Attaching a repo already present, or detaching one already absent, is\n" +
			"a no-op. A task with no repos is a draft (see ls --drafts).",
		Example: "  furrow repo t-k3m9p --add akira-toriyama/furrow\n" +
			"  furrow repo t-k3m9p --rm furrow                # detach by short name\n" +
			"  furrow repo t-k3m9p --add cifail --rm furrow   # move across repos",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutation(a, "repo", args[0], func() (*core.Task, error) {
				return a.Rerepo(args[0], add, rm)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "repo to attach (owner/repo, or a unique short name; repeatable)")
	cmd.Flags().StringSliceVar(&rm, "rm", nil, "repo to detach (same forms; repeatable)")
	return cmd
}
