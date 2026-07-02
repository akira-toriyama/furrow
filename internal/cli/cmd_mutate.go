package cli

import (
	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// emitMutation runs a single-task edit on id and reports it. In --json mode it
// snapshots the task before the change and prints {before, after, changed}, so
// an agent sees the effect inline without a follow-up `show`. The pre-fetch is
// skipped (and harmless) outside --json; the mutate closure is the authoritative
// source of any not-found / validation error.
func emitMutation(a *app.App, verb, id string, mutate func() (*core.Task, error)) error {
	var before *core.Task
	if flagJSON {
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
		Use:   "done <id>",
		Short: "Move a task into the done lane (stamps closed)",
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(2),
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
		add string
		off bool
	)
	cmd := &cobra.Command{
		Use:   "check <id> [item-index]",
		Short: "Toggle a checklist item, or add one with --add",
		Long: "With --add, append a checklist item. Otherwise, mark the item at the given\n" +
			"zero-based index done (or --off to uncheck it).",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			verb := "checked"
			mutate := func() (*core.Task, error) {
				if add != "" {
					return a.AddCheck(args[0], add)
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
			if add != "" {
				verb = "checklist+"
			}
			return emitMutation(a, verb, args[0], mutate)
		},
	}
	cmd.Flags().StringVar(&add, "add", "", "append a checklist item with this text")
	cmd.Flags().BoolVar(&off, "off", false, "uncheck instead of check")
	return cmd
}

func newDepCmd() *cobra.Command {
	var rm bool
	cmd := &cobra.Command{
		Use:   "dep <id> <dep-id>",
		Short: "Add a dependency to a task (or remove it with --rm)",
		Long: "Make <id> depend on <dep-id> (id waits on dep-id). With --rm, remove that\n" +
			"dependency instead. Both ids must exist; adding is acyclic and idempotent.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			verb := "dep+"
			mutate := func() (*core.Task, error) { return a.AddDep(args[0], args[1]) }
			if rm {
				verb = "dep-"
				mutate = func() (*core.Task, error) { return a.RemoveDep(args[0], args[1]) }
			}
			return emitMutation(a, verb, args[0], mutate)
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the dependency instead of adding it")
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
