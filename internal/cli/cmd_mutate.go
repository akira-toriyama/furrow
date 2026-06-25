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
