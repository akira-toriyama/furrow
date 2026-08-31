package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// The stale-read guard (--expect-updated): furrow's store has no locks, so when
// two sessions work one board, the later write silently wins and the earlier
// session keeps reasoning about a task that has moved under it. The guard is
// the caller declaring "my picture of this task is its updated=<ts>": pass the
// stamp your last read emitted, and when someone else wrote in between, the
// mutation still goes through but says so — a stderr note plus a `stale_read`
// {expected, actual} envelope key. A WARNING by design, not an optimistic-lock
// refusal: the co-writer being caught is usually right too (they acted on newer
// knowledge), so the fix is to re-read and reconcile, not to lose the second
// edit as well. One timestamp describes one read of ONE entity, so the batch
// mutators refuse it alongside several ids (the position-flag precedent).
const expectUpdatedFlag = "expect-updated"

// addExpectUpdatedFlag registers --expect-updated on a mutating command. The
// funnels look the flag up by name, so a command without the registration
// simply has no guard (never a silent ignore of a set flag: cobra rejects an
// unregistered flag at parse time).
func addExpectUpdatedFlag(cmd *cobra.Command) {
	cmd.Flags().String(expectUpdatedFlag, "",
		"the updated stamp from your last read (RFC3339); warns via stale_read when the entity changed since")
}

// expectUpdatedArg reads --expect-updated off cmd: ok=false when the command
// never registered it or the caller didn't pass it. A malformed timestamp is
// exit 2 — an explicit argument is never quietly dropped.
func expectUpdatedArg(cmd *cobra.Command, subject string) (time.Time, bool, error) {
	if cmd == nil {
		return time.Time{}, false, nil
	}
	f := cmd.Flags().Lookup(expectUpdatedFlag)
	if f == nil || f.Value.String() == "" {
		return time.Time{}, false, nil
	}
	ts, err := time.Parse(time.RFC3339, f.Value.String())
	if err != nil {
		return time.Time{}, false, core.Validationf(subject,
			"--expect-updated %q is not an RFC3339 timestamp (pass `updated` exactly as a read emitted it, e.g. 2026-08-09T12:00:00Z)", f.Value.String())
	}
	return ts, true, nil
}

// staleReadExtra compares the pre-mutation `updated` against the caller's
// declared read. Instants are compared (time.Equal), so a +09:00 spelling of
// the same moment matches the stored UTC. On a mismatch it warns on stderr and
// returns the `stale_read` envelope entry; nil when current (the key must not
// appear on a clean write). A nil pre-fetch yields nil too: the mutate closure
// owns not-found errors, and a guard that cannot see the before has nothing
// truthful to say.
func staleReadExtra(id string, actual *time.Time, expected time.Time) map[string]any {
	if actual == nil || actual.Equal(expected) {
		return nil
	}
	a, e := actual.UTC().Format(time.RFC3339), expected.UTC().Format(time.RFC3339)
	fmt.Fprintf(errOut, "note: %s changed since your read (updated %s, you read %s) — the write went through; re-read before editing further\n", id, a, e)
	return map[string]any{"stale_read": map[string]any{"expected": e, "actual": a}}
}

// mergeExtra folds add into extra, allocating only when there is something to
// merge — so the envelope stays key-free on the common clean path.
func mergeExtra(extra, add map[string]any) map[string]any {
	if len(add) == 0 {
		return extra
	}
	if extra == nil {
		extra = map[string]any{}
	}
	for k, v := range add {
		extra[k] = v
	}
	return extra
}

// emitMutation runs a single-task edit on id and reports it. In machine mode
// (--json or --ndjson) it snapshots the task before the change and prints
// {before, after, changed}, so an agent sees the effect inline without a
// follow-up `show`. The pre-fetch is skipped (and harmless) in human mode
// unless the stale-read guard needs it; the mutate closure is the
// authoritative source of any not-found / validation error.
func emitMutation(cmd *cobra.Command, a *app.App, verb, id string, mutate func() (*core.Task, error)) error {
	return emitMutationWith(cmd, a, verb, id, mutate, nil)
}

