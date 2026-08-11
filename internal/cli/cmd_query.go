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
		status     []string
		label      []string
		repo       string
		epicRef    string
		limit      int
		drafts     bool
		since      string
		until      string
		sortBy     string
		reverse    bool
		archived   bool
		tree       bool
		actionable bool
		blocked    bool
		queryStr   string
	)
	cmd := &cobra.Command{
		Use:     "ls [<epic>]",
		Aliases: []string{"list"},
		Short:   "List tasks (canonical lane->priority->id order), or group them by epic with --tree",
		Long: "List tasks in canonical lane->priority->id order (or reordered with\n" +
			"--sort). --since/--until window by the updated timestamp (a bare\n" +
			"YYYY-MM-DD, or a full RFC3339 instant; a bare --until includes the whole\n" +
			"day). --sort reorders by updated|created|value|effort (newest/highest\n" +
			"first; --reverse flips it, and an unset value/effort stays last either\n" +
			"way); with --sort, -n takes the top N of the sorted set.\n\n" +
			"Every row carries a one-character state glyph: ★ actionable (a next lane,\n" +
			"every dep done — ready to pick up; `furrow next` additionally scopes to the\n" +
			"active epic, so ★ is a superset of what next hands you), ✓ done, ~ parked\n" +
			"(a terminal lane that is not done), · open but not available. Filter on it\n" +
			"with --actionable (only ★) or --blocked (only rows with an unsatisfied\n" +
			"dependency); both AND with -s/-l/-r — e.g. `-s ready --blocked` is the\n" +
			"ready rows that are actually stuck. -e <epic> keeps only that box's members\n" +
			"(strict: the unfiled pile needs `-q no:epic`, not a box name).\n" +
			"--json/--ndjson add actionable and blocked_by to each row.\n\n" +
			"--tree groups the rows by epic instead of a flat table: the active epic\n" +
			"first, then open epics by id, then closed ones, then the unfiled group —\n" +
			"or a single box's group when an <epic> argument is given. Every filter\n" +
			"still applies, and the grouping is built over what matched, so --tree\n" +
			"never shows fewer tasks than the same flags without it. With --tree, -n\n" +
			"caps the number of GROUPS (never the tasks: truncating mid-group would\n" +
			"amputate members from a box it did show). Each group carries the epic's\n" +
			"member progress ({done,total}, counted over the FULL board so a filter\n" +
			"can't under-count the box) and a stuck flag (open members but no\n" +
			"actionable one). --json nests tasks under their group and adds\n" +
			"`actionable` + `blocked_by` to each node; --ndjson streams one whole\n" +
			"group per line.",
		Example: "  furrow ls                 # this repo's board, canonical order\n" +
			"  furrow ls -s ready --json\n" +
			"  furrow ls -s inbox,backlog     # comma = OR within a field\n" +
			"  furrow ls -l bug -r furrow\n" +
			"  furrow ls --since 2026-07-08   # touched on/after a date\n" +
			"  furrow ls --sort value -n5     # top 5 by value\n" +
			"  furrow ls --drafts        # only repo-less draft tasks\n" +
			"  furrow ls --actionable    # only ★ (what `furrow next` would hand you)\n" +
			"  furrow ls -s ready --blocked   # ready rows that are actually stuck\n" +
			"  furrow ls -q 'is:actionable value:>=4'   # typed query (see -q below)\n" +
			"  furrow ls -q 'label:cli,dx -status:done updated:>=-2w'\n" +
			"  furrow ls -e travel       # only that box's members\n" +
			"  furrow ls --tree          # the board grouped by epic, ★ = ready now\n" +
			"  furrow ls --tree e-k3m9   # just that box's group",
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
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, epicRef)
			if err != nil {
				return err
			}
			o.Status, o.Limit, o.Drafts = joinOrFilter(status), limit, drafts
			o.Sort, o.Reverse, o.Archived = sortBy, reverse, archived
			o.Actionable, o.Blocked = actionable, blocked
			o.Query = queryStr
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
				groups, err := a.Tree(o, root)
				if err != nil {
					return err
				}
				// The tree is still `ls`: the did-you-mean guard and the
				// hidden-drafts hint fire under the SAME conditions as the flat
				// listing — this branch used to early-return past both, so
				// `--tree -l <repo-name>` printed an empty tree at exit 0 where
				// the flat read was exit 2 with candidates.
				matched := 0
				for _, g := range groups {
					matched += len(g.Tasks)
				}
				if err := labelDidYouMean(cmd, a, o, matched); err != nil {
					return err
				}
				hintHiddenDrafts(o, a.List, "furrow ls --drafts")
				hintCapped(len(groups), limit, "groups", func() (int, error) {
					u := o
					u.Limit = 0
					all, err := a.Tree(u, root)
					return len(all), err
				})
				return emitTree(a, groups)
			}
			items, err := a.ListItems(o)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(items)); err != nil {
				return err
			}
			hintHiddenDrafts(o, a.List, "furrow ls --drafts")
			hintCapped(len(items), limit, "", func() (int, error) {
				u := o
				u.Limit = 0
				all, err := a.List(u)
				return len(all), err
			})
			// An empty listing is a valid result (exit 0), not a miss.
			return emitListItems(a, items)
		},
	}
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; with --sort, the top N)")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "list only drafts (tasks with no repo); bypasses the board scope")
	cmd.Flags().StringVar(&since, "since", "", "only tasks updated on/after this date (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&until, "until", "", "only tasks updated on/before this date (YYYY-MM-DD includes the whole day, or RFC3339)")
	cmd.Flags().StringVar(&sortBy, "sort", "", "reorder by updated|created|value|effort (default: canonical lane->priority->id)")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "reverse the --sort direction (oldest/lowest first; unset value/effort stay last)")
	cmd.Flags().BoolVar(&archived, "archived", false, "list from the archive store (.furrow/archive/) instead of the hot board")
	cmd.Flags().BoolVar(&tree, "tree", false, "group the rows by epic (★ = actionable now); with an <epic> argument, just that box's group")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "only that epic's members (id, unique id prefix, or unique title substring; strict — no unfiled carve-out)")
	cmd.Flags().BoolVar(&actionable, "actionable", false, "only tasks 'furrow next' would hand you now (★: a next lane, every dep done); ANDs with -s/-l/-r")
	cmd.Flags().BoolVar(&blocked, "blocked", false, "only tasks with an unsatisfied dependency (a non-empty blocked_by); ANDs with -s/-l/-r")
	addQueryFlag(cmd, &queryStr)
	// A task cannot be both actionable (all deps done) and blocked (a dep undone),
	// so combining them would always be empty — refuse it rather than mislead.
	cmd.MarkFlagsMutuallyExclusive("actionable", "blocked")
	return cmd
}

