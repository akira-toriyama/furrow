package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newArchiveCmd() *cobra.Command {
	var (
		olderThan int
		yes       bool
		repoArgs  []string
	)
	cmd := &cobra.Command{
		Use:   "archive [<id>...]",
		Short: "Retire done tasks to .furrow/archive/ — by id, or the aged sweep (preview unless --yes)",
		Long: "Move done-lane tasks into .furrow/archive/, keeping the hot index light.\n" +
			"With one or more <id>s it retires exactly those tasks (each must be in the\n" +
			"done lane); with no id it sweeps every done task closed more than --older-than\n" +
			"days ago. Without --yes it only previews what would move (the destructive-op\n" +
			"guard from the CLI contract).\n\n" +
			"The age sweep INHERITS THE BOARD SCOPE, like every read: with no -r it folds\n" +
			"only the aged done of the repo your ls/next/search are already scoped to. An\n" +
			"explicit -r (repeatable) swaps that scope; -r '' sweeps the whole board, and\n" +
			"has to be typed. Both AND with the age guard. -r/--older-than apply to the\n" +
			"sweep only, not to an explicit id list.",
		Args: cobra.ArbitraryArgs,
		Example: "  furrow archive t-k3m9p --yes               # retire one finished task\n" +
			"  furrow archive t-k3m9p t-a1b2c --yes       # retire several by id\n" +
			"  furrow archive --yes                       # fold the board scope's aged done\n" +
			"  furrow archive -r '' --yes                 # fold EVERY repo's aged done\n" +
			"  furrow archive -r owner/app --older-than 7 --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			dry := !yes
			// By-id retire: an explicit list ignores the age/scope knobs, so reject
			// them together rather than silently drop them.
			if len(args) > 0 {
				if cmd.Flags().Changed("older-than") || cmd.Flags().Changed("repo") {
					return core.Validationf("", "archive <id>... cannot be combined with --older-than or -r/--repo (those scope the age sweep)")
				}
				moved, err := a.ArchiveIDs(args, dry)
				if err != nil {
					return err
				}
				emitArchive(moved, dry, true, 0, nil)
				return nil
			}
			days := a.Cfg.ArchiveOlderThanDays
			if cmd.Flags().Changed("older-than") {
				days = olderThan
			}
			repos, err := sweepRepos(cmd, a, repoArgs)
			if err != nil {
				return err
			}
			moved, err := a.Archive(days, dry, repos...)
			if err != nil {
				return err
			}
			emitArchive(moved, dry, false, days, repos)
			return nil
		},
	}
	cmd.Flags().IntVar(&olderThan, "older-than", 0, "age in days (default: config archive.older_than_days) — sweep only")
	cmd.Flags().BoolVar(&yes, "yes", false, "actually move (required; otherwise dry-run)")
	cmd.Flags().StringSliceVarP(&repoArgs, "repo", "r", nil, "scope the sweep to these repos (owner/repo or a unique short name; repeatable); -r '' = the whole board — sweep only")
	return cmd
}

// emitArchive renders an archive result — a by-id retire (byID) or the age sweep.
// For the sweep, days/repos describe the selection and ride along in JSON; a
// by-id retire omits them (the id list was explicit). Human output previews with
// "would archive …" on a dry run. Honors --json (indented) and --ndjson (compact).
func emitArchive(moved []core.Task, dry, byID bool, days int, repos []string) {
	if moved == nil {
		moved = []core.Task{} // array shape, never null
	}
	if jsonMode() {
		payload := map[string]any{"dry_run": dry, "tasks": moved}
		if !byID {
			if repos == nil {
				repos = []string{}
			}
			payload["older_than_days"] = days
			payload["repos"] = repos
		}
		emitObject(payload)
		return
	}
	verb := "archived"
	if dry {
		verb = "would archive"
	}
	if byID {
		fmt.Fprintf(out, "%s %d task(s) by id\n", verb, len(moved))
	} else {
		// Name the scope in BOTH directions. The wide variant is now the one that had
		// to be asked for (-r ''), which is exactly why it must still say so: it is
		// the only shape whose blast radius exceeds what the reads in this cwd show.
		// "whole board" is the word `board` already prints for an empty scope.
		scope := " across the whole board"
		if len(repos) > 0 {
			scope = " in " + strings.Join(repos, ", ")
		}
		fmt.Fprintf(out, "%s %d task(s) closed >%dd ago%s\n", verb, len(moved), days, scope)
	}
	for _, t := range moved {
		fmt.Fprintf(out, "  %s  %s\n", t.ID, t.Title)
	}
	if dry && len(moved) > 0 {
		fmt.Fprintln(out, "re-run with --yes to apply")
	}
}

// sweepRepos resolves the age sweep's repo selection — the write-side twin of
// scopedQuery, and deliberately the same rule: -r is the scope control, and it
// narrows FROM the board scope. With no -r the sweep inherits the board's
// DefaultRepo (when AutoFilter is on), so it retires exactly the tasks the reads
// in this cwd have been showing; an explicit -r replaces that scope, and -r ""
// escapes to the whole board.
//
// The sweep used to ignore the board scope entirely, which made -r mean "narrow
// from ALL repos" here and "narrow from my board scope" in every read — one word,
// two meanings, on the one command that moves files. Inheriting costs the widest
// blast radius the most keystrokes instead of the fewest.
func sweepRepos(cmd *cobra.Command, a *app.App, repoArgs []string) ([]string, error) {
	if cmd.Flags().Changed("repo") {
		return a.ResolveRepos(repoArgs)
	}
	if a.DefaultRepo != "" && a.AutoFilter {
		return []string{a.DefaultRepo}, nil
	}
	return nil, nil
}
