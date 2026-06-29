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
	var (
		label string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show actionable tasks (in the next-lanes, all deps done)",
		Long: "List the tasks ready to pick up: status in the configured next-lanes\n" +
			"([next].lanes in config.toml, default ready + in-progress) and with every\n" +
			"dependency already in the done lane, in canonical order. Use --label to\n" +
			"restrict to a single label (e.g. a repo name in a shared tracker).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			tasks, err := a.Next(label, limit)
			if err != nil {
				return err
			}
			// "nothing actionable" is the empty arm of the contract -> exit 1.
			// --json/--ndjson attach a reason per task (why it is actionable).
			return emitActionable(tasks)
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; use -n1 for just the top)")
	return cmd
}

func newRevisitCmd() *cobra.Command {
	var (
		label     string
		limit     int
		staleDays int
	)
	cmd := &cobra.Command{
		Use:   "revisit",
		Short: "List open tasks needing re-evaluation (agent re-weighing signal)",
		Long: "List the open tasks worth a fresh judgment, the read-only counterpart to\n" +
			"`next`. A task surfaces when an estimate is unset (value/effort), it has\n" +
			"gone stale (no update within [revisit].stale_days), or a dependency is\n" +
			"already done. --json/--ndjson attach the reasons per task so an agent can\n" +
			"fix them with the setters (value/effort/dep); this command never mutates.\n" +
			"An empty result is healthy and exits 0. Use --label to restrict to a repo\n" +
			"and --stale-days to override the staleness window (0 disables it).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			days := a.Cfg.RevisitStaleDays
			if cmd.Flags().Changed("stale-days") {
				days = staleDays
			}
			items, err := a.Revisit(label, days, limit)
			if err != nil {
				return err
			}
			// "nothing to revisit" is a valid clean result (exit 0), not a miss.
			return emitRevisit(items)
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "days without update before stale (default: config [revisit].stale_days; 0 disables)")
	return cmd
}