// addQueryFlag registers the -q/--query typed-query flag — ONE registrar shared
// by every filtering read (ls/next/revisit/stats/search), so the five commands
// can never drift on what the query language is. The evaluator is equally
// shared (app.queryPred), so wiring a new command is exactly this call plus
// setting QueryOpts.Query.
func addQueryFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVarP(p, "query", "q", "",
		"typed query (GH-Projects style): field:value, comma=OR, -=NOT, has:/no:, "+
			"is:actionable|blocked|stale|open|closed|draft|unfiled|overdue, "+
			"ordinals+dates with >=,<,.. (updated:>=-2w), graph epic:/depends-on:/blocks:, "+
			"free text over title+body; ANDs with the other filters")
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

// joinOrFilter flattens a repeatable OR-filter flag (-s/--status, -l/--label)
// into the single comma-delimited string the app-layer filter parses: -s by
// matchAnyLane/validateLaneFilter, -l by matchAnyLabel and the DidYouMeanRepo
// guard — each already splits on ",". Every spelling converges on the same
// OR-set: `-s a,b`, `-s a -s b`, and `-s a,b -s c` all become "a,b" / "a,b,c".
// Before this both were plain string flags, so a repeated flag silently kept
// only the last value — the "silent last-wins" trap (-s: t-1bwc, -l: t-k1sr).
// A comma-join keeps the downstream split as the one filter parser (whitespace
// trimming, empty-token dropping, and for -l the single-token DidYouMeanRepo
// guard) rather than duplicating it here; the flag stays a StringArray (not a
// StringSlice), so a comma inside one value is not double-split.
func joinOrFilter(vals []string) string {
	return strings.Join(vals, ",")
}

