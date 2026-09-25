package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		status     string
		priority   int
		value      int
		effort     int
		labels     []string
		repos      []string
		draft      bool
		deps       []string
		refs       []string
		body       string
		checks     []string
		stdin      bool
		batch      string
		epicRef    string
		due        string
		repeatSpec string
	)
	cmd := &cobra.Command{
		Use:   "add <title>...",
		Short: "Add a task (or many with --stdin / --batch)",
		Long: "Add a task. The id is assigned automatically (frozen, never reused) and a\n" +
			"bodies/<id>.md file is created, seeded with the title as a heading.\n\n" +
			"With --stdin, read one title per line from stdin and create them all in a\n" +
			"single write (blank lines skipped); the shared flags apply to every task.\n\n" +
			"With --batch <file|->, read NDJSON — one JSON object per line, one task each\n" +
			"— and create them all in a single write with per-task fields: title (required),\n" +
			"status, priority, value, effort, labels, repos, draft, epic (\"\" = unfiled on\n" +
			"purpose), deps, refs, body, checklist, due, repeat, and key. An unknown field\n" +
			"is exit 2 with the vocabulary in candidates. The shared flags are the\n" +
			"defaults: a scalar field on the line replaces the flag's value, a list field\n" +
			"(labels, repos, deps, refs, checklist) unions with it. A checklist entry is\n" +
			"a string (an unticked item) or the shard's own item object\n" +
			"{\"text\": \"...\", \"done\": true}, so a board exported with its ticks reads\n" +
			"back with them; a key the object does not have is exit 2, like an unknown\n" +
			"field. `key` names the line INSIDE the batch only: a dep may cite another\n" +
			"line's key instead of an id, and a [[key]] in a title, body, or checklist\n" +
			"item becomes [[id]] once ids are minted — so an epic with a dependency\n" +
			"graph is written in one file, in any order, with no id known in advance.\n" +
			"Keys never reach the board; --json echoes each task's key beside it, which\n" +
			"is how a caller learns the ids. A duplicate key, a key that is an existing\n" +
			"id, a dep naming neither an id nor a key, or a dep cycle inside the batch\n" +
			"is exit 2 and writes nothing.\n\n" +
			"--due promises the task for an instant: `2026-08-04` (that WHOLE day — it\n" +
			"binds 23:59:59 in the board's calendar, so the day never starts out\n" +
			"overdue), `2026-08-04T10:30`, an RFC3339 instant, or a signed offset such\n" +
			"as `+1d`. --repeat makes the task RECUR: a short spelling (daily, every <n>\n" +
			"days, weekly, weekly on <days>, every <n> weeks on <days>, monthly, monthly\n" +
			"on <day-of-month>, monthly on last, monthly on <nth> <weekday>, monthly on\n" +
			"last <weekday>, every <n> months on <day-of-month>, yearly, every <n>\n" +
			"years), optionally ending in `until <date>` or `for <n> times` (never\n" +
			"both), or a raw RFC 5545 RRULE line — minus a DTSTART, which is exit 2 in\n" +
			"either spelling: the series starts at --due, which --repeat therefore\n" +
			"requires, and that first date becomes the immovable series anchor. The\n" +
			"anchor need not land on the rule: an off-lattice one is a live occurrence\n" +
			"OUTSIDE the series (`--due <a Friday> --repeat 'weekly on mon for 3 times'`\n" +
			"is four tasks), and a stderr note names the rule's own first date. A day\n" +
			"past 28 SKIPS the months that lack it (`monthly on last` always lands) and\n" +
			"February 29 skips the years that lack one; both say so at bind time. Only\n" +
			"a close advances a series — see `furrow done --help`.",
		Example: "  furrow add \"Wire up the config loader\"\n" +
			"  furrow add \"Fix flaky sync test\" -s ready -l bug --value 4 --effort 2\n" +
			"  furrow add \"Cross-repo epic\" -r akira-toriyama/furrow -r akira-toriyama/cifail\n" +
			"  furrow add \"Check the nightly run landed\" -s waiting --due 2026-08-04T10:30\n" +
			"  git grep -l TODO | furrow add --stdin -l chore   # one task per line\n" +
			"  furrow add --batch plan.ndjson -e travel --json   # per-task fields, keys for deps and [[links]]",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// `--body -` reads the initial body from stdin (the shared `-`=stdin
			// convention; `note`/`done --note` honor it too). `--stdin` (one title
			// per line) also consumes stdin, so the two cannot both read it.
			if body == "-" && stdin {
				return core.Validationf("", "cannot combine --stdin with --body - (stdin has a single stream)")
			}
			if batch != "" && stdin {
				return core.Validationf("", "cannot combine --batch with --stdin (one bulk input per call)")
			}
			if batch == "-" && body == "-" {
				return core.Validationf("", "cannot combine --batch - with --body - (stdin has a single stream)")
			}
			// An empty --due is exit 2 here for the same reason it is on `set`: a
			// caller interpolating an unset variable (`--due "$WHEN"`) means a bug,
			// and silently creating a DATELESS task would drop the promise where
			// nothing — not brief, not lint — could ever report it again.
			if cmd.Flags().Changed("due") && strings.TrimSpace(due) == "" {
				return core.Validationf("", "--due was given an empty value; pass a date, or drop the flag to create the task without one")
			}
			if cmd.Flags().Changed("repeat") && strings.TrimSpace(repeatSpec) == "" {
				return core.Validationf("", "--repeat was given an empty value; pass a rule, or drop the flag to create the task without one")
			}
			opts := app.AddOpts{
				Status: status, Labels: labels, Repos: repos, Draft: draft,
				Deps: deps, Refs: refs, Body: body, Checklist: app.UncheckedItems(checks),
				Epic: epicRef, Due: due, Repeat: repeatSpec,
				// An explicit `-e ''` means "unfiled, on purpose" — suppress the
				// active-epic inheritance a bare add gets.
				NoEpic: cmd.Flags().Changed("epic") && epicRef == "",
			}
			if cmd.Flags().Changed("priority") {
				p := priority
				opts.Priority = &p
			}
			if cmd.Flags().Changed("value") {
				v := value
				opts.Value = &v
			}
			if cmd.Flags().Changed("effort") {
				e := effort
				opts.Effort = &e
			}

			if stdin {
				if len(args) > 0 {
					return core.Validationf("", "cannot combine --stdin with title arguments")
				}
				return addFromStdin(cmd, a, opts)
			}
			if batch != "" {
				if len(args) > 0 {
					return core.Validationf("", "cannot combine --batch with title arguments")
				}
				return addFromBatch(cmd, a, batch, opts)
			}
			if len(args) == 0 {
				return core.Validationf("", "provide a title, --stdin to read titles from stdin, or --batch <file|-> for NDJSON")
			}
			// Resolve `--body -` (read stdin) for the single-task path; the --stdin
			// path was excluded above, so body is otherwise a literal here.
			if opts.Body, err = readTextArg(cmd, body); err != nil {
				return err
			}
			t, err := a.Add(strings.Join(args, " "), opts)
			if err != nil {
				return err
			}
			// Signal a clamped estimate on stderr (an explicit --value/--effort
			// silently rounded to 1..5), matching value/effort/set. stdout stays
			// the created task.
			warnClamp("value", opts.Value, t.Value)
			warnClamp("effort", opts.Effort, t.Effort)
			warnShadowedDraft(a, opts.Draft, len(t.Repos) == 0)
			noteInheritedEpic(cmd, []core.Task{*t})
			noteRepeatBinds(a, []core.Task{*t})
			printOK("added", t)
			return nil
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "lane (default: config lanes.default)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "explicit priority (default: append in lane)")
	cmd.Flags().IntVar(&value, "value", 0, "coarse 1..5 value estimate (clamped; omit to leave unset)")
	cmd.Flags().IntVar(&effort, "effort", 0, "coarse 1..5 effort estimate (clamped; omit to leave unset)")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringSliceVarP(&repos, "repo", "r", nil, "repo to attach (owner/repo, or a unique short name; repeatable)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as a draft (no repo attached; suppresses the board repo); conflicts with -r")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "epic to file this task under (id, unique id prefix, or unique title substring; default: the scope's single active epic, '' stays unfiled)")
	cmd.Flags().StringVar(&due, "due", "", "promise this for a date: 2026-08-04 (that whole day), 2026-08-04T10:30, an RFC3339 instant, or an offset like +1d")
	cmd.Flags().StringVar(&repeatSpec, "repeat", "", "recur when closed: daily | every 2 weeks on mon,thu | monthly on last fri | ... (needs --due; a raw RRULE line also works, minus a DTSTART — --due is the series start)")
	cmd.Flags().StringSliceVar(&deps, "dep", nil, "dependency task id (repeatable)")
	cmd.Flags().StringArrayVar(&refs, "ref", nil, "reference (file:line or URL; verbatim; repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "initial body markdown ('-' reads stdin; default: a heading from the title)")
	cmd.Flags().StringArrayVar(&checks, "check", nil, "seed an unchecked checklist item (repeatable; text verbatim)")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read one task title per line from stdin; create all in one write")
	cmd.Flags().StringVar(&batch, "batch", "", "read NDJSON (one task object per line; '-' = stdin) with per-task fields and batch-local keys for deps and [[links]]; create all in one write")
	// A title that begins with '-' (e.g. `add --ndjson-ish title`) is parsed as a
	// flag → "unknown flag". Steer the caller to the `--` separator instead of a
	// bare cobra usage error, so an agent recovers without guessing.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if msg := err.Error(); strings.HasPrefix(msg, "unknown flag") || strings.HasPrefix(msg, "unknown shorthand flag") {
			return core.Validationf("", "%s — a title starting with '-' needs a `--` separator: furrow add -- \"<title>\"", msg)
		}
		return err
	})
	return cmd
}

