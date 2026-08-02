package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newEpicCmd is the box command tree. Epics are a separate entity from tasks —
// their own shard, their own id prefix — so they get their own subtree rather
// than more flags on `add`/`set`: `furrow epic <verb>` reads as "operate on a
// box", and the two entities' verbs never collide.
func newEpicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "epic",
		Short: "Manage epics (boxes of work): add, list, show, set, activate, done",
		Long: "An epic is a box of work: a first-class entity with a goal, free-form\n" +
			"meta, and member tasks. At most ONE epic may be active per repo, and\n" +
			"`furrow next` scopes to it — that is what keeps a session from picking the\n" +
			"cheapest task on the whole board every time.\n\n" +
			"furrow never chooses which box to open: `epic done` clears the active flag\n" +
			"but picks no successor, and `furrow lint` warns (epic-no-active) until a\n" +
			"human does.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newEpicAddCmd(), newEpicLsCmd(), newEpicShowCmd(), newEpicSetCmd(),
		newEpicActivateCmd(), newEpicDeactivateCmd(), newEpicDoneCmd(), newEpicDepCmd())
	return cmd
}

func newEpicAddCmd() *cobra.Command {
	var (
		goal   string
		meta   []string
		labels []string
		repos  []string
		body   string
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
			"interprets or indexes it. Read it back with `epic show --json | jq`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			m, err := parseMetaPairs(meta)
			if err != nil {
				return err
			}
			e, err := a.EpicAdd(args[0], app.EpicAddOpts{
				Goal: goal, Meta: m, Labels: labels, Repos: repos, Body: body,
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
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringArrayVarP(&repos, "repo", "r", nil, "owner/repo this box spans (repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "initial body markdown (default: a heading from the title)")
	return cmd
}

func newEpicLsCmd() *cobra.Command {
	var (
		all   bool
		label string
		repo  string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List epics (open only by default), active first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			resolved := ""
			if repo != "" {
				if resolved, err = a.ResolveRepo(repo); err != nil {
					return err
				}
			}
			items, err := a.EpicList(app.EpicQueryOpts{All: all, Label: label, Repo: resolved, Limit: limit})
			if err != nil {
				return err
			}
			return emitEpicList(items)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include closed epics")
	cmd.Flags().StringVarP(&label, "label", "l", "", "filter by label")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "filter by owner/repo")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "max rows (0 = all)")
	return cmd
}

func newEpicShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <epic>",
		Short: "Show one epic: goal, meta, progress, member tasks, and its body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			d, err := a.EpicShow(args[0])
			if err != nil {
				return err
			}
			return emitEpicDetail(a, d)
		},
	}
	return cmd
}

