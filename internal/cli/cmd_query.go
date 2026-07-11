package cli

import (
	"fmt"

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
			"  furrow ls -s inbox,backlog     # comma = OR within a field\n" +
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
			return emitTasks(tasks)
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "filter by lane (comma-separated = OR, e.g. -s inbox,backlog)")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "list only drafts (tasks with no repo); bypasses the board scope")
	return cmd
}

func newShowCmd() *cobra.Command {
	var backlinks, noBody bool
	cmd := &cobra.Command{
		Use:   "show <id>...",
		Short: "Show tasks with metadata and markdown body (batch-friendly)",
		Long: "Show one or more tasks' metadata and Markdown body in a single read, in\n" +
			"input order. --json emits an array for several ids (a single id keeps the\n" +
			"historical single object); --ndjson emits one task per line at any arity;\n" +
			"the human output separates tasks with a --- line. --no-body omits the body\n" +
			"(the body_text key in JSON) — the lean metadata-only read for agents. When\n" +
			"some ids are missing, the found tasks are still emitted and the not-found\n" +
			"error carries details.missing, so a partial read is never wasted.\n" +
			"With --backlinks, also list the tasks whose body mentions each one via the\n" +
			"[[id]] notation (the local, rate-limit-free twin of GitHub's \"mentioned\n" +
			"in\"); --json adds a mentioned_by array. The scan is opt-in, so a plain\n" +
			"`show` never pays for it.",
		Example: "  furrow show t-4fq1\n" +
			"  furrow show t-4fq1 t-x2x9 --no-body --ndjson   # lean batch read\n" +
			"  furrow show t-4fq1 --backlinks",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			items, missing, err := a.GetBatch(args, !noBody)
			if err != nil {
				return err
			}
			// Single-id compat: the classic not-found error, nothing on stdout —
			// details.missing rides along so agents branch the same at any arity.
			if len(args) == 1 && len(missing) > 0 {
				fe := core.NotFound(missing[0])
				fe.Details = map[string][]string{"missing": missing}
				return fe
			}
			var mentions [][]core.Task
			if backlinks {
				ids := make([]string, len(items))
				for i := range items {
					ids[i] = items[i].Task.ID
				}
				// One board pass for the whole batch (O(board), not O(ids×board)).
				bl, err := a.BacklinksBatch(ids)
				if err != nil {
					return err
				}
				mentions = make([][]core.Task, len(items))
				for i := range items {
					mentions[i] = bl[items[i].Task.ID]
				}
			}
			emitShow(items, mentions, len(args) == 1, noBody, backlinks)
			if len(missing) > 0 {
				return &core.Error{
					Code:    core.CodeNotFound,
					Msg:     fmt.Sprintf("%d of %d ids not found", len(missing), len(items)+len(missing)),
					Details: map[string][]string{"missing": missing},
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&backlinks, "backlinks", false, "also list tasks whose body mentions this one via [[id]]")
	cmd.Flags().BoolVar(&noBody, "no-body", false, "omit the body (body_text in JSON): the lean metadata-only read")
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
			"filter on top. An empty result is healthy (nothing to pick up right now)\n" +
			"and exits 0 — the same contract as ls/revisit.",
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
			// "nothing actionable" is a healthy empty result -> exit 0 (same as
			// ls/revisit). --json/--ndjson attach a reason per task.
			return emitActionable(tasks)
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
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
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "days without update before stale (default: config [revisit].stale_days; 0 disables)")
	return cmd
}
