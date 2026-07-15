package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newLsCmd() *cobra.Command {
	var (
		status   []string
		label    string
		repo     string
		limit    int
		drafts   bool
		since    string
		until    string
		sortBy   string
		reverse  bool
		archived bool
		tree     bool
		typ      string
		progRec  bool
	)
	cmd := &cobra.Command{
		Use:     "ls [<id>]",
		Aliases: []string{"list"},
		Short:   "List tasks (canonical lane->priority->id order), or draw the hierarchy with --tree",
		Long: "List tasks in canonical lane->priority->id order (or reordered with\n" +
			"--sort). --since/--until window by the updated timestamp (a bare\n" +
			"YYYY-MM-DD, or a full RFC3339 instant; a bare --until includes the whole\n" +
			"day). --sort reorders by updated|created|value|effort (newest/highest\n" +
			"first; --reverse flips it, and an unset value/effort stays last either\n" +
			"way); with --sort, -n takes the top N of the sorted set.\n\n" +
			"--tree draws the parent hierarchy instead of a flat table: one tree per\n" +
			"top-level task, or the subtree under <id> when given. Every filter still\n" +
			"applies, and the forest is built over what matched — a task whose parent was\n" +
			"filtered out becomes a root rather than disappearing, so --tree never shows\n" +
			"fewer tasks than the same flags without it. With --tree, -n caps the number\n" +
			"of TREES (never the tasks: truncating mid-hierarchy would amputate children\n" +
			"from the trees it did show).\n\n" +
			"The tree carries the two facts a flat list can't: a ★ marks a task `furrow\n" +
			"next` would hand you right now (in a next lane, every dep done), and a\n" +
			"blocked task names what is in its way. Glyphs: ★ actionable, ✓ done, ~ parked\n" +
			"(a terminal lane that is not done), · open but not available. --json nests\n" +
			"children and adds `actionable` + `blocked_by` to each node; --ndjson streams\n" +
			"one whole tree per line.",
		Example: "  furrow ls                 # this repo's board, canonical order\n" +
			"  furrow ls -s ready --json\n" +
			"  furrow ls -s inbox,backlog     # comma = OR within a field\n" +
			"  furrow ls -l bug -r furrow\n" +
			"  furrow ls --since 2026-07-08   # touched on/after a date\n" +
			"  furrow ls --sort value -n5     # top 5 by value\n" +
			"  furrow ls --drafts        # only repo-less draft tasks\n" +
			"  furrow ls --tree          # the hierarchy, ★ = pick this up now\n" +
			"  furrow ls --tree t-k3m9p  # just what leads to (and hangs under) that goal",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("tree") {
				return cobra.MaximumNArgs(1)(cmd, args)
			}
			return cobra.NoArgs(cmd, args)
		},
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
			o.Status, o.Limit, o.Drafts = joinStatus(status), limit, drafts
			o.Sort, o.Reverse, o.Archived = sortBy, reverse, archived
			o.Type = typ
			if cmd.Flags().Changed("since") {
				ts, err := parseDateBound(since, false)
				if err != nil {
					return err
				}
				o.Since = &ts
			}
			if cmd.Flags().Changed("until") {
				ts, err := parseDateBound(until, true)
				if err != nil {
					return err
				}
				o.Until = &ts
			}
			if tree {
				root := ""
				if len(args) == 1 {
					root = args[0]
				}
				nodes, err := a.Tree(o, root, progRec)
				if err != nil {
					return err
				}
				return emitTree(a, nodes)
			}
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
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; with --sort, the top N)")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "list only drafts (tasks with no repo); bypasses the board scope")
	cmd.Flags().StringVar(&since, "since", "", "only tasks updated on/after this date (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&until, "until", "", "only tasks updated on/before this date (YYYY-MM-DD includes the whole day, or RFC3339)")
	cmd.Flags().StringVar(&sortBy, "sort", "", "reorder by updated|created|value|effort (default: canonical lane->priority->id)")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "reverse the --sort direction (oldest/lowest first; unset value/effort stay last)")
	cmd.Flags().BoolVar(&archived, "archived", false, "list from the archive store (.furrow/archive/) instead of the hot board")
	cmd.Flags().BoolVar(&tree, "tree", false, "draw the parent hierarchy (★ = actionable now); with an <id>, just that subtree")
	cmd.Flags().StringVar(&typ, "type", "", "filter by work-item type (a value from [types].order, e.g. epic; unknown = exit 2 + candidates)")
	cmd.Flags().BoolVar(&progRec, "progress-recursive", false, "with --tree, roll up container progress over the whole subtree (default: direct children only)")
	return cmd
}