// addFromStdin bulk-creates one task per non-blank stdin line via a single
// atomic write (app.AddMany). The command's shared flags apply to every task.
func addFromStdin(cmd *cobra.Command, a *app.App, opts app.AddOpts) error {
	var specs []app.AddSpec
	sc := bufio.NewScanner(cmd.InOrStdin())
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long title lines
	for sc.Scan() {
		title := strings.TrimSpace(sc.Text())
		if title == "" {
			continue
		}
		specs = append(specs, app.AddSpec{Title: title, AddOpts: opts})
	}
	if err := sc.Err(); err != nil {
		return core.Internalf("", "reading stdin: %v", err)
	}
	if len(specs) == 0 {
		return core.Validationf("", "no task titles on stdin")
	}
	created, err := a.AddMany(specs)
	if err != nil {
		return err
	}
	drafted := len(created) > 0 && len(created[0].Repos) == 0
	warnShadowedDraft(a, opts.Draft, drafted)
	noteInheritedEpic(cmd, created)
	noteRepeatBinds(a, created)
	return emitTasks(a, created)
}

// noteRepeatBinds says, once per distinct note, what a rule this add bound will
// do that the operator almost certainly did not ask for — a day some months
// lack, an anchor the rule does not land on. Said at BIND time because the
// alternative is finding out in March, or at the close that hands out one
// occurrence more than the count.
func noteRepeatBinds(a *app.App, created []core.Task) {
	said := map[string]bool{}
	for i := range created {
		for _, w := range a.RepeatWarnings(&created[i]) {
			if said[w] {
				continue
			}
			said[w] = true
			fmt.Fprintln(errOut, w)
		}
	}
}

