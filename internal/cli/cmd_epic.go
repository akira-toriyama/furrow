package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/v10/internal/app"
	"github.com/akira-toriyama/furrow/v10/internal/core"
	"github.com/spf13/cobra"
)

// newEpicCmd is the box command tree. Epics are a separate entity from tasks —
// their own shard, their own id prefix — so they get their own subtree rather
// than more flags on `add`/`set`: `furrow epic <verb>` reads as "operate on a
// box", and the two entities' verbs never collide.
func newEpicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "epic",
		Short: "Manage epics (boxes of work): add, ls, show, set, activate, deactivate, done, reopen, dep, rm",
		Long: "An epic is a box of work: a first-class entity with a goal, free-form\n" +
			"meta, and member tasks. At most ONE epic may be active per repo, and\n" +
			"`furrow next` scopes to it — that is what keeps a session from picking the\n" +
			"cheapest task on the whole board every time.\n\n" +
			"furrow never opens a box on its own: `epic done` clears the active flag\n" +
			"and activates nothing, and `furrow lint` warns (epic-no-active) until a\n" +
			"human does. It does PROPOSE — `epic done` names the successor the box\n" +
			"DECLARED (`epic set --successor`, the human's plan written ahead), and for\n" +
			"what no declaration covers, done/deactivate name the previous active box\n" +
			"in the same repo (computed from the activation log, `unknown` when no\n" +
			"record decides it) — but activating it stays your call.",
		// `furrow epic` alone prints help; an unknown subcommand is exit 2 with
		// the known names in candidates, the root's and `config`'s contract.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return unknownSubcommandErr(cmd, args[0])
		},
	}
	cmd.AddCommand(newEpicAddCmd(), newEpicLsCmd(), newEpicShowCmd(), newEpicSetCmd(),
		newEpicActivateCmd(), newEpicDeactivateCmd(), newEpicDoneCmd(), newEpicReopenCmd(), newEpicDepCmd(), newEpicRmCmd())
	return cmd
}

func newEpicAddCmd() *cobra.Command {
	var (
		goal   string
		meta   []string
		labels []string
		repos  []string
		body   bodyFlags
		anchor string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create an epic (never active — open it with `epic activate`)",
		Long: "Create a box. It is never active on creation: opening one is a separate,\n" +
			"deliberate act, so `epic add` can never silently change what `next` hands\n" +
			"out.\n\n" +
			"--goal is the closing condition in one line, and is OPTIONAL: a box whose\n" +
			"title already says it (\"make curry\") gains nothing from restating it, so\n" +
			"furrow neither requires a goal nor lints its absence.\n\n" +
			"--meta k=v is free-form: furrow stores it, round-trips it, and never\n" +
			"interprets or indexes it. Read it back with `epic show --json | jq`.\n\n" +
			"--anchor YYYY-MM-DD is the box's own calendar day — the event, the\n" +
			"release, the deadline its members' dates count back from. Tasks whose\n" +
			"due follows it are marked with `set --anchor <this epic>`; when the day\n" +
			"moves (`epic set --anchor <new day>`) every follower's due moves with\n" +
			"it. See `furrow epic set --help`.\n\n" +
			"`epic add` only CREATES a box. A task joins one with `furrow set <task>\n" +
			"-e <epic>` (or `add -e` at creation); an `epic add <epic> <task>` is exit 2\n" +
			"naming that call in candidates when both resolve on the board.",
		Args: epicAddArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := emptyFlagErr(cmd, "", "repo", "label", "meta"); err != nil {
				return err
			}
			a, err := openApp()
			if err != nil {
				return err
			}
			m, err := parseMetaPairs(meta)
			if err != nil {
				return err
			}
			if err := emptyFlagRemedyErr(cmd, "", flagRemedy{"anchor", "pass a calendar day (2026-11-21), or drop the flag to create the box without one"}); err != nil {
				return err
			}
			bodyText, err := body.text(cmd, true)
			if err != nil {
				return err
			}
			e, err := a.EpicAdd(args[0], app.EpicAddOpts{
				Goal: goal, Meta: m, Labels: labels, Repos: repos, Body: bodyText, Anchor: anchor,
			})
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(e)
				return nil
			}
			fmt.Fprintf(out, "%s  %s\n", e.ID, e.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "the closing condition, in one line (optional)")
	cmd.Flags().StringArrayVar(&meta, "meta", nil, "free-form key=value furrow never interprets (repeatable)")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVarP(&repos, "repo", "r", nil, "repo this box spans (owner/repo, or a unique short name; repeatable; comma-separated)")
	body.register(cmd, "initial body markdown itself, never a path ('-' reads stdin; default: a heading from the title)")
	cmd.Flags().StringVar(&anchor, "anchor", "", "the box's calendar day (YYYY-MM-DD) that its followers' dues are derived from")
	return cmd
}

