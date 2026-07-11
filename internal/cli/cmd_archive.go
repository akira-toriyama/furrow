package cli

import (
	"fmt"
	"strings"

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
		Use:   "archive",
		Short: "Move aged done tasks to .furrow/archive/ (preview unless --yes)",
		Long: "Move done-lane tasks closed more than --older-than days ago into\n" +
			".furrow/archive/, keeping the hot index light. Without --yes it only previews\n" +
			"what would move (this is the destructive-op guard from the CLI contract).\n\n" +
			"By default the sweep is board-wide. Pass -r/--repo (repeatable) to fold only\n" +
			"one repo's aged done on a shared board without touching another's; it ANDs\n" +
			"with the age guard.",
		Args: cobra.NoArgs,
		Example: "  furrow archive --yes                       # fold every repo's aged done\n" +
			"  furrow archive -r owner/app --yes          # fold only owner/app's aged done\n" +
			"  furrow archive -r app -r lib --older-than 7 --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			days := a.Cfg.ArchiveOlderThanDays
			if cmd.Flags().Changed("older-than") {
				days = olderThan
			}
			var repos []string
			if cmd.Flags().Changed("repo") {
				repos, err = a.ResolveRepos(repoArgs)
				if err != nil {
					return err
				}
			}
			dry := !yes
			moved, err := a.Archive(days, dry, repos...)
			if err != nil {
				return err
			}
			if jsonMode() {
				if moved == nil {
					moved = []core.Task{} // array shape, never null
				}
				if repos == nil {
					repos = []string{} // array shape, never null
				}
				emitObject(map[string]any{"dry_run": dry, "older_than_days": days, "repos": repos, "tasks": moved})
				return nil
			}
			verb := "archived"
			if dry {
				verb = "would archive"
			}
			scope := ""
			if len(repos) > 0 {
				scope = " in " + strings.Join(repos, ", ")
			}
			fmt.Fprintf(out, "%s %d task(s) closed >%dd ago%s\n", verb, len(moved), days, scope)
			for _, t := range moved {
				fmt.Fprintf(out, "  %s  %s\n", t.ID, t.Title)
			}
			if dry && len(moved) > 0 {
				fmt.Fprintln(out, "re-run with --yes to apply")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&olderThan, "older-than", 0, "age in days (default: config archive.older_than_days)")
	cmd.Flags().BoolVar(&yes, "yes", false, "actually move (required; otherwise dry-run)")
	cmd.Flags().StringSliceVarP(&repoArgs, "repo", "r", nil, "limit the sweep to these repos (owner/repo or a unique short name; repeatable)")
	return cmd
}
