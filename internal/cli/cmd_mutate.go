package cli

import (
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
	printMutation(verb, before, after)
	return nil
}

func newDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "done <id>",
		Short:   "Move a task into the done lane (stamps closed)",
		Example: "  furrow done t-k3m9p",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutation(a, "done", args[0], func() (*core.Task, error) { return a.Done(args[0]) })
		},
	}
}

func newMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <id> <lane>",
		Short: "Move a task to a lane",
		Example: "  furrow move t-k3m9p in-progress\n" +
			"  furrow move t-k3m9p done",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutation(a, "moved", args[0], func() (*core.Task, error) { return a.Move(args[0], args[1]) })
		},
	}
}

func newReorderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <id> <priority>",
		Short: "Set a task's priority (sparse integer; lower = higher up)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			prio, err := atoiArg("priority", args[1])
			if err != nil {
				return err
			}
			return emitMutation(a, "reordered", args[0], func() (*core.Task, error) { return a.Reorder(args[0], prio) })
		},
	}
}

// newEstimateCmd builds the shared `value`/`effort` setter: `furrow <name> <id>
// <1-5>` records a coarse estimate (clamped into 1..5), `--clear` unsets it.
// value and effort together drive ROI = value/effort for picking the next task.
func newEstimateCmd(name string, set func(*app.App, string, *int) (*core.Task, error)) *cobra.Command {
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
			return emitMutation(a, name, id, func() (*core.Task, error) { return set(a, id, v) })
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the estimate (back to unset)")
	return cmd
}

func newValueCmd() *cobra.Command {
	return newEstimateCmd("value", func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetValue(id, v) })
}

func newEffortCmd() *cobra.Command {
	return newEstimateCmd("effort", func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetEffort(id, v) })
}

func newCheckCmd() *cobra.Command {
	var (
		adds []string
		off  bool
	)
	cmd := &cobra.Command{
		Use:   "check <id> [item-index]",
		Short: "Mark a checklist item done (--off to uncheck), or add one with --add",
		Long: "With --add, append a checklist item. Otherwise, mark the item at the given\n" +
			"zero-based index done (or --off to uncheck it).",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// Drop empty/whitespace-only --add values so `--add ""` keeps its prior
			// meaning (flag effectively unset → fall through to the toggle path)
			// rather than appending a blank checklist item. Real items stay verbatim.
			kept := adds[:0]
			for _, s := range adds {
				if strings.TrimSpace(s) != "" {
					kept = append(kept, s)
				}
			}
			adds = kept
			verb := "checked"
			mutate := func() (*core.Task, error) {
				if len(adds) > 0 {
					return a.AddChecks(args[0], adds)
				}
				if len(args) != 2 {
					return nil, core.Validationf(args[0], "provide an item index to toggle, or --add to append")
				}
				idx, err := atoiArg("item-index", args[1])
				if err != nil {
					return nil, err
				}
				return a.Check(args[0], idx, !off)
			}
			if len(adds) > 0 {
				verb = "checklist+"
			}
			return emitMutation(a, verb, args[0], mutate)
		},
	}
	cmd.Flags().StringArrayVar(&adds, "add", nil, "append a checklist item with this text (repeatable)")
	cmd.Flags().BoolVar(&off, "off", false, "uncheck instead of check")
	return cmd
}

func newDepCmd() *cobra.Command {
	var rm bool
	cmd := &cobra.Command{
		Use:   "dep <id> <dep-id>...",
		Short: "Add one or more dependencies to a task (or remove with --rm)",
		Long: "Make <id> depend on each <dep-id> (id waits on them). Several dep-ids in one\n" +
			"call apply in a single write. With --rm, remove those dependencies instead.\n" +
			"Every dep must exist; adding is acyclic and idempotent, and the batch is\n" +
			"all-or-nothing (a bad dep-id aborts without a partial change).",
		Example: "  furrow dep t-k3m9p t-a1b2c\n" +
			"  furrow dep t-k3m9p t-a1b2c t-d4e5f    # depend on both in one write\n" +
			"  furrow dep t-k3m9p t-a1b2c --rm",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
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
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Apply several triage edits at once (lane, value, effort, labels)",
		Long: "Combine the routine triage edits into a single write: move a lane (-s), set\n" +
			"or clear the 1..5 value/effort estimates, and add/remove labels — instead of\n" +
			"running move + value + effort + label as four commands. At least one change\n" +
			"is required; an unknown lane is exit 2 with candidates (like move), and under\n" +
			"[labels].required a set that would strip the last label is refused.",
		Example: "  furrow set t-k3m9p -s ready --value 4 --effort 2 --add-label bug\n" +
			"  furrow set t-k3m9p --clear-value --rm-label wip",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o := app.SetOpts{
				AddLabels:   addLabels,
				RmLabels:    rmLabels,
				ClearValue:  clearValue,
				ClearEffort: clearEffort,
			}
			if cmd.Flags().Changed("status") {
				o.Status = &status
			}
			if cmd.Flags().Changed("value") {
				v := value
				o.Value = &v
			}
			if cmd.Flags().Changed("effort") {
				e := effort
				o.Effort = &e
			}
			return emitMutation(a, "set", args[0], func() (*core.Task, error) { return a.Set(args[0], o) })
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "move to this lane")
	cmd.Flags().IntVar(&value, "value", 0, "set the 1..5 value estimate")
	cmd.Flags().IntVar(&effort, "effort", 0, "set the 1..5 effort estimate")
	cmd.Flags().BoolVar(&clearValue, "clear-value", false, "clear the value estimate")
	cmd.Flags().BoolVar(&clearEffort, "clear-effort", false, "clear the effort estimate")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "add a label (repeatable)")
	cmd.Flags().StringArrayVar(&rmLabels, "rm-label", nil, "remove a label (repeatable)")
	cmd.MarkFlagsMutuallyExclusive("value", "clear-value")
	cmd.MarkFlagsMutuallyExclusive("effort", "clear-effort")
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