// epicAddArgs is `epic add`'s arity check with the two first-time mistakes
// spelled out (t-5r9g): an unquoted multi-word title, and `epic add <epic>
// <task>` meant as "put this task into that box" — which is `set -e`, named as
// the exact call in candidates when one argument is a task on the board and
// another resolves to a box, in either order (the shape-only guess used to
// propose `set <epic> -e <task>` for the reversed spelling, and a call for ids
// that did not exist).
func epicAddArgs(_ *cobra.Command, args []string) error {
	if len(args) == 1 {
		return nil
	}
	e := core.Validationf("", "`epic add` takes exactly one argument, the new box's title (quote a title with spaces), received %d; a task joins a box with `furrow set <task> -e <epic>`, not with `epic add`", len(args))
	if len(args) >= 2 {
		if task, epic, ok := epicAddRedirect(args); ok {
			e.Candidates = []string{"furrow set " + task + " -e " + epic}
		}
	}
	return e
}

// epicAddRedirect finds the one task id and the one box ref among args, both
// resolved against the board; ok is false when the board cannot be opened or
// the pair is not there.
func epicAddRedirect(args []string) (task, epic string, ok bool) {
	a, err := openApp()
	if err != nil {
		return "", "", false
	}
	var tasks, epics []string
	for _, arg := range args {
		if _, _, err := a.Get(arg); err == nil {
			tasks = append(tasks, arg)
			continue
		}
		if id, err := a.ResolveEpic(arg); err == nil {
			epics = append(epics, id)
		}
	}
	if len(tasks) != 1 || len(epics) != 1 {
		return "", "", false
	}
	return tasks[0], epics[0], true
}

func newEpicLsCmd() *cobra.Command {
	var (
		all bool
		f   filterFlags
	)
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List epics (open only by default), active first",
		Long: "List the boxes: the active one first, then pinned open, then open by id,\n" +
			"then closed (with --all). The board's repo scope applies exactly as on the\n" +
			"task reads: inside a scoped checkout a bare `epic ls` lists this repo's\n" +
			"boxes (the population `furrow brief`'s epic header draws from), an explicit\n" +
			"-r overrides it, and -r '' is the whole-board escape — with a stderr note\n" +
			"whenever the scope hid boxes. -l is the task reads' tag filter (comma = OR,\n" +
			"repeatable unions).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// The task reads' scope rule, applied to boxes: EpicList takes one
			// repo filter, so the board scope and an explicit -r collapse into it.
			scope, repo, err := repoScope(cmd, a, f.repoValue())
			if err != nil {
				return err
			}
			if repo == "" {
				repo = scope
			}
			o := app.EpicQueryOpts{All: all, Label: joinOrFilter(f.label), Repo: repo}
			// One uncapped read; -n is a slice of it, so the cap note needs no
			// second query.
			matched, err := a.EpicList(o)
			if err != nil {
				return err
			}
			items := matched
			if f.limit > 0 && len(items) > f.limit {
				items = items[:f.limit]
			}
			// Disclose what the repo scope hid: a read never narrows silently
			// (the drafts-hint rule, applied to boxes).
			if repo != "" {
				u := o
				u.Repo = ""
				if board, err := a.EpicList(u); err == nil && len(board) > len(matched) {
					fmt.Fprintf(errOut, "note: %d box(es) outside %s hidden — furrow epic ls -r ''\n", len(board)-len(matched), repo)
				}
			}
			hintCapped(len(items), f.limit, "", func() (int, error) { return len(matched), nil })
			return emitEpicList(a, items)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include closed epics")
	addFilterFlags(cmd, &f, want("label"), want("repo"), want("limit"))
	return cmd
}

func newEpicShowCmd() *cobra.Command {
	var noBody bool
	cmd := &cobra.Command{
		Use:   "show <epic>",
		Short: "Show one epic: goal, meta, progress, member tasks, and its body",
		Long: "Show one epic: goal, meta, progress, member tasks, and its body.\n\n" +
			"--no-body omits the body (body_text under --json) — the lean metadata read,\n" +
			"the same switch `furrow show <epic-id> --no-body` honors.\n\n" +
			"A box with no non-terminal member left and a parked member whose due is\n" +
			"still ahead is WAITING: it prints `⏳ waiting until <due> (<task>)` for the\n" +
			"earliest such due (`waiting: {until, task}` in --json), and revisit's\n" +
			"epic_all_done stays quiet while it waits.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			d, err := a.EpicShow(args[0])
			if err != nil {
				return err
			}
			return emitEpicDetail(a, d, noBody)
		},
	}
	cmd.Flags().BoolVar(&noBody, "no-body", false, "omit the body (body_text under --json)")
	return cmd
}

