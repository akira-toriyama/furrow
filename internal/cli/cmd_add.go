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
		status   string
		priority int
		value    int
		effort   int
		labels   []string
		repos    []string
		draft    bool
		parent   string
		deps     []string
		refs     []string
		body     string
		checks   []string
		stdin    bool
		typ      string
	)
	cmd := &cobra.Command{
		Use:   "add <title>...",
		Short: "Add a task (or many with --stdin)",
		Long: "Add a task. The id is assigned automatically (frozen, never reused) and a\n" +
			"bodies/<id>.md file is created, seeded with the title as a heading.\n\n" +
			"With --stdin, read one title per line from stdin and create them all in a\n" +
			"single write (blank lines skipped); the shared flags apply to every task.",
		Example: "  furrow add \"Wire up the config loader\"\n" +
			"  furrow add \"Fix flaky sync test\" -s ready -l bug --value 4 --effort 2\n" +
			"  furrow add \"Cross-repo epic\" -r akira-toriyama/furrow -r akira-toriyama/cifail\n" +
			"  git grep -l TODO | furrow add --stdin -l chore   # one task per line",
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
			opts := app.AddOpts{
				Status: status, Labels: labels, Repos: repos, Draft: draft,
				Parent: parent, Deps: deps, Refs: refs, Body: body, Checklist: checks,
				Type: typ,
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
			if len(args) == 0 {
				return core.Validationf("", "provide a title, or --stdin to read titles from stdin")
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
			// printOK renders JSON (--json / --ndjson) or the human line.
			printOK("added", t)
			return nil
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "lane (default: config lanes.default)")
	cmd.Flags().StringVar(&typ, "type", "", "work-item type (a value from [types].order, e.g. epic; unknown = exit 2 + candidates)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "explicit priority (default: append in lane)")
	cmd.Flags().IntVar(&value, "value", 0, "coarse 1..5 value estimate (clamped; omit to leave unset)")
	cmd.Flags().IntVar(&effort, "effort", 0, "coarse 1..5 effort estimate (clamped; omit to leave unset)")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringSliceVarP(&repos, "repo", "r", nil, "repo to attach (owner/repo, or a unique short name; repeatable)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as a draft (no repo attached; suppresses the board repo); conflicts with -r")
	cmd.Flags().StringVar(&parent, "parent", "", "parent task id")
	cmd.Flags().StringSliceVar(&deps, "dep", nil, "dependency task id (repeatable)")
	cmd.Flags().StringSliceVar(&refs, "ref", nil, "reference (file:line or URL, repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "initial body markdown (`-` reads stdin; default: a heading from the title)")
	cmd.Flags().StringArrayVar(&checks, "check", nil, "seed an unchecked checklist item (repeatable; text verbatim)")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read one task title per line from stdin; create all in one write")
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
	return emitTasks(created)
}

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's markdown body in $EDITOR",
		Long: "Open bodies/<id>.md in $EDITOR. In a non-interactive context (no TTY) it\n" +
			"prints the absolute body path instead of launching an editor, so an agent\n" +
			"can edit the file directly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			path, err := a.EditPath(args[0])
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(map[string]string{"path": path})
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
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