// noteInheritedEpic discloses on stderr that a bare add (no -e) filed the
// created task(s) under the scope's single active epic — the inheritance must
// never be silent, and the escape (`-e ”` now, `set <id> -e ”` after) rides
// in the same line. One note per command, whatever the arity.
func noteInheritedEpic(cmd *cobra.Command, created []core.Task) {
	if cmd.Flags().Changed("epic") {
		return // explicit -e: nothing was inherited
	}
	n, epic := 0, ""
	for _, t := range created {
		if t.Epic != "" {
			n++
			epic = t.Epic
		}
	}
	if n == 0 {
		return
	}
	fmt.Fprintf(errOut, "note: filed under active epic %s (inherited; -e '' creates unfiled, `furrow set <id> -e ''` unfiles)\n", epic)
}

// warnShadowedDraft raises doctor's scope-shadowed finding at the moment it
// bites: a task (or batch — the shared flags make them uniform) that landed
// repo-less, NOT because --draft asked for it, from inside a configured
// board's own tree. One stderr line, exit unchanged — local work is never
// blocked, it just stops being silent.
func warnShadowedDraft(a *app.App, draftFlag, drafted bool) {
	if draftFlag || !drafted {
		return
	}
	if w := a.ShadowedDraftWarning("."); w != "" {
		fmt.Fprintln(errOut, w)
	}
}

func newEditCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's or epic's markdown body in $EDITOR, or replace it with --body",
		Long: "Open bodies/<id>.md in $EDITOR. In a non-interactive context (no TTY) it\n" +
			"prints the absolute body path instead of launching an editor, so an agent\n" +
			"can edit the file directly.\n\n" +
			"--body \"<markdown>\" skips the editor entirely: it REPLACES the whole body\n" +
			"AND stamps the entity's `updated`, in one command — the non-interactive\n" +
			"edit. Pass `-` to read the new body from stdin (the shared `-`=stdin\n" +
			"convention; `add --body`, `note`, and `done --note` honor it too). An\n" +
			"empty replacement is exit 2, never a silent clear. Unlike a direct file\n" +
			"edit, which leaves `updated` stale, --body keeps the staleness signals\n" +
			"(revisit, lint's reconcile-gap) honest — prefer `furrow note <id>` when\n" +
			"you mean to APPEND a progress paragraph rather than rewrite.\n\n" +
			"<id> may name a TASK or an EPIC — both entities' prose lives in the one\n" +
			"bodies/ directory, so this is the same file either way; store membership\n" +
			"routes it, never the id's prefix.",
		Example: "  furrow edit t-k3m9p\n" +
			"  furrow edit t-k3m9p --body \"# rewritten\\n\\nnew plan\"\n" +
			"  ridge-editor-save | furrow edit t-k3m9p --body -   # replace from stdin",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("body") {
				return editSetBody(cmd, a, args[0], body)
			}
			// The guard describes a write; the editor path never writes the
			// shard, so a set flag there would be silently meaningless.
			if f := cmd.Flags().Lookup(expectUpdatedFlag); f != nil && f.Value.String() != "" {
				return core.Validationf(args[0], "--expect-updated only applies to the --body replacement write")
			}
			path, err := a.EditPath(args[0])
			if err != nil {
				return err
			}
			// EditPath is guarded like every write, so an idle occupant's
			// warning has to be drained here as the mutators drain it.
			warn := sessionGuardExtra(a)
			if jsonMode() {
				emitObject(mergeExtra(map[string]any{"path": path}, warn))
				return nil
			}
			// Non-interactive: emit the path; the caller (or Claude) edits it.
			if !isTTY() {
				fmt.Fprintln(out, path)
				return nil
			}
			editor := firstNonEmpty(os.Getenv("FURROW_EDITOR"), os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
			parts := strings.Fields(editor)
			parts = append(parts, path)
			// #nosec G204 -- the command is the operator's own $EDITOR
			// (FURROW_EDITOR/VISUAL/EDITOR), same trust model as git commit.
			ed := exec.Command(parts[0], parts[1:]...)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return core.Internalf(args[0], "editor %q failed: %v", editor, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "replace the WHOLE body with this markdown and advance updated ('-' reads stdin); empty is exit 2")
	addExpectUpdatedFlag(cmd)
	return cmd
}

// editSetBody is the `edit --body` arm: resolve `-`=stdin, route task/epic by
// store membership (note's contract), replace, and report. `changed` tracks
// metadata only, so the envelope surfaces the effect as `replaced_bytes` — the
// byte count written, not the text echoed back: unlike a note, a body is
// unbounded and the caller just supplied it.
func editSetBody(cmd *cobra.Command, a *app.App, ref, body string) error {
	text, err := readTextArg(cmd, body)
	if err != nil {
		return err
	}
	// The count must match the file: the write appends the trailing newline
	// the normalizer guarantees (a test pins this against the disk).
	extra := map[string]any{"replaced_bytes": len(strings.TrimRight(text, "\n")) + 1}
	epic, err := a.RefTargetsEpic(ref)
	if err != nil {
		return err
	}
	if epic {
		expected, guard, gerr := expectUpdatedArg(cmd, ref)
		if gerr != nil {
			return gerr
		}
		before, after, serr := a.EpicSetBody(ref, text)
		if serr != nil {
			return serr
		}
		if guard && before != nil {
			extra = mergeExtra(extra, staleReadExtra(before.ID, &before.Updated, expected))
		}
		printEpicMutation("edited", before, after, extra)
		return nil
	}
	return emitMutationWith(cmd, a, "edited", ref,
		func() (*core.Task, error) { return a.SetBody(ref, text) },
		func(after *core.Task) map[string]any { return extra })
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