func newEpicSetCmd() *cobra.Command {
	var (
		anchor         string
		clearAnchor    bool
		successor      string
		clearSuccessor bool
		yes            bool
	)
	var (
		title     string
		goal      string
		meta      []string
		rmMeta    []string
		addLabels []string
		rmLabels  []string
		addRepos  []string
		rmRepos   []string
		standing  bool
		pinned    bool
	)
	cmd := &cobra.Command{
		Use:   "set <epic>",
		Short: "Edit an epic's title, goal, meta, labels, repos, anchor day, successor, or its standing/pinned declarations",
		Long: "Edit a box's metadata.\n\n" +
			"--anchor YYYY-MM-DD sets or MOVES the box's calendar day — the one date\n" +
			"its derived dates count back from (an event, a release, a deadline).\n" +
			"Tasks whose due follows it carry `anchor: <this epic>` (`furrow set\n" +
			"--anchor <epic>`; `-q anchor:<epic>` lists them, `-q has:due no:anchor`\n" +
			"the dates the other side or the calendar fixed). A FIRST set moves\n" +
			"nothing: the dues the followers already carry are their distances from\n" +
			"the day being declared. A later set that changes the day is the\n" +
			"reschedule: every open follower's due shifts by the calendar-day delta\n" +
			"(--due-shift's rule, so a 13:20 stays 13:20 across a DST boundary), a\n" +
			"done follower keeps its due (history) and is listed as kept, and the\n" +
			"write is all-or-nothing — a follower with no due or with a repeat rule\n" +
			"refuses the whole move, every such id in details.undated /\n" +
			"details.repeating (lint names them first: anchor-undated,\n" +
			"anchor-on-repeat). A move PREVIEWS until --yes, one row per follower\n" +
			"with its due before and after ({dry_run: true, anchor: {epic, from, to,\n" +
			"days, moves, kept}} in JSON); with --yes, or when no due moves, the write\n" +
			"lands and the envelope carries the same object under `anchor`. The same\n" +
			"day again is a no-op. --clear-anchor drops the day; followers keep their\n" +
			"pointer, are named in the envelope's anchor.followers and warned by lint\n" +
			"(anchor-unset) until they are re-pointed or cleared (`furrow set -q\n" +
			"'anchor:<epic>' -r '' --clear-anchor --yes`). The derivation itself is\n" +
			"never stored: a due stays one absolute instant, and `show` prints a\n" +
			"follower's D-N against the box's day.\n\n" +
			"--successor <epic> declares the box to open AFTER this one closes — the\n" +
			"plan written ahead, where `epic dep` is the wait: a dep sits on the box\n" +
			"that waits (B opens after A closes is B's edge, and B may hold many), a\n" +
			"successor sits on the box that closes (after A, open B is A's\n" +
			"declaration, one per box), and a chain A→B→C is a queue. It is stored as\n" +
			"the id (`successor` in the shard and in --json) and resolved like every\n" +
			"epic reference. Nothing is opened by it: `epic done` PROPOSES the\n" +
			"declared box, walking past one that already closed along its own chain,\n" +
			"and says why when it cannot simply be activated (already active, its\n" +
			"slot held, no repo); `furrow brief` names it while nothing is active.\n" +
			"Refused (exit 2): the box itself, a closed target (`epic reopen` it or\n" +
			"name another), a loop through the chain, a target this box already waits\n" +
			"on (and `epic dep` refuses the reverse: waiting on the declared successor),\n" +
			"and a declaration on a standing box — or --standing on a box that holds\n" +
			"one — since a box that never closes has no \"after\". Allowed on purpose:\n" +
			"an active target (resolved at the\n" +
			"close), several boxes declaring the same successor (the first to close\n" +
			"proposes it; a real join is `epic dep B A1 A2`), and a standing target\n" +
			"(a mandate box can be what opens next). --clear-successor drops the plan.\n" +
			"`furrow lint` backstops what a merge lands on an OPEN declarer:\n" +
			"epic-successor-missing and epic-successor-dep (errors),\n" +
			"epic-successor-closed and epic-successor-standing (warns); a closed\n" +
			"declarer keeps the pointer as history. `epic rm` counts a declaration as\n" +
			"a reference.\n\n" +
			"--standing and --pinned (v7) are the two PERMANENT-\n" +
			"channel declarations: a standing box is exempt from the lifecycle nags\n" +
			"(revisit's epic_all_done / epic_dep_done / epic_stuck) and instead\n" +
			"carries the review cadence — once `furrow review <epic-ref>` has stamped\n" +
			"it, epic_review_due fires when that review outlives [review]'s staleness\n" +
			"threshold (v9) — and a pinned\n" +
			"box's actionable tasks lead `next`/`brief` regardless of the active scope.\n" +
			"They are orthogonal to each other and to activate (a mandate box is\n" +
			"standing + pinned; a parking lot is standing only). Clear one explicitly\n" +
			"with --standing=false / --pinned=false — an omitted flag never touches the\n" +
			"stored value. Which boxes carry them is an operating convention, not\n" +
			"furrow's: furrow stays name-independent.",
		Example: "  furrow epic add \"会場\" --anchor 2026-11-21             # the event day\n" +
			"  furrow set -q 'epic:e-v0zdq has:due' --anchor e-v0zdq --yes  # mark the dues that follow it\n" +
			"  furrow epic set e-v0zdq --anchor 2026-11-28              # the day moved: preview every follower's old → new due\n" +
			"  furrow epic set e-v0zdq --anchor 2026-11-28 --yes        # apply, one write\n" +
			"  furrow epic set e-v0zdq --clear-anchor                   # drop the day (followers keep their pointer, disclosed)\n" +
			"  furrow epic set e-k3m9p --successor e-a1b2c             # after this box closes, propose that one\n" +
			"  furrow epic set e-k3m9p --clear-successor               # drop the plan",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := emptyFlagErr(cmd, args[0], "add-label", "rm-label", "add-repo", "rm-repo", "meta", "rm-meta"); err != nil {
				return err
			}
			a, err := openApp()
			if err != nil {
				return err
			}
			m, err := parseMetaPairs(meta)
			if err != nil {
				return err
			}
			if err := emptyFlagRemedyErr(cmd, args[0],
				flagRemedy{"anchor", "pass a calendar day (2026-11-21), or use --clear-anchor to remove it"},
				flagRemedy{"successor", "pass the box to open after this one, or use --clear-successor to drop the plan"},
			); err != nil {
				return err
			}
			if yes && !cmd.Flags().Changed("anchor") {
				return core.Validationf(args[0], "--yes confirms the follower dues an --anchor move shifts; without --anchor there is nothing to preview")
			}
			o := app.EpicSetOpts{
				SetMeta: m, RmMeta: rmMeta,
				AddLabels: addLabels, RmLabels: rmLabels,
				AddRepos: addRepos, RmRepos: rmRepos,
				ClearAnchor: clearAnchor, ClearSuccessor: clearSuccessor,
			}
			if cmd.Flags().Changed("anchor") {
				o.Anchor = &anchor
			}
			if cmd.Flags().Changed("successor") {
				o.Successor = &successor
			}
			if cmd.Flags().Changed("title") {
				o.Title = &title
			}
			if cmd.Flags().Changed("goal") {
				o.Goal = &goal
			}
			if cmd.Flags().Changed("standing") {
				o.Standing = &standing
			}
			if cmd.Flags().Changed("pinned") {
				o.Pinned = &pinned
			}
			// A move is a write to every follower, so it previews like a `set -q`
			// selection until --yes: the plan is computed the way the write
			// computes it (PlanAnchor), and a plan that moves nothing — a first
			// set, the same day — needs no confirmation.
			if o.Anchor != nil && !yes {
				plan, err := a.PlanAnchor(args[0], anchor)
				if err != nil {
					return err
				}
				if len(plan.Moves) > 0 {
					// The other edits on the line ride the same --yes; a preview
					// that showed only the move hid them (t-r6vt).
					var pending []string
					for _, f := range []string{"title", "goal", "standing", "pinned", "successor", "clear-successor", "add-label", "rm-label", "add-repo", "rm-repo", "meta", "rm-meta"} {
						if cmd.Flags().Changed(f) {
							pending = append(pending, f)
						}
					}
					emitAnchorPreview(a, plan, pending)
					return nil
				}
			}
			before, after, plan, err := a.EpicSet(args[0], o)
			if err != nil {
				return err
			}
			var extra map[string]any
			if plan != nil {
				extra = map[string]any{"anchor": plan}
			}
			if err := emitEpicMutationResult(a, before, after, extra); err != nil {
				return err
			}
			if plan != nil {
				printAnchorReport(a, plan)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "rename the epic")
	cmd.Flags().StringVar(&goal, "goal", "", "set the closing condition (\"\" clears it)")
	cmd.Flags().StringArrayVar(&meta, "meta", nil, "set a free-form key=value (repeatable)")
	cmd.Flags().StringArrayVar(&rmMeta, "rm-meta", nil, "remove a meta key (repeatable)")
	cmd.Flags().StringSliceVar(&addLabels, "add-label", nil, "add a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&rmLabels, "rm-label", nil, "remove a label (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&addRepos, "add-repo", nil, "attach a repo (owner/repo, or a unique short name; repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&rmRepos, "rm-repo", nil, "detach a repo (same forms; repeatable; comma-separated)")
	cmd.Flags().BoolVar(&standing, "standing", false, "declare a permanent box (exempt from epic_all_done/epic_dep_done; --standing=false clears)")
	cmd.Flags().BoolVar(&pinned, "pinned", false, "surface this box's actionable tasks in next/brief regardless of the active scope (--pinned=false clears)")
	cmd.Flags().StringVar(&anchor, "anchor", "", "set or move the box's calendar day (YYYY-MM-DD); a move shifts every follower's due by the day delta, previewed until --yes")
	cmd.Flags().BoolVar(&clearAnchor, "clear-anchor", false, "drop the box's day (followers keep their pointer and are disclosed)")
	cmd.Flags().StringVar(&successor, "successor", "", "declare the box to open after this one closes (an epic reference; epic done proposes it, never activates it)")
	cmd.Flags().BoolVar(&clearSuccessor, "clear-successor", false, "drop the declared successor")
	addYesFlag(cmd, &yes)
	cmd.MarkFlagsMutuallyExclusive("anchor", "clear-anchor")
	cmd.MarkFlagsMutuallyExclusive("successor", "clear-successor")
	return cmd
}

func newEpicActivateCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "activate <epic>",
		Short: "Make this the active epic for its repos (at most one each)",
		Long: "Open a box for work. `furrow next` then scopes to it.\n\n" +
			"At most ONE epic may be active per repo, and an epic naming several repos\n" +
			"consumes a slot in every one of them — a cross-repo box really is the\n" +
			"current core on both sides. Activating a second one for a repo is exit 2,\n" +
			"naming the incumbent.\n\n" +
			"An epic with no repos cannot be activated: with no slot to consume it would\n" +
			"slip past the per-repo count entirely.\n\n" +
			"furrow does not police WHO switches — a CLI has no caller identity. It\n" +
			"RECORDS instead: the switch is appended to the epic's body as a\n" +
			"`YYYY-MM-DD HH:MM activated[ — reason]` line, the time in UTC, and\n" +
			"`furrow sync` surfaces it, so a misread instruction shows up in the same\n" +
			"session rather than weeks later. Pass --reason to say who asked and why.\n\n" +
			"An epic still waiting on open deps (`furrow epic dep`) activates with a\n" +
			"WARNING, never a refusal — the ordering is advice, and the stderr note\n" +
			"plus the envelope's open_deps (and lint's epic-dep-open afterwards) are\n" +
			"what keep the crossing visible.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			before, after, openDeps, err := a.EpicActivate(args[0], reason)
			if err != nil {
				return err
			}
			if len(openDeps) > 0 {
				fmt.Fprintf(errOut, "note: %s still waits on open epic(s) %s — activating anyway; `furrow epic dep %s --list` shows the edges\n",
					after.ID, strings.Join(openDeps, ", "), after.ID)
			}
			var extra map[string]any
			if len(openDeps) > 0 {
				extra = map[string]any{"open_deps": openDeps}
			}
			return emitEpicMutationResult(a, before, after, extra)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "who asked for this switch and why (recorded in the epic's body)")
	return cmd
}

func newEpicDeactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deactivate <epic>",
		Short: "Clear the active flag without closing the epic (suggests where to return)",
		Long: "Step away from a box without closing it. The output carries the\n" +
			"previous-active SUGGESTION (see `epic done --help` — same mechanism, same\n" +
			"contract: computed from the activation log, never auto-activated,\n" +
			"`unknown` when no record decides it). A declared successor is not\n" +
			"consulted: the box has not closed, so its \"after\" has not come.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			before, after, err := a.EpicDeactivate(args[0])
			if err != nil {
				return err
			}
			return emitWithPreviousSuggest(a, before, after, nil)
		},
	}
}

func newEpicDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <epic>",
		Short: "Close an epic (clears active; proposes the declared successor, else the previous active — never activates)",
		Long: "Close the box and free its repos' slots. furrow does NOT open the next\n" +
			"box — that act is the human's — so the repo is left with no active epic\n" +
			"and `furrow lint` warns epic-no-active until someone activates one. It\n" +
			"PROPOSES, in this order:\n\n" +
			"The DECLARED successor first (`epic set --successor`, the plan written\n" +
			"ahead): the output names it (`successor: <id> … — furrow epic activate\n" +
			"<id>`; a `successor` key in --json, null when the box declared none, else\n" +
			"{declared, id, title, state, reason, skipped, open_deps, held}). A declared\n" +
			"box that already closed is walked past along its own chain (`skipped`) to\n" +
			"the first open one; `reason` is absent when the box can simply be\n" +
			"activated and otherwise says why not: `active` (no switch to make),\n" +
			"`slot-held` (another box holds a repo it needs, named in `held`), `no-repo`\n" +
			"(attach one first), `closed` (the whole chain is closed) or `missing` (the\n" +
			"id names no box). `open_deps` lists the deps it would still cross,\n" +
			"computed after this close. Branch on the fields, never on the line.\n\n" +
			"The PREVIOUS active box speaks for what the declaration does not cover:\n" +
			"with no declaration at all, exactly as before; with one, only for a freed\n" +
			"repo slot the proposed box does not span, and never when the box being\n" +
			"closed was not active (nothing was freed). The `previous` key in --json is\n" +
			"always computed over every repo, as deactivate's is.\n\n" +
			"Closing a box ahead of its tail is untidy, not broken, so members still\n" +
			"open never REFUSE the close — they are DISCLOSED: a stderr note names them\n" +
			"and --json carries open_members (an array of {id, title, status, repeat};\n" +
			"[] = none, null = the board could not be read). `furrow lint` then warns\n" +
			"epic-closed for each until it closes, moves to a terminal lane, is re-filed\n" +
			"(`furrow set <id> -e <epic>`), or the box is reopened.\n\n" +
			"A REPEATING member is called out separately, because its warn does not\n" +
			"clear itself: closing an occurrence mints the next one, which inherits the\n" +
			"epic, so the same warn comes back under this closed box every cycle. The\n" +
			"fix is one-shot — re-file the live occurrence (`furrow set <id> -e\n" +
			"<open-epic>`) and every later occurrence inherits the new box.\n\n" +
			"The previous suggestion: among the open boxes sharing a repo of\n" +
			"this one that no other active epic holds, the output names the one with\n" +
			"the newest activation record (`previous: <id> … — furrow epic activate\n" +
			"<id>`, its time in local time with the offset; a `previous` key in\n" +
			"--json, {id, title, at} with at the UTC stamp as recorded, null = no\n" +
			"suggestion). Another repo's box is never suggested. A box with no repo\n" +
			"gets unknown, and one whose repos all still have an active epic gets\n" +
			"none: that focus never left.\n" +
			"The answer is COMPUTED fresh from the activation log `epic activate`\n" +
			"already writes into each box's body — furrow stores no \"previous\"\n" +
			"pointer that could go stale, and when no record decides it, it says\n" +
			"`unknown` and leaves the judgement to you. The suggestion is never\n" +
			"executed: activating remains your call.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			before, after, err := a.EpicDone(args[0])
			if err != nil {
				return err
			}
			// Disclosure only, and computed AFTER the write: closing a box ahead of
			// its tail is a warn-level condition (core.EpicProblems' epic-closed),
			// so nothing here may refuse or alter the close.
			members := a.EpicOpenMembers(after.ID)
			for _, line := range openMembersNotes(after.ID, members) {
				stderrNote(line)
			}
			return emitEpicDoneResult(a, before, after, members)
		},
	}
}

