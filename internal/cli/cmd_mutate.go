package cli

import (
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

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
			t, err := a.Done(args[0])
			if err != nil {
				return err
			}
			printOK("done", t)
			return nil
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
			t, err := a.Move(args[0], args[1])
			if err != nil {
				return err
			}
			printOK("moved", t)
			return nil
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
			t, err := a.Reorder(args[0], prio)
			if err != nil {
				return err
			}
			printOK("reordered", t)
			return nil
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
			if add != "" {
				t, err := a.AddCheck(args[0], add)
				if err != nil {
					return err
				}
				printOK("checklist+", t)
				return nil
			}
			if len(args) != 2 {
				return core.Validationf(args[0], "provide an item index to toggle, or --add to append")
			}
			idx, err := atoiArg("item-index", args[1])
			if err != nil {
				return err
			}
			t, err := a.Check(args[0], idx, !off)
			if err != nil {
				return err
			}
			printOK("checked", t)
			return nil
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
			if rm {
				t, err := a.RemoveDep(args[0], args[1])
				if err != nil {
					return err
				}
				printOK("dep-", t)
				return nil
			}
			t, err := a.AddDep(args[0], args[1])
			if err != nil {
				return err
			}
			printOK("dep+", t)
			return nil
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the dependency instead of adding it")
	return cmd
}