// emitMutationWith is emitMutation plus an optional `annotate`: given the
// resulting task it returns extra top-level fields to merge into the --json
// {before,after,changed} envelope (and may write a human note to stderr).
func emitMutationWith(cmd *cobra.Command, a *app.App, verb, id string, mutate func() (*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	expected, guard, gerr := expectUpdatedArg(cmd, id)
	if gerr != nil {
		return gerr
	}
	var before *core.Task
	if guard || jsonMode() {
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
	if guard && before != nil {
		extra = mergeExtra(extra, staleReadExtra(before.ID, &before.Updated, expected))
	}
	printMutation(verb, before, after, extra)
	return nil
}

// emitMutationMany is emitMutation for a batch mutator: one {before,after,
// changed} envelope per task, in the batch's (deduped) input order — --json
// ALWAYS an array (a single id is a one-element array: a command whose Use
// says `<id>...` has array cardinality by SIGNATURE, and the runtime argv
// length must not fork the shape — the always-array rule), --ndjson one
// envelope per line, human mode one verb line per task. Befores come from one
// batch read; a miss there is harmless because the mutate closure is the
// authority and fails the whole batch before anything is printed.
func emitMutationMany(cmd *cobra.Command, a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error)) error {
	return emitMutationManyWith(cmd, a, verb, ids, mutate, nil)
}

// emitMutationManyWith is emitMutationMany plus an optional per-task annotate:
// given a resulting task it returns extra top-level fields merged into THAT
// task's envelope — the batch twin of emitMutationWith's annotate (done --note
// surfaces `appended` on each, a single-id set its `clamped`/`renumbered`).
func emitMutationManyWith(cmd *cobra.Command, a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	expected, guard, gerr := expectUpdatedArg(cmd, strings.Join(ids, ","))
	if gerr != nil {
		return gerr
	}
	if guard && len(ids) > 1 {
		// Same rule as set's position flags: one --expect-updated describes one
		// read of one task, so it cannot ride a several-id batch.
		return core.Validationf("", "--expect-updated describes one read of one task; it cannot apply to %d ids", len(ids))
	}
	befores := map[string]*core.Task{}
	if guard || jsonMode() {
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
	var stale map[string]any
	if guard {
		if b := befores[ids[0]]; b != nil {
			stale = staleReadExtra(b.ID, &b.Updated, expected)
		}
	}
	if jsonMode() {
		envs := make([]any, 0, len(after))
		for _, t := range after {
			var extra map[string]any
			if annotate != nil {
				extra = annotate(t)
			}
			if t.ID == ids[0] {
				extra = mergeExtra(extra, stale)
			}
			envs = append(envs, mutationEnvelope(befores[t.ID], t, extra))
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
	var sel writeSelector
	cmd := &cobra.Command{
		Use:   "done [<id>...]",
		Short: "Move tasks into the done lane (stamps closed)",
		Long: "Close one or more tasks in a single index write, all-or-nothing: a batch\n" +
			"with an unknown id closes NOTHING and exits 1 with every miss in\n" +
			"details.missing (the show batch shape). --json is ALWAYS an array of\n" +
			"{before,after,changed} envelopes, one per id (a single id is a one-element\n" +
			"array); --ndjson streams one envelope per line.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters: -q (the ls/next typed query, same grammar), -l, and -r, which AND\n" +
			"together under the board scope exactly as in ls — so `furrow ls <flags>`\n" +
			"previews precisely what `furrow done <flags>` would close. A selection and\n" +
			"an id list refuse to combine (exit 2). A selection only PREVIEWS the\n" +
			"matched tasks until --yes (the archive/tidy destructive-op guard; the\n" +
			"preview is {dry_run: true, tasks} in JSON), the close itself is the same\n" +
			"single all-or-nothing write as the id form — no jq, no xargs, no ARG_MAX\n" +
			"split — and a selection matching nothing is exit 0 with a stderr note, like\n" +
			"every empty read. --expect-updated cannot ride a selection (one stamp\n" +
			"describes one read of one task).\n\n" +
			"--note \"<text>\" records the closing word in the same command: the text is\n" +
			"appended to EVERY closed task's body as a new paragraph (the note command's\n" +
			"contract — updated advances, nothing is deduped) and the envelope gains the\n" +
			"same `appended` key. Pass `-` to read the note from stdin; an empty note is\n" +
			"exit 2, never a silent plain close.",
		Example: "  furrow done t-k3m9p\n" +
			"  furrow done t-k3m9p --note \"→ continued in t-x7q2\"\n" +
			"  furrow done t-k3m9p t-x7q2 t-9d4n   # triage sweep, one write\n" +
			"  furrow done -q 'label:spike status:waiting'        # preview the selection\n" +
			"  furrow done -q 'label:spike status:waiting' --yes  # close it, one write",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("query") || cmd.Flags().Changed("label") || cmd.Flags().Changed("repo") {
				return cobra.ArbitraryArgs(cmd, args) // the id/selector clash gets its own message in guard
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := sel.guard(cmd, args); err != nil {
				return err
			}
			if sel.active(cmd) {
				tasks, err := sel.resolve(cmd, a)
				if err != nil {
					return err
				}
				if !sel.yes {
					emitSelectPreview("close", tasks)
					return nil
				}
				if len(tasks) == 0 {
					emitEmptySelection()
					return nil
				}
				args = taskIDs(tasks)
			}
			if !cmd.Flags().Changed("note") {
				return emitMutationMany(cmd, a, "done", args, func() ([]*core.Task, error) { return a.DoneMany(args) })
			}
			text, terr := readTextArg(cmd, note)
			if terr != nil {
				return terr
			}
			// `changed` tracks metadata only, so surface the note's effect the
			// way the note command does.
			appended := map[string]any{"appended": strings.TrimRight(text, "\n")}
			return emitMutationManyWith(cmd, a, "done", args,
				func() ([]*core.Task, error) { return a.DoneManyNote(args, text) },
				func(*core.Task) map[string]any { return appended })
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "append this closing note to each task's body ('-' reads stdin)")
	addSelectorFlags(cmd, &sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newMoveCmd() *cobra.Command {
	var sel writeSelector
	cmd := &cobra.Command{
		Use:   "move [<id>...] <lane>",
		Short: "Move tasks to a lane",
		Long: "Move one or more tasks to <lane> (the LAST argument) in a single index\n" +
			"write, all-or-nothing: a batch with an unknown id moves NOTHING and exits 1\n" +
			"with every miss in details.missing; an unknown lane is exit 2 with the\n" +
			"configured lanes in candidates. --json is ALWAYS an array of\n" +
			"{before,after,changed} envelopes, one per id (a single id is a one-element\n" +
			"array); --ndjson streams one envelope per line.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters — -q/-l/-r, ANDed under the board scope exactly as in ls (then\n" +
			"`move <flags> <lane>` takes just the lane). The selection previews until\n" +
			"--yes ({dry_run: true, tasks} in JSON), applies as the same single\n" +
			"all-or-nothing write, matches-nothing is exit 0 with a stderr note, and it\n" +
			"refuses to combine with ids or --expect-updated (exit 2) — the full\n" +
			"contract is spelled out in `furrow done --help`.",
		Example: "  furrow move t-k3m9p in-progress\n" +
			"  furrow move t-k3m9p t-x7q2 backlog     # triage sweep, one write\n" +
			"  furrow move -q 'status:inbox no:value' icebox        # preview\n" +
			"  furrow move -q 'status:inbox no:value' icebox --yes  # one write",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("query") || cmd.Flags().Changed("label") || cmd.Flags().Changed("repo") {
				return cobra.MinimumNArgs(1)(cmd, args) // <lane> only; extra ids get guard's message
			}
			return cobra.MinimumNArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			ids, lane := args[:len(args)-1], args[len(args)-1]
			if err := sel.guard(cmd, ids); err != nil {
				return err
			}
			if sel.active(cmd) {
				// Vet the lane BEFORE resolving: `move -q … typo-lane` must exit 2
				// with candidates whether or not anything matches, and a preview
				// against a lane the apply would refuse is a lie.
				if err := a.CheckLane(lane); err != nil {
					return err
				}
				tasks, err := sel.resolve(cmd, a)
				if err != nil {
					return err
				}
				if !sel.yes {
					emitSelectPreview("move to "+lane, tasks)
					return nil
				}
				if len(tasks) == 0 {
					emitEmptySelection()
					return nil
				}
				ids = taskIDs(tasks)
			}
			return emitMutationMany(cmd, a, "moved", ids, func() ([]*core.Task, error) { return a.MoveMany(ids, lane) })
		},
	}
	addSelectorFlags(cmd, &sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note <id> <text>",
		Short: "Append a paragraph to a task's or epic's body and advance its updated time",
		Long: "Append <text> as a new paragraph to bodies/<id>.md AND stamp the entity's\n" +
			"`updated` time, in one command — the in-band way to record progress, stop-points,\n" +
			"and next steps across sessions. Unlike hand-editing the file (what `furrow\n" +
			"edit` hands an agent), it keeps `updated` honest, so `furrow lint`'s\n" +
			"reconcile-gap check does not misfire on a task whose progress lives only in\n" +
			"its body. Pass `-` as <text> to read the note from stdin (for multi-line or\n" +
			"long notes).\n\n" +
			"<id> may name a TASK or an EPIC — the two share the bodies/ directory, so a\n" +
			"box's progress record is the same operation on the same file, and only the\n" +
			"shard that stamps `updated` differs. MEMBERSHIP routes it, not the id's\n" +
			"prefix: a ref naming a real task is always the task, else a ref the epic\n" +
			"store resolves (exact id, unique id prefix, unique title substring — every\n" +
			"other epic reference's contract) is that box. A ref that resolves to neither\n" +
			"fails on the side its prefix suggests: an unknown `e-` id is exit 2 with\n" +
			"candidates, an unknown task id exit 1.",
		Example: "  furrow note t-k3m9p \"検証完了。次: アダプタ選定。\"\n" +
			"  furrow note e-k3m9 \"方針: v7 の pinned に寄せる。\"\n" +
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
			// `changed` tracks metadata fields only, so a note (body + updated)
			// would show changed:[] — surface the effect instead. Both entities
			// carry the same key, so a caller reads one shape either way.
			appended := map[string]any{"appended": strings.TrimRight(text, "\n")}
			epic, rerr := a.RefTargetsEpic(args[0])
			if rerr != nil {
				return rerr
			}
			if epic {
				// The stale-read guard covers the box side of note's dual contract
				// too: an epic's progress record races between sessions exactly
				// like a task's, and both entities carry `updated`.
				expected, guard, gerr := expectUpdatedArg(cmd, args[0])
				if gerr != nil {
					return gerr
				}
				before, after, nerr := a.EpicNote(args[0], text)
				if nerr != nil {
					return nerr
				}
				if guard && before != nil {
					appended = mergeExtra(appended, staleReadExtra(before.ID, &before.Updated, expected))
				}
				printEpicMutation("noted", before, after, appended)
				return nil
			}
			return emitMutationWith(cmd, a, "noted", args[0],
				func() (*core.Task, error) { return a.AddNote(args[0], text) },
				func(after *core.Task) map[string]any { return appended })
		},
	}
	addExpectUpdatedFlag(cmd)
	return cmd
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
				return emitMutationWith(cmd, a, "reordered", id,
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
				return emitMutation(cmd, a, "reordered", id, func() (*core.Task, error) { return a.Reorder(id, prio) })
			}
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "place immediately before this task (same lane)")
	cmd.Flags().StringVar(&after, "after", "", "place immediately after this task (same lane)")
	cmd.MarkFlagsMutuallyExclusive("before", "after")
	addExpectUpdatedFlag(cmd)
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
			"signal for picking the next task — order by it with `furrow ls --sort value`\n" +
			"(unset estimates stay last) or select on it with `furrow ls -q 'roi:>=2'`.",
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
			return emitMutationWith(cmd, a, name, id,
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
	addExpectUpdatedFlag(cmd)
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
			return emitMutation(cmd, a, verb, args[0], mutate)
		},
	}
	cmd.Flags().StringArrayVar(&adds, "add", nil, "append a checklist item with this text (repeatable)")
	cmd.Flags().BoolVar(&off, "off", false, "uncheck instead of check")
	cmd.Flags().BoolVar(&rm, "rm", false, "delete the checklist item at the index")
	cmd.Flags().StringVar(&reword, "reword", "", "replace the text of the item at the index")
	cmd.MarkFlagsMutuallyExclusive("add", "rm", "reword", "off")
	addExpectUpdatedFlag(cmd)
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
			return emitMutation(cmd, a, verb, id, mutate)
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the dependencies instead of adding them")
	cmd.Flags().BoolVar(&list, "list", false, "read-only: list what <id> depends on and what depends on it (both directions)")
	cmd.MarkFlagsMutuallyExclusive("list", "rm")
	addExpectUpdatedFlag(cmd)
	return cmd
}

// newSetCmd combines the routine triage edits into one write, so triaging a task
// no longer means running move + reorder + value + effort + label + epic as
// separate commands.
func newSetCmd() *cobra.Command {
	var (
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
		sel         writeSelector
	)
	cmd := &cobra.Command{
		Use:   "set [<id>...]",
		Short: "Apply several triage edits at once (lane, priority, value, effort, labels, repos, epic, due)",
		Long: "Combine the routine triage edits into a single write: move a lane (-s),\n" +
			"position the task (--priority, or --before/--after a task in the destination\n" +
			"lane — so a cross-lane drop is lane + position in ONE write), set or clear\n" +
			"the 1..5 value/effort estimates, add/remove labels, attach/detach repos\n" +
			"(--add-repo/--rm-repo — Rerepo's strict resolution, so a short name must\n" +
			"match exactly one known repo; removing the last repo leaves a first-class\n" +
			"DRAFT), and file the task under\n" +
			"an epic (-e), and set or clear the due date (--due/--clear-due, where\n" +
			"--due +1d is the snooze) — instead of running move + reorder + value +\n" +
			"effort + label as separate commands. At least one change is required; an unknown lane is\n" +
			"exit 2 with the configured lanes in candidates (like move/add) and an\n" +
			"unresolvable -e epic exits 2 with the known boxes,\n" +
			"a relative target outside the destination lane is exit 2, and under\n" +
			"[labels].required a set that would strip the last label is refused. A\n" +
			"relative placement that has to respace the lane does so in the same write\n" +
			"and reports the neighbors in `renumbered`, exactly like reorder.\n\n" +
			"Several ids apply the SAME edits to all of them in ONE all-or-nothing\n" +
			"write (bulk triage): a miss sets NOTHING and exits 1 with every miss in\n" +
			"details.missing, and --json is ALWAYS an array of envelopes, one per id\n" +
			"(a single id is a one-element array). The position flags\n" +
			"(--priority/--before/--after) apply to ONE task and are exit 2 for two or\n" +
			"more ids.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters — -q/-l/-r, ANDed under the board scope exactly as in ls. The\n" +
			"selection previews until --yes ({dry_run: true, tasks} in JSON), applies\n" +
			"as the same single all-or-nothing write, matches-nothing is exit 0 with a\n" +
			"stderr note, and it refuses to combine with ids, --expect-updated, or the\n" +
			"position flags (a position places ONE task) — the full contract is\n" +
			"spelled out in `furrow done --help`.",
		Example: "  furrow set t-k3m9p -s ready --value 4 --effort 2 --add-label bug\n" +
			"  furrow set t-k3m9p -s ready --before t-x1y2z\n" +
			"  furrow set t-k3m9p -e e-v0zd\n" +
			"  furrow set t-k3m9p t-x1y2z t-9f2qr -s backlog --add-label triaged\n" +
			"  furrow set t-a1 t-b2 t-c3 --add-repo owner/app   # bulk attach, one write\n" +
			"  furrow set t-k3m9p --due 2026-08-04     # promise it for that whole day\n" +
			"  furrow set t-k3m9p --due +1d            # snooze a day from now\n" +
			"  furrow set t-k3m9p --clear-value --rm-label wip\n" +
			"  furrow set -q 'status:inbox label:bug' -e e-v0zd        # preview\n" +
			"  furrow set -q 'status:inbox label:bug' -e e-v0zd --yes  # one write",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("query") || cmd.Flags().Changed("label") || cmd.Flags().Changed("repo") {
				return cobra.ArbitraryArgs(cmd, args) // the id/selector clash gets its own message in guard
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
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
				AddRepos:    addRepos,
				RmRepos:     rmRepos,
				ClearValue:  clearValue,
				ClearEffort: clearEffort,
				ClearDue:    clearDue,
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
			if cmd.Flags().Changed("epic") {
				o.Epic = &epicRef
			}
			if cmd.Flags().Changed("due") {
				o.Due = &due
			}
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
			if f := cmd.Flags().Lookup("due"); f != nil && f.Changed && strings.TrimSpace(due) == "" {
				return core.Validationf(subject, "--due was given an empty value; pass a date, or use --clear-due to remove it")
			}
			if err := emptyFlagErr(cmd, subject, "before", "after", "add-label", "rm-label", "add-repo", "rm-repo", "status"); err != nil {
				return err
			}
			if err := sel.guard(cmd, args); err != nil {
				return err
			}
			if sel.active(cmd) {
				if cmd.Flags().Changed("priority") || cmd.Flags().Changed("before") || cmd.Flags().Changed("after") {
					return core.Validationf("", "the position flags place ONE task; they cannot ride a -q/-l/-r selection")
				}
				// Vet what CAN be vetted before resolving, so the preview never
				// shows a write the apply would refuse: an edit must exist (the
				// app's own at-least-one-change rule), and a -s lane must be real.
				hasEdit := o.Status != nil || o.Value != nil || o.Effort != nil || o.Epic != nil || o.Due != nil ||
					o.ClearValue || o.ClearEffort || o.ClearDue || len(o.AddLabels) > 0 || len(o.RmLabels) > 0 ||
					len(o.AddRepos) > 0 || len(o.RmRepos) > 0
				if !hasEdit {
					return core.Validationf("", "a selection needs at least one edit flag (-s, --value, --add-label, -e, --due, …) to apply")
				}
				if o.Status != nil {
					if err := a.CheckLane(*o.Status); err != nil {
						return err
					}
				}
				tasks, err := sel.resolve(cmd, a)
				if err != nil {
					return err
				}
				if !sel.yes {
					emitSelectPreview("set", tasks)
					return nil
				}
				if len(tasks) == 0 {
					emitEmptySelection()
					return nil
				}
				args = taskIDs(tasks)
			}
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
			if len(args) > 1 {
				return emitMutationManyWith(cmd, a, "set", args,
					func() ([]*core.Task, error) { return a.SetMany(args, o) },
					clampExtra)
			}
			// One id still emits a one-element ARRAY (the always-array rule —
			// `set <id>...` has array cardinality by signature); only the
			// single-task renumbered extra needs this separate path.
			var renumbered []core.PriorityChange
			return emitMutationManyWith(cmd, a, "set", args,
				func() ([]*core.Task, error) {
					t, ch, err := a.Set(args[0], o)
					renumbered = ch
					if err != nil {
						return nil, err
					}
					return []*core.Task{t}, nil
				},
				func(after *core.Task) map[string]any {
					extra := map[string]any{}
					for k, v := range clampExtra(after) {
						extra[k] = v
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
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "re-file under this epic (id, unique id prefix, or unique title substring; \"\" unfiles)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "set the sparse priority directly")
	cmd.Flags().StringVar(&before, "before", "", "place immediately before this task (in the destination lane)")
	cmd.Flags().StringVar(&after, "after", "", "place immediately after this task (in the destination lane)")
	cmd.Flags().IntVar(&value, "value", 0, "set the 1..5 value estimate")
	cmd.Flags().IntVar(&effort, "effort", 0, "set the 1..5 effort estimate")
	cmd.Flags().BoolVar(&clearValue, "clear-value", false, "clear the value estimate")
	cmd.Flags().BoolVar(&clearEffort, "clear-effort", false, "clear the effort estimate")
	cmd.Flags().StringVar(&due, "due", "", "set the due date: 2026-08-04 (that whole day), 2026-08-04T10:30, an RFC3339 instant, or an offset like +1d (the snooze)")
	cmd.Flags().BoolVar(&clearDue, "clear-due", false, "clear the due date")
	// StringSlice, not StringArray: these edit the SAME field `label --add` does
	// (cmd_mutate.go's newLabelCmd), and comma is how every label surface splits —
	// `-l a,b` is OR on reads. As StringArray, `set --add-label "a,b"` stored the
	// single label "a,b", which NO read filter can match (`-l "a,b"` means a OR b),
	// so the write produced data unreachable by the tool that wrote it.
	cmd.Flags().StringSliceVar(&addLabels, "add-label", nil, "add a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&rmLabels, "rm-label", nil, "remove a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&addRepos, "add-repo", nil, "attach a repo (owner/repo or a unique short name; repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&rmRepos, "rm-repo", nil, "detach a repo (same forms; removing the last one leaves a draft; repeatable)")
	cmd.MarkFlagsMutuallyExclusive("value", "clear-value")
	cmd.MarkFlagsMutuallyExclusive("effort", "clear-effort")
	cmd.MarkFlagsMutuallyExclusive("due", "clear-due")
	cmd.MarkFlagsMutuallyExclusive("priority", "before", "after")
	addSelectorFlags(cmd, &sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

// emptyFlagErr names a flag that WAS passed but carried nothing pflag kept — a
// bare `--add ""` on a StringSlice, or `--before ""`. Without it the command
// falls through to its "provide at least one …" guard and tells the caller to
// pass a flag they just passed, which reads as a bug in furrow rather than in
// the invocation. The exit code was already 2; this only stops the message
// from being wrong. (A NON-empty blank like `--add " "` is caught deeper, in
// the app's requireNonBlank, which is where the whole rule lives.)
func emptyFlagErr(cmd *cobra.Command, id string, names ...string) error {
	for _, name := range names {
		f := cmd.Flags().Lookup(name)
		if f == nil || !f.Changed {
			continue
		}
		switch v := f.Value.(type) {
		case interface{ GetSlice() []string }:
			if len(v.GetSlice()) == 0 {
				return core.Validationf(id, "--%s was given an empty value; pass a real one or drop the flag", name)
			}
		default:
			if strings.TrimSpace(f.Value.String()) == "" {
				return core.Validationf(id, "--%s was given an empty value; pass a real one or drop the flag", name)
			}
		}
	}
	return nil
}

func newLabelCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "label <id>",
		Short: "Add and/or remove labels on a task",
		Long: "Add labels with --add and remove them with --rm (both repeatable and\n" +
			"combinable in one call). Adding a label already present, or removing one\n" +
			"already absent, is a no-op. Provide at least one --add or --rm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "labeled", args[0], func() (*core.Task, error) {
				return a.Relabel(args[0], add, remove)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "label to add (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&remove, "rm", nil, "label to remove (repeatable; comma-separated)")
	addExpectUpdatedFlag(cmd)
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
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "ref", args[0], func() (*core.Task, error) {
				return a.Reref(args[0], add, rm)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "ref to add (file:line or URL; repeatable)")
	cmd.Flags().StringSliceVar(&rm, "rm", nil, "ref to remove (exact match; repeatable)")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newRetitleCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			return emitMutation(cmd, a, "retitled", id, func() (*core.Task, error) { return a.Retitle(id, title) })
		},
	}
	addExpectUpdatedFlag(cmd)
	return cmd
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
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "repo", args[0], func() (*core.Task, error) {
				return a.Rerepo(args[0], add, rm)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "repo to attach (owner/repo, or a unique short name; repeatable)")
	cmd.Flags().StringSliceVar(&rm, "rm", nil, "repo to detach (same forms; repeatable)")
	addExpectUpdatedFlag(cmd)
	return cmd
}