// emitEpicDoneResult is `epic done`'s tail: the mutation envelope with the
// declared-successor proposal, the previous-active suggestion and the open
// members, then the human lines — the successor's, and the previous line only
// for the freed slots the proposal leaves uncovered (every slot when nothing
// was declared; none when the closed box was not active, since nothing was
// freed). Both suggestions are display data computed AFTER the close.
func emitEpicDoneResult(a *app.App, before, after *core.Epic, members []app.EpicOpenMember) error {
	succ := a.SuccessorSuggest(after.ID)
	prev, held := a.PreviousActiveSuggest(after.ID)
	if err := emitEpicMutationResult(a, before, after, map[string]any{
		"previous": prev, "successor": succ, "open_members": members,
	}); err != nil {
		return err
	}
	if jsonMode() {
		return nil
	}
	if succ == nil {
		printPreviousLine(prev, held, after.Repos)
		return nil
	}
	printSuccessorLines(succ)
	if !before.Active {
		return nil
	}
	// A spent or unusable plan leaves every freed slot to the previous line —
	// printed even for a repo-less box, whose answer is the "unknown" reason.
	if uncovered := a.UncoveredRepos(succ, before.Repos); len(uncovered) > 0 || !succ.Proposed() {
		prev, held := a.PreviousActiveAmong(after.ID, uncovered)
		printPreviousLine(prev, held, uncovered)
	}
	return nil
}