func newShowCmd() *cobra.Command {
	var backlinks, noBody, archived bool
	cmd := &cobra.Command{
		Use:   "show <id>...",
		Short: "Show tasks or epics with metadata and markdown body (batch-friendly)",
		Long: "Show one or more tasks' metadata and Markdown body in a single read, in\n" +
			"input order. --json is ALWAYS an array, one element per found id (a single\n" +
			"id is a one-element array); --ndjson emits one task per line at any arity;\n" +
			"the human output separates tasks with a --- line. --no-body omits the body\n" +
			"(the body_text key in JSON) — the lean metadata-only read for agents. When\n" +
			"some ids are missing, the found tasks are still emitted and the not-found\n" +
			"error carries details.missing, so a partial read is never wasted.\n" +
			"An id may name an EPIC: store membership routes each one (never the id's\n" +
			"prefix), and a box is rendered as the box view `furrow epic show` prints —\n" +
			"goal, member roll-up, body — so a mixed batch's --json array carries one\n" +
			"shape per entity. --archived reads the task archive only (boxes are never\n" +
			"archived), and --backlinks names the tasks whose [[id]] links point at a\n" +
			"TASK, so a box carries no mentioned_by.\n" +
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
				entries []app.ShowEntry
				missing []string
			)
			if archived {
				// The archive holds tasks only, so this read never routes to a box.
				var items []app.ShowItem
				items, missing, err = a.GetBatchArchived(args, !noBody)
				for i := range items {
					entries = append(entries, app.ShowEntry{Task: &items[i]})
				}
			} else {
				entries, missing, err = a.ShowBatch(args, !noBody)
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
			var mentions [][]core.Task
			if backlinks {
				// Boxes are skipped: [[id]] links name tasks (core.LinkPattern is
				// built from [ids].prefix), so a box has no backlinks by construction.
				ids := make([]string, 0, len(entries))
				for i := range entries {
					if entries[i].Task != nil {
						ids = append(ids, entries[i].Task.Task.ID)
					}
				}
				// One board pass for the whole batch (O(board), not O(ids×board)).
				bl, err := a.BacklinksBatch(ids)
				if err != nil {
					return err
				}
				mentions = make([][]core.Task, len(entries))
				for i := range entries {
					if entries[i].Task != nil {
						mentions[i] = bl[entries[i].Task.Task.ID]
					}
				}
			}
			// The found entries are ALWAYS emitted first — a total miss prints []
			// under --json — so the success shape never depends on how many ids
			// resolved; only the error report below varies with the misses. (The
			// single-id read used to error before emission with a classic bare
			// object as its success shape — the arity fork the always-array rule
			// removed.)
			emitShow(a, entries, mentions, noBody, backlinks)
			if len(missing) > 0 {
				fe := &core.Error{
					Code: core.CodeNotFound,
					Kind: core.KindNotFound,
					Msg:  fmt.Sprintf("%d of %d ids not found", len(missing), len(entries)+len(missing)),
				}
				// A lone miss whose ref reads as a box says so: "ids not found"
				// for an `e-` id would send the reader looking in the wrong
				// store. Under --archived the box may well EXIST — the archive
				// simply holds tasks — so that reading gets its own sentence
				// (and a bad-usage kind: a consumer branching on not-found would
				// conclude the box is gone).
				if len(missing) == 1 {
					fe.Subject = missing[0]
					if epic, rerr := a.RefTargetsEpic(missing[0]); rerr == nil && epic {
						if archived {
							fe.Kind = core.KindValidation
							fe.Code = core.CodeValidation
							fe.Msg = fmt.Sprintf("%s is an epic — --archived reads the task archive, and boxes are never archived (drop the flag to read it)", missing[0])
						} else {
							fe.Kind = core.KindEpicNotFound
							fe.Msg = fmt.Sprintf("epic not found: %s", missing[0])
						}
					}
				}
				fe.Msg += archivedSuffix(inArchive)
				fe.Details = missDetails(missing, inArchive)
				return fe
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
		label    []string
		repo     string
		epicRef  string
		limit    int
		allEpics bool
		lanes    []string
		queryStr string
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show actionable tasks (in the next-lanes, all deps done, in the active epic)",
		Long: "List the tasks ready to pick up: status in the configured next-lanes\n" +
			"([next].lanes in config.toml, default ready + in-progress) and with every\n" +
			"dependency already in the done lane, in canonical order.\n\n" +
			"On a board that has declared at least one epic, the result is ALSO scoped\n" +
			"to the ACTIVE epic for the repo, plus tasks filed under no epic — the\n" +
			"active box's tasks first, then the unfiled pile. With no active epic the\n" +
			"result is deliberately EMPTY (exit 0, a stderr hint): pick a box with\n" +
			"`furrow epic activate <id>`. -e <epic> reads one box explicitly (strict —\n" +
			"no unfiled carve-out) and --all-epics ignores the scope entirely; a board\n" +
			"with no epics at all is not participating and behaves classically. Use\n" +
			"--repo to restrict to a repo (a unique short name works) and --label to\n" +
			"AND a tag filter on top. An empty result is healthy (nothing to pick up\n" +
			"right now) and exits 0 — the same contract as ls/revisit.\n\n" +
			"--lanes <csv> overrides which lanes count as \"now\" for THIS call only,\n" +
			"leaving [next].lanes in config untouched (non-destructive): `next --lanes\n" +
			"backlog,ready` surfaces a no-dependency backlog task you could start now\n" +
			"without first promoting it. The deps-done half is unchanged, so --lanes\n" +
			"widens which lanes qualify, not what \"ready\" means; an unknown lane is exit\n" +
			"2 with the configured lanes in candidates (like -s). --json's reason.in_next_lane\n" +
			"names the lane each task matched, so a --lanes-included one is distinguishable.\n\n" +
			"-q ANDs a typed query onto the actionable set — the same language as `ls -q`\n" +
			"(qualifiers, dates, has:/no:, is:, graph edges, free text over title+body) —\n" +
			"so `next -q 'value:>=4'` is \"what can I pick up now that is worth a lot\".",
		Example: "  furrow next               # what to pick up now (active epic + unfiled)\n" +
			"  furrow next -n1 --json    # just the top task, with a reason\n" +
			"  furrow next -e travel     # one box explicitly, active or not\n" +
			"  furrow next --all-epics   # ignore the active-epic scope\n" +
			"  furrow next --lanes backlog,ready   # temporarily widen the lanes considered\n" +
			"  furrow next -q 'value:>=4 -label:chore'   # ready AND worth it\n" +
			"  furrow next -r furrow -l bug",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, epicRef)
			if err != nil {
				return err
			}
			o.Limit = limit
			o.AllEpics = allEpics
			o.Query = queryStr
			// --lanes is a one-shot override of [next].lanes: the same comma-OR /
			// repeated union as -s (a StringArray split on ",", trimmed, empties
			// dropped). Next validates the tokens against the configured lanes, so a
			// typo is exit 2 + candidates (never a silent empty result).
			if cmd.Flags().Changed("lanes") {
				for _, v := range lanes {
					for _, tok := range strings.Split(v, ",") {
						if tok = strings.TrimSpace(tok); tok != "" {
							o.Lanes = append(o.Lanes, tok)
						}
					}
				}
			}
			tasks, err := a.Next(o)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(tasks)); err != nil {
				return err
			}
			hintHiddenDrafts(o, a.Next, "furrow ls --drafts")
			hintCapped(len(tasks), limit, "", func() (int, error) {
				u := o
				u.Limit = 0
				all, err := a.Next(u)
				return len(all), err
			})
			hintEpicScope(a, o, tasks)
			hintDue(a, o)
			// "nothing actionable" is a healthy empty result -> exit 0 (same as
			// ls/revisit). --json/--ndjson attach a reason per task.
			return emitActionable(a, tasks)
		},
	}
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "read one box explicitly, active or not (id, unique id prefix, or unique title substring; strict — no unfiled carve-out)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all; use -n1 for just the top)")
	cmd.Flags().BoolVar(&allEpics, "all-epics", false, "ignore the active-epic scope and consider the whole board")
	cmd.Flags().StringArrayVar(&lanes, "lanes", nil, "override [next].lanes for THIS call (OR; comma-separated or repeated; unknown lane = exit 2 + candidates); config untouched")
	addQueryFlag(cmd, &queryStr)
	return cmd
}