// parseDateBound parses a --since/--until value: a bare YYYY-MM-DD (interpreted
// UTC) or a full RFC3339 instant. For a bare date, endOfDay advances it to
// 23:59:59 — the last whole second of the day, since furrow stamps whole-second
// timestamps — so a bare --until includes the entire day.
func parseDateBound(s string, endOfDay bool) (time.Time, error) {
	if d, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			return d.Add(24*time.Hour - time.Second), nil
		}
		return d, nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, core.Validationf("", "invalid date %q (want YYYY-MM-DD or RFC3339)", s)
}

// joinStatus flattens the repeatable -s/--status flag into the single
// comma-delimited string the lane filter parses (matchAnyLane and
// validateLaneFilter both split on ","). Every spelling converges on the same
// OR-set: `-s a,b`, `-s a -s b`, and `-s a,b -s c` all become "a,b" / "a,b,c".
// Before this, -s was a plain string flag, so a repeated -s silently kept only
// the last value — the "silent last-wins" trap (t-1bwc). Kept as a comma-join so
// the downstream split stays the one lane-filter parser (whitespace trimming,
// empty-token dropping) rather than duplicating it here.
func joinStatus(status []string) string {
	return strings.Join(status, ",")
}

func newShowCmd() *cobra.Command {
	var backlinks, noBody, archived bool
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
			"  furrow show t-4fq1 --backlinks\n" +
			"  furrow show t-4fq1 --archived                  # read a retired task",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			var (
				items   []app.ShowItem
				missing []string
			)
			if archived {
				items, missing, err = a.GetBatchArchived(args, !noBody)
			} else {
				items, missing, err = a.GetBatch(args, !noBody)
			}
			if err != nil {
				return err
			}
			// A hot-store miss might be an archived task: name that subset so the
			// agent knows to retry with --archived (the archive read already
			// looked there, so it never re-hints itself).
			var inArchive []string
			if !archived && len(missing) > 0 {
				inArchive = a.ArchivedContains(missing)
			}
			// Single-id compat: the classic not-found error, nothing on stdout —
			// details.missing rides along so agents branch the same at any arity.
			if len(args) == 1 && len(missing) > 0 {
				fe := core.NotFound(missing[0])
				fe.Msg += archivedSuffix(inArchive)
				fe.Details = missDetails(missing, inArchive)
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
					Msg:     fmt.Sprintf("%d of %d ids not found", len(missing), len(items)+len(missing)) + archivedSuffix(inArchive),
					Details: missDetails(missing, inArchive),
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&backlinks, "backlinks", false, "also list tasks whose body mentions this one via [[id]]")
	cmd.Flags().BoolVar(&noBody, "no-body", false, "omit the body (body_text in JSON): the lean metadata-only read")
	cmd.Flags().BoolVar(&archived, "archived", false, "read from the archive store (.furrow/archive/) instead of the hot board")
	cmd.MarkFlagsMutuallyExclusive("archived", "backlinks")
	return cmd
}

// missDetails builds a show/not-found error's details: always the missing ids,
// plus the subset found in the archive (so an agent can retry with --archived).
// Kept as map[string][]string so a plain miss stays the historical
// {"missing":[...]} shape and only gains an "archived" key when relevant.
func missDetails(missing, inArchive []string) map[string][]string {
	d := map[string][]string{"missing": missing}
	if len(inArchive) > 0 {
		d["archived"] = inArchive
	}
	return d
}