// printSuccessorLines renders the proposal, one line per fact a human acts on:
// the box and the command, or the reason it cannot simply be activated.
func printSuccessorLines(s *app.SuccessorSuggest) {
	switch s.Reason {
	case "":
		fmt.Fprintf(out, "successor: %s %q — open with `furrow epic activate %s`\n", s.ID, s.Title, s.ID)
	case app.SuccessorActive:
		fmt.Fprintf(out, "successor: %s %q is already active — no switch to make\n", s.ID, s.Title)
	case app.SuccessorSlotHeld:
		parts := make([]string, 0, len(s.Held))
		for repo, id := range s.Held {
			parts = append(parts, repo+" by "+id)
		}
		sort.Strings(parts)
		fmt.Fprintf(out, "successor: %s %q cannot open yet — its slot is held (%s); the declaration stays\n", s.ID, s.Title, strings.Join(parts, ", "))
	case app.SuccessorNoRepo:
		fmt.Fprintf(out, "successor: %s %q names no repo — attach one (`furrow epic set %s --add-repo <owner/repo>`), then activate it\n", s.ID, s.Title, s.ID)
	case app.SuccessorClosed:
		fmt.Fprintf(out, "successor: %s %q is closed, and its own chain reaches no open box — the plan is spent\n", s.ID, s.Title)
	case app.SuccessorMissing:
		fmt.Fprintf(out, "successor: %s does not exist — the declaration was dangling (lint: epic-successor-missing)\n", s.ID)
	}
	if len(s.Skipped) > 0 && s.Reason != app.SuccessorClosed {
		fmt.Fprintf(out, "           reached by walking past closed %s\n", strings.Join(s.Skipped, ", "))
	}
	if len(s.OpenDeps) > 0 && s.Reason == "" {
		fmt.Fprintf(out, "           it still waits on open epic(s) %s — activate warns and proceeds\n", strings.Join(s.OpenDeps, ", "))
	}
}