func newBriefCmd() *cobra.Command {
	var (
		label     []string
		repo      string
		limit     int
		staleDays int
	)
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "One-shot session-orient read: due band, active/pinned epics, next picks with bodies, blocked, revisit, drafts",
		Long: "Answer \"where am I?\" in ONE process at session start — the sync → next →\n" +
			"show ritual folded into a single read. Each section keeps the\n" +
			"contract of the command it summarizes: `due` LEADS the read —\n" +
			"{overdue, today}, longest-overdue first, board-wide as to epics and\n" +
			"lane-filter-free (dated work is usually parked outside the focus; a date\n" +
			"is the one thing on the board that EXPIRES), omitted when nothing has\n" +
			"arrived; `active` = the open+active epic(s)\n" +
			"next scopes to, with their member roll-up (epics_declared tells a\n" +
			"non-participating board apart from \"nothing active, so next is\n" +
			"deliberately empty\"); `pinned` = the open pinned channel(s) whose\n" +
			"actionable tasks lead next regardless of the active scope (shown even\n" +
			"with nothing active; pinned_quiet counts the ones with no open work);\n" +
			"`next` = the top -n actionable tasks\n" +
			"(next's predicate) WITH their bodies (show's body_text — the follow-up read\n" +
			"folded in), plus next_total, the uncapped count, so the cap never hides the\n" +
			"queue size; `blocked` = next-lane tasks with an unsatisfied dep and their\n" +
			"blocked_by (started or queued work that plain `next` deliberately hides);\n" +
			"`revisit` = the summary sync reports ({dep_done, stale, …} id arrays);\n" +
			"`drafts` = the repo-less count, board-wide by definition (a draft has no\n" +
			"repo, so no scope can own it); `lint` = sync's error-count ride-along\n" +
			"(omitted when clean). Scope with -r/-l like every read; human mode\n" +
			"is a compact dashboard without bodies (prose is --json's payload). Read-only:\n" +
			"it never touches git — run `furrow sync && furrow brief` to orient on a\n" +
			"shared board.",
		Example: "  furrow sync && furrow brief   # session start, one orientation read\n" +
			"  furrow brief --json -n1       # just the top pick, with its body\n" +
			"  furrow brief -r furrow",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, "")
			if err != nil {
				return err
			}
			days := a.Cfg.RevisitStaleDays
			if cmd.Flags().Changed("stale-days") {
				days = staleDays
			}
			b, err := a.Brief(o, limit, days)
			if err != nil {
				return err
			}
			scope := o.Repo
			if scope == "" {
				scope = o.ScopeRepo
			}
			printBrief(b, scope)
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 3, "how many next picks to include, bodies attached (0 = all; next_total is never capped)")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "days without update before stale (default: config [revisit].stale_days; 0 disables)")
	return cmd
}