func newEpicSetCmd() *cobra.Command {
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
		Short: "Edit an epic's title, goal, meta, labels, repos, or its standing/pinned declarations",
		Long: "Edit a box's metadata. --standing and --pinned (v7) are the two PERMANENT-\n" +
			"channel declarations: a standing box is exempt from the finish-shaped nags\n" +
			"(revisit's epic_all_done / epic_dep_done; stuck still fires), and a pinned\n" +
			"box's actionable tasks lead `next`/`brief` regardless of the active scope.\n" +
			"They are orthogonal to each other and to activate (a mandate box is\n" +
			"standing + pinned; a parking lot is standing only). Clear one explicitly\n" +
			"with --standing=false / --pinned=false — an omitted flag never touches the\n" +
			"stored value. Which boxes carry them is an operating convention, not\n" +
			"furrow's: furrow stays name-independent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			m, err := parseMetaPairs(meta)
			if err != nil {
				return err
			}
			o := app.EpicSetOpts{
				SetMeta: m, RmMeta: rmMeta,
				AddLabels: addLabels, RmLabels: rmLabels,
				AddRepos: addRepos, RmRepos: rmRepos,
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
			return emitEpicMutation(func() (*core.Epic, *core.Epic, error) { return a.EpicSet(args[0], o) })
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "rename the epic")
	cmd.Flags().StringVar(&goal, "goal", "", "set the closing condition (\"\" clears it)")
	cmd.Flags().StringArrayVar(&meta, "meta", nil, "set a free-form key=value (repeatable)")
	cmd.Flags().StringArrayVar(&rmMeta, "rm-meta", nil, "remove a meta key (repeatable)")
	cmd.Flags().StringArrayVar(&addLabels, "add-label", nil, "add a label (repeatable)")
	cmd.Flags().StringArrayVar(&rmLabels, "rm-label", nil, "remove a label (repeatable)")
	cmd.Flags().StringArrayVar(&addRepos, "add-repo", nil, "attach an owner/repo (repeatable)")
	cmd.Flags().StringArrayVar(&rmRepos, "rm-repo", nil, "detach an owner/repo (repeatable)")
	cmd.Flags().BoolVar(&standing, "standing", false, "declare a permanent box (exempt from epic_all_done/epic_dep_done; --standing=false clears)")
	cmd.Flags().BoolVar(&pinned, "pinned", false, "surface this box's actionable tasks in next/brief regardless of the active scope (--pinned=false clears)")
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
			"RECORDS instead: the switch is appended to the epic's body and `furrow\n" +
			"sync` surfaces it, so a misread instruction shows up in the same session\n" +
			"rather than weeks later. Pass --reason to say who asked and why.\n\n" +
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
			return emitEpicMutationResult(before, after, extra)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "who asked for this switch and why (recorded in the epic's body)")
	return cmd
}

func newEpicDeactivateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deactivate <epic>",
		Short: "Clear the active flag without closing the epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitEpicMutation(func() (*core.Epic, *core.Epic, error) { return a.EpicDeactivate(args[0]) })
		},
	}
}

func newEpicDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <epic>",
		Short: "Close an epic (clears active; never picks the next one)",
		Long: "Close the box and free its repos' slots. furrow does NOT choose a\n" +
			"successor — that judgement is the human's — so the repo is left with no\n" +
			"active epic and `furrow lint` warns epic-no-active until someone picks one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitEpicMutation(func() (*core.Epic, *core.Epic, error) { return a.EpicDone(args[0]) })
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
			"Several deps in one call apply in a single write; --rm removes them instead.\n" +
			"Adding is acyclic and idempotent, the batch is all-or-nothing, and every\n" +
			"argument resolves like any epic reference (id, unique prefix, or unique\n" +
			"title substring — a dangling edge is still removable by its literal id).\n\n" +
			"The edge is INFORMATION, not enforcement — deliberately weaker than a task\n" +
			"dep: `epic activate` warns about a still-open dep and proceeds, a dep on a\n" +
			"closed epic is simply satisfied, and `furrow revisit` raises epic_dep_done\n" +
			"when every box this one waited on is closed — its turn to open. Parallel\n" +
			"branches need no feature: two epics sharing a dep are two branches.\n\n" +
			"With --list, don't mutate — read the neighborhood in BOTH directions: what\n" +
			"it waits on (depends_on) and what waits on it (blocks), each resolved to\n" +
			"id+title+state.",
		Example: "  furrow epic dep e-k3m9 e-a1b2\n" +
			"  furrow epic dep e-k3m9 e-a1b2 e-d4e5   # wait on both in one write\n" +
			"  furrow epic dep e-k3m9 e-a1b2 --rm\n" +
			"  furrow epic dep e-k3m9 --list --json   # what it waits on and what it blocks",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("list") {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.MinimumNArgs(2)(cmd, args)
		},
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
			ref, deps := args[0], args[1:]
			if rm {
				return emitEpicMutation(func() (*core.Epic, *core.Epic, error) { return a.EpicRemoveDeps(ref, deps) })
			}
			return emitEpicMutation(func() (*core.Epic, *core.Epic, error) { return a.EpicAddDeps(ref, deps) })
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