// openMembersNotes renders `epic done`'s stderr disclosure: one line naming the
// members the close left open, and — only when some of them recur — a second
// line calling those out, since a repeating member's epic-closed comes back
// every cycle instead of clearing when the task closes. Nothing open, nothing
// printed; nil members (the board could not be read) says so rather than
// printing a false all-clear.
func openMembersNotes(epicID string, members []app.EpicOpenMember) []string {
	if members == nil {
		return []string{"note: " + epicID + " closed, but the board could not be read to report the members it leaves open — check with `furrow epic show " + epicID + "`"}
	}
	if len(members) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf(
		"note: %s closed with %d open member(s): %s — `furrow lint` warns epic-closed for each until it closes, moves to a terminal lane, is re-filed (`furrow set <id> -e <epic>`), or the box is reopened",
		epicID, len(members), namedMemberIDs(members))}
	if rep := app.RepeatingMembers(members); len(rep) > 0 {
		verb := "repeats"
		if len(rep) > 1 {
			verb = "repeat"
		}
		lines = append(lines, fmt.Sprintf(
			"note: %d of them %s: %s — closing an occurrence mints the next one under this CLOSED box, so the warn returns every cycle; re-file the live occurrence once (`furrow set %s -e <open-epic>`) and the whole series follows",
			len(rep), verb, namedMemberIDs(rep), rep[0].ID))
	}
	return lines
}

// namedMemberIDs names at most three ids, "+N more" for the rest — incomingLine's
// cap, for its reason: the count is exact, the line stays legible, and the full
// list is the JSON open_members key.
func namedMemberIDs(members []app.EpicOpenMember) string {
	n, more := capNames(len(members))
	ids := make([]string, n)
	for i, m := range members[:n] {
		ids[i] = m.ID
	}
	return strings.Join(ids, ", ") + more
}

// emitWithPreviousSuggest is deactivate's tail (done's is emitEpicDoneResult,
// which adds the declared successor): the usual epic mutation envelope plus
// the previous-active suggestion — a `previous` key in JSON (always present
// on both verbs; null = no suggestion, so a consumer can tell "computed, no
// answer" apart from an older binary that lacks the key) and one human line
// after the mutation line. The suggestion is display data: it is computed
// AFTER the mutation succeeded and can never affect it.
func emitWithPreviousSuggest(a *app.App, before, after *core.Epic, extra map[string]any) error {
	prev, held := a.PreviousActiveSuggest(after.ID)
	keys := map[string]any{"previous": prev}
	for k, v := range extra {
		keys[k] = v
	}
	if err := emitEpicMutationResult(a, before, after, keys); err != nil {
		return err
	}
	if jsonMode() {
		return nil
	}
	printPreviousLine(prev, held, after.Repos)
	return nil
}

// printPreviousLine is the one human line of the previous-active suggestion,
// which alone says WHY there is none (no repo, the repos' focus never left,
// or no record). repos are the ones the answer was computed over.
func printPreviousLine(prev *app.PreviousActive, held map[string]string, repos []string) {
	switch {
	case prev != nil:
		fmt.Fprintf(out, "previous: %s %q (activated %s) — return with `furrow epic activate %s`\n", prev.ID, prev.Title, switchStampHuman(prev.At), prev.ID)
	case len(held) > 0:
		parts := make([]string, 0, len(held))
		for repo, id := range held {
			parts = append(parts, repo+" by "+id)
		}
		sort.Strings(parts)
		fmt.Fprintf(out, "previous: none (still held: %s — nothing to return to)\n", strings.Join(parts, ", "))
	case len(repos) == 0:
		fmt.Fprintln(out, "previous: unknown (this box has no repo, so no box shares one — pick one with `furrow epic ls`)")
	default:
		fmt.Fprintln(out, "previous: unknown (no open box in its repos has an activation record — pick one with `furrow epic ls`)")
	}
}

func newEpicReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <epic>",
		Short: "Reopen a closed epic (clears closed; never re-activates)",
		Long: "Clear the box's closed stamp — the CLI way back for a box closed by\n" +
			"mistake (before this verb, recovery meant hand-editing the shard, which\n" +
			"the store contract forbids, or a git revert on the board repo). The box\n" +
			"comes back OPEN and INACTIVE: reopening is one deliberate act, choosing\n" +
			"it again (`furrow epic activate`) is another, and furrow never chains\n" +
			"them. Reopening an already-open box is exit 2 (mirroring `epic done`'s\n" +
			"already-closed refusal).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			before, after, err := a.EpicReopen(args[0])
			if err != nil {
				return err
			}
			return emitEpicMutationResult(a, before, after, nil)
		},
	}
}

// newEpicDepCmd is `furrow dep`'s epic twin (v7): the same variadic add/rm and
// the same read-only --list, over the epic dep graph.
func newEpicDepCmd() *cobra.Command {
	var rm, list bool
	cmd := &cobra.Command{
		Use:   "dep <epic> [<dep-epic>...]",
		Short: "Add/remove an epic's deps (open after those close), or list them both ways with --list",
		Long: "Make <epic> wait on each <dep-epic>: \"open this box after those close\".\n" +
			"Several deps in one call apply in a single write; --rm removes them instead\n" +
			"(`epic dep add` / `epic dep rm` are the same two writes, verb first).\n" +
			"Adding is acyclic and idempotent, the batch is all-or-nothing, and every\n" +
			"argument resolves like any epic reference (id, unique prefix, or unique\n" +
			"title substring — a dangling edge is still removable by its literal id).\n\n" +
			"The edge is INFORMATION, not enforcement — deliberately weaker than a task\n" +
			"dep: `epic activate` warns about a still-open dep and proceeds, a dep on a\n" +
			"closed epic is simply satisfied, and `furrow brief`/`sync` raise epic_dep_done\n" +
			"when every box this one waited on is closed — its turn to open. Parallel\n" +
			"branches need no feature: two epics sharing a dep are two branches. Task\n" +
			"deps are never checked against it: a task edge may run either way across\n" +
			"boxes (a box's dep says when the box opens, not what its members may wait\n" +
			"on), and `furrow lint` has no finding for the crossing.\n\n" +
			"With --list, don't mutate — read the neighborhood in BOTH directions: what\n" +
			"it waits on (depends_on) and what waits on it (blocks), each resolved to\n" +
			"id+title+state.\n\n" +
			"`furrow lint` backstops the graph: epic-dep-cycle and epic-dep-missing\n" +
			"(errors), epic-dep-open (warn: an active box still waiting on an open dep).",
		Example: "  furrow epic dep e-k3m9p e-a1b2c\n" +
			"  furrow epic dep e-k3m9p e-a1b2c e-d4e5f   # wait on both in one write\n" +
			"  furrow epic dep e-k3m9p e-a1b2c --rm\n" +
			"  furrow epic dep add e-k3m9p e-a1b2c      # the same two writes, verb first\n" +
			"  furrow epic dep rm e-k3m9p e-a1b2c\n" +
			"  furrow epic dep e-k3m9p --list --json   # what it waits on and what it blocks",
		Args: depVerbArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if list {
				res, err := a.EpicDepList(args[0])
				if err != nil {
					return err
				}
				return emitEpicDepList(res)
			}
			spelled, args := depVerb(args)
			ref, deps := args[0], args[1:]
			if rm || spelled == "rm" {
				return emitEpicMutation(a, func() (*core.Epic, *core.Epic, error) { return a.EpicRemoveDeps(ref, deps) })
			}
			return emitEpicMutation(a, func() (*core.Epic, *core.Epic, error) { return a.EpicAddDeps(ref, deps) })
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the deps instead of adding them")
	cmd.Flags().BoolVar(&list, "list", false, "read-only: list what <epic> waits on and what waits on it (both directions)")
	cmd.MarkFlagsMutuallyExclusive("list", "rm")
	return cmd
}

// parseMetaPairs turns repeated --meta k=v flags into a map. A pair with no "="
// is bad usage, never a key with an empty value: silently storing `--meta place`
// as place="" would look like it worked.
func parseMetaPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, core.Validationf("", "--meta %q is not key=value", p)
		}
		if strings.TrimSpace(k) == "" {
			return nil, core.Validationf("", "--meta %q has an empty key", p)
		}
		m[k] = v
	}
	return m, nil
}