func newRevisitCmd() *cobra.Command {
	var (
		label     []string
		repo      string
		epicRef   string
		limit     int
		staleDays int
		queryStr  string
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
			"--stale-days to override the staleness window (0 disables it).\n\n" +
			"-q ANDs a typed query onto the surfaced set — the same language as `ls -q`\n" +
			"— narrowing WHICH flagged tasks come back (`revisit -q 'label:cli'`); a\n" +
			"task still needs at least one revisit signal to appear. Within -q,\n" +
			"is:stale uses this call's staleness window (--stale-days included), so the\n" +
			"flag and the query cannot disagree.",
		Example: "  furrow revisit                 # everything worth a fresh look\n" +
			"  furrow revisit --json -n5\n" +
			"  furrow revisit -q 'label:cli'  # only the flagged tasks tagged cli\n" +
			"  furrow revisit --stale-days 7  # tighter staleness window",
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
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, epicRef)
			if err != nil {
				return err
			}
			o.Limit = limit
			o.Query = queryStr
			items, err := a.Revisit(o, days)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(items)); err != nil {
				return err
			}
			hintCapped(len(items), limit, "", func() (int, error) {
				u := o
				u.Limit = 0
				all, err := a.Revisit(u, days)
				return len(all), err
			})
			// "nothing to revisit" is a valid clean result (exit 0), not a miss.
			return emitRevisit(a, items)
		},
	}
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "only that epic's members (id, unique id prefix, or unique title substring; strict)")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "days without update before stale (default: config [revisit].stale_days; 0 disables)")
	addQueryFlag(cmd, &queryStr)
	return cmd
}