// archivedSuffix is the human hint appended to a not-found message when some of
// the missing ids are actually archived — empty otherwise, so a genuine miss
// keeps its classic wording.
func archivedSuffix(inArchive []string) string {
	if len(inArchive) == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d archived — retry with --archived)", len(inArchive))
}

func newNextCmd() *cobra.Command {
	var (
		label      string
		repo       string
		limit      int
		containers bool
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show actionable tasks (in the next-lanes, all deps done)",
		Long: "List the tasks ready to pick up: status in the configured next-lanes\n" +
			"([next].lanes in config.toml, default ready + in-progress) and with every\n" +
			"dependency already in the done lane, in canonical order. Container types\n" +
			"(epics — see [types].containers) are boxes, not work, so they are excluded\n" +
			"by default; pass --containers to surface a ready one too. Use --repo to\n" +
			"restrict to a repo (a unique short name works) and --label to AND a tag\n" +
			"filter on top. An empty result is healthy (nothing to pick up right now)\n" +
			"and exits 0 — the same contract as ls/revisit.",
		Example: "  furrow next               # what to pick up now\n" +
			"  furrow next -n1 --json    # just the top task, with a reason\n" +
			"  furrow next --containers  # include ready epics (boxes)\n" +
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
			o.IncludeContainers = containers
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
	cmd.Flags().BoolVar(&containers, "containers", false, "also surface ready container types (epics), which next hides by default")
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

func newStatsCmd() *cobra.Command {
	var (
		status []string
		label  string
		repo   string
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Summarize the board: counts by lane, repo, and label",
		Long: "Aggregate the tasks in scope into distributions — total, drafts, and counts\n" +
			"by lane (a complete histogram in configured lane order), by repo, and by\n" +
			"label (the used vocabulary, most-used first). It honors the same -s/-l/-r\n" +
			"scope as `ls`, so a bare `stats` describes THIS repo's slice; `stats -r ''`\n" +
			"describes the whole board — the call that learns the label/repo vocabulary\n" +
			"before guessing a -l/-r value. --json/--ndjson emit one object; an all-zero\n" +
			"board is a clean result (exit 0).",
		Example: "  furrow stats               # this repo's board at a glance\n" +
			"  furrow stats -r '' --json  # whole-board counts + full label/repo vocab\n" +
			"  furrow stats -s inbox,backlog",
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
			o.Status = joinStatus(status)
			s, err := a.Stats(o)
			if err != nil {
				return err
			}
			return emitStats(s)
		},
	}
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "scope to a repo (owner/repo or a unique short name; '' = whole board)")
	return cmd
}

func newSearchCmd() *cobra.Command {
	var (
		status []string
		label  string
		repo   string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "search <term>",
		Short: "Full-text search over task titles and bodies",
		Long: "Search every task's title and Markdown body for a case-insensitive\n" +
			"substring, in canonical order, honoring the same -s/-l/-r scope and -n\n" +
			"limit as `ls` (so a bare `search` stays within this repo's board; -r ''\n" +
			"searches the whole board). Each hit reports which field matched (title or\n" +
			"body) and a one-line snippet with the term in context; --json/--ndjson\n" +
			"emit the full task plus matched_field and snippet, so an agent skips the\n" +
			"`grep .furrow/bodies` dance. A title match never pays to read the body.\n" +
			"Several words are one literal phrase. An empty result is healthy (exit 0),\n" +
			"not a miss — the same contract as ls/next/revisit.",
		Example: "  furrow search teatest\n" +
			"  furrow search \"single marshaller\" --json\n" +
			"  furrow search sync -s backlog -n5\n" +
			"  furrow search attach -r ''        # whole board, not just this repo",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, label, repo)
			if err != nil {
				return err
			}
			o.Status, o.Limit = joinStatus(status), limit
			hits, err := a.Search(o, strings.Join(args, " "))
			if err != nil {
				return err
			}
			// A zero-match search is a valid clean result (exit 0), not a miss.
			return emitSearch(hits)
		},
	}
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label (comma-separated = OR); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	return cmd
}
