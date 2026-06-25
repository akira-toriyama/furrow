package cli

import (
	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	var (
		status string
		label  string
		limit  int
	)
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tasks (canonical lane->priority->id order)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			tasks, err := a.List(app.QueryOpts{Status: status, Label: label, Limit: limit})
			if err != nil {
				return err
			}
			// An empty listing is a valid result (exit 0), not a miss.
			return emitTasks(tasks, false)
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "filter by lane")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	return cmd
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task with its markdown body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			t, body, err := a.Get(args[0])
			if err != nil {
				return err
			}
			printTaskDetail(t, body)
			return nil
		},
	}
}

func newNextCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show actionable tasks (non-terminal, all deps done)",
		Long:  "List the tasks ready to pick up: not in a terminal lane and with every\ndependency already in the done lane, in canonical order.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			tasks, err := a.Next(limit)
			if err != nil {
				return err
			}
			// "nothing actionable" is the empty arm of the contract -> exit 1.
			return emitTasks(tasks, true)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; use -n1 for just the top)")
	return cmd
}