func newStatsCmd() *cobra.Command {
	var (
		status   []string
		label    []string
		repo     string
		epicRef  string
		queryStr string
		since    string
		until    string
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
			"board is a clean result (exit 0).\n\n" +
			"-q ANDs a typed query onto the scope — the same language as `ls -q` — so\n" +
			"the distributions describe the QUERIED slice: `stats -q is:stale` is the\n" +
			"shape of the stale board, `stats -q 'no:effort'` where the unestimated\n" +
			"work sits.\n\n" +
			"--since/--until window by the updated timestamp exactly like `ls` (a bare\n" +
			"YYYY-MM-DD, or a full RFC3339 instant; a bare --until includes the whole\n" +
			"day) AND add a `window` section reporting the FLOW inside those bounds:\n" +
			"the ids created there and the ids closed there (counts are the lengths).\n" +
			"Flow membership is decided by `created`/`closed` alone — a task closed in\n" +
			"the window and touched after it still counts — and the scan unions the\n" +
			"archive store, so an archive sweep cannot deflate `closed`. That section\n" +
			"is the machine side of the session budget check (created ≤ closed − 1):\n" +
			"declare counts in prose, verify them against `stats -r '' --since <t0>`.",
		Example: "  furrow stats               # this repo's board at a glance\n" +
			"  furrow stats -r '' --json  # whole-board counts + full label/repo vocab\n" +
			"  furrow stats -q is:stale   # where the stale work sits\n" +
			"  furrow stats -r '' --since 2026-08-02 --json   # today's created/closed flow\n" +
			"  furrow stats -s inbox,backlog",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, epicRef)
			if err != nil {
				return err
			}
			o.Status = joinOrFilter(status)
			o.Query = queryStr
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
			s, err := a.Stats(o)
			if err != nil {
				return err
			}
			// The same -l did-you-mean guard as ls/next/revisit: an explicit tag
			// filter that zeroed EVERYTHING (drafts included — they honor -l too)
			// while uniquely naming a repo is exit 2 + candidates, never a
			// confident all-zero histogram.
			if err := labelDidYouMean(cmd, a, o, s.Total+s.Drafts); err != nil {
				return err
			}
			return emitStats(s)
		},
	}
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "scope to a repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "only that epic's members (id, unique id prefix, or unique title substring; strict)")
	cmd.Flags().StringVar(&since, "since", "", "window on/after this date (YYYY-MM-DD or RFC3339): filters by updated like 'ls' and adds the created/closed flow section")
	cmd.Flags().StringVar(&until, "until", "", "window on/before this date (YYYY-MM-DD includes the whole day, or RFC3339)")
	addQueryFlag(cmd, &queryStr)
	return cmd
}

