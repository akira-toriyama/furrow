package cli

import (
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	var (
		status string
		label  string
		repo   string
		limit  int
		drafts bool
	)
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tasks (canonical lane->priority->id order)",
		Example: "  furrow ls                 # this repo's board, canonical order\n" +
			"  furrow ls -s ready --json\n" +
			"  furrow ls -l bug -r furrow\n" +
			"  furrow ls --drafts        # only repo-less draft tasks",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if drafts && cmd.Flags().Changed("repo") {
				return core.Validationf("", "--drafts cannot be combined with -r/--repo (a draft has no repo)")
			}
			o, err := scopedQuery(cmd, a, label, repo)
			if err != nil {
				return err
			}
			o.Status, o.Limit, o.Drafts = status, limit, drafts
			tasks, err := a.List(o)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(tasks)); err != nil {
				return err
			}
			hintHiddenDrafts(o, a.List)
			// An empty listing is a valid result (exit 0), not a miss.
			return emitTasks(tasks, false)
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "filter by lane")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (a pure tag; ANDs with the board scope)")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "list only drafts (tasks with no repo); bypasses the board scope")
	return cmd
}

func newShowCmd() *cobra.Command {
	var backlinks bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task with its markdown body",
		Long: "Show one task's metadata and Markdown body. With --backlinks, also list the\n" +
			"tasks whose body mentions this one via the [[id]] notation (the local,\n" +
			"rate-limit-free twin of GitHub's \"mentioned in\"); --json adds a mentioned_by\n" +
			"array. The scan is opt-in, so a plain `show` never pays for it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			t, body, err := a.Get(args[0])
			if err != nil {
				return err
			}
			if !backlinks {
				printTaskDetail(t, body)
				return nil
			}
			refs, err := a.Backlinks(args[0])
			if err != nil {
				return err
			}
			printTaskDetailWithBacklinks(t, body, refs)
			return nil
		},
	}
	cmd.Flags().BoolVar(&backlinks, "backlinks", false, "also list tasks whose body mentions this one via [[id]]")
	return cmd
}

func newNextCmd() *cobra.Command {
	var (
		label string
		repo  string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show actionable tasks (in the next-lanes, all deps done)",
		Long: "List the tasks ready to pick up: status in the configured next-lanes\n" +
			"([next].lanes in config.toml, default ready + in-progress) and with every\n" +
			"dependency already in the done lane, in canonical order. Use --repo to\n" +
			"restrict to a repo (a unique short name works) and --label to AND a tag\n" +
			"filter on top.",
		Example: "  furrow next               # what to pick up now\n" +
			"  furrow next -n1 --json    # just the top task, with a reason\n" +
			"  furrow next -r furrow -l bug",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, label, repo)
			if err != nil {
				return err
			}
			o.Limit = limit
			tasks, err := a.Next(o)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(tasks)); err != nil {
				return err
			}
			hintHiddenDrafts(o, a.Next)
			// "nothing actionable" is the empty arm of the contract -> exit 1.
			// --json/--ndjson attach a reason per task (why it is actionable).
			return emitActionable(tasks)
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (a pure tag; ANDs with the board scope)")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; use -n1 for just the top)")
	return cmd
}

func newRevisitCmd() *cobra.Command {
	var (
		label     string
		repo      string
		limit     int
		staleDays int
	)
	cmd := &cobra.Command{
		Use:   "revisit",
		Short: "List open tasks needing re-evaluation (agent re-weighing signal)",
		Long: "List the open tasks worth a fresh judgment, the read-only counterpart to\n" +
			"`next`. A task surfaces when it is a draft (no repo), an estimate is unset\n" +
			"(value/effort), it has gone stale (no update within [revisit].stale_days),\n" +
			"or a dependency is already done. --json/--ndjson attach the reasons per task\n" +
			"so an agent can fix them with the setters (repo/value/effort/dep); this\n" +
			"command never mutates. Drafts surface regardless of the board scope. An\n" +
			"empty result is healthy and exits 0. Use --repo to restrict to a repo and\n" +
			"--stale-days to override the staleness window (0 disables it).",
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
			o, err := scopedQuery(cmd, a, label, repo)
			if err != nil {
				return err
			}
			o.Limit = limit
			items, err := a.Revisit(o, days)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(items)); err != nil {
				return err
			}
			// "nothing to revisit" is a valid clean result (exit 0), not a miss.
			return emitRevisit(items)
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (a pure tag; ANDs with the board scope)")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "days without update before stale (default: config [revisit].stale_days; 0 disables)")
	return cmd
}