func newSearchCmd() *cobra.Command {
	var (
		status   []string
		label    []string
		repo     string
		epicRef  string
		limit    int
		queryStr string
		archived bool
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
			"not a miss — the same contract as ls/next/revisit.\n\n" +
			"-q ANDs a typed query onto the hits — the same language as `ls -q` — so\n" +
			"`search sync -q 'is:open label:cli'` greps only the open cli-tagged tasks.\n" +
			"(A bare `-q` free-text term IS this search: `ls -q foo` finds what `furrow\n" +
			"search foo` finds. `search` remains the command that reports match field +\n" +
			"snippet; the term argument stays required.)\n\n" +
			"--archived searches the sibling archive store INSTEAD of the hot board —\n" +
			"the same meaning it has on ls/show, so digging up why something was done\n" +
			"stops falling back to grepping .furrow/archive/bodies by hand.",
		Example: "  furrow search teatest\n" +
			"  furrow search \"single marshaller\" --json\n" +
			"  furrow search sync -s backlog -n5\n" +
			"  furrow search sync -q 'is:open label:cli'   # only open cli-tagged hits\n" +
			"  furrow search attach -r ''        # whole board, not just this repo\n" +
			"  furrow search reslice --archived  # the retired tasks, not the hot board",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			o, err := scopedQuery(cmd, a, joinOrFilter(label), repo, epicRef)
			if err != nil {
				return err
			}
			o.Status, o.Limit = joinOrFilter(status), limit
			o.Query = queryStr
			o.Archived = archived
			term := strings.Join(args, " ")
			hits, err := a.Search(o, term)
			if err != nil {
				return err
			}
			if err := labelDidYouMean(cmd, a, o, len(hits)); err != nil {
				return err
			}
			// The same hidden-drafts disclosure as ls/next. Search has no --drafts
			// flag, so the adapter re-runs the search drafts-only and the remedy
			// names the escape hatch search does have (-r '').
			hintHiddenDrafts(o, func(q app.QueryOpts) ([]core.Task, error) {
				hs, err := a.Search(q, term)
				if err != nil {
					return nil, err
				}
				ts := make([]core.Task, len(hs))
				for i := range hs {
					ts[i] = hs[i].Task
				}
				return ts, nil
			}, "furrow search ... -r '' includes them")
			hintCapped(len(hits), limit, "", func() (int, error) {
				u := o
				u.Limit = 0
				all, err := a.Search(u, term)
				return len(all), err
			})
			// A zero-match search is a valid clean result (exit 0), not a miss.
			return emitSearch(hits)
		},
	}
	cmd.Flags().StringArrayVarP(&status, "status", "s", nil, "filter by lane (OR; comma-separated or repeated -s, e.g. -s inbox,backlog or -s inbox -s backlog)")
	cmd.Flags().StringArrayVarP(&label, "label", "l", nil, "filter by label (OR; comma-separated or repeated -l, e.g. -l bug,urgent or -l bug -l urgent); a pure tag that ANDs with the board scope")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by repo (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "only that epic's members (id, unique id prefix, or unique title substring; strict)")
	cmd.Flags().BoolVar(&archived, "archived", false, "search the archive store instead of the hot board (same meaning as on ls/show)")
	addQueryFlag(cmd, &queryStr)
	return cmd
}
