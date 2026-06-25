package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newArchiveCmd() *cobra.Command {
	var (
		olderThan int
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Move aged done tasks to .furrow/archive/ (preview unless --yes)",
		Long: "Move done-lane tasks closed more than --older-than days ago into\n" +
			".furrow/archive/, keeping the hot index light. Without --yes it only previews\n" +
			"what would move (this is the destructive-op guard from the CLI contract).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			days := a.Cfg.ArchiveOlderThanDays
			if cmd.Flags().Changed("older-than") {
				days = olderThan
			}
			dry := !yes
			moved, err := a.Archive(days, dry)
			if err != nil {
				return err
			}
			if flagJSON {
				if moved == nil {
					moved = []core.Task{} // array shape, never null
				}
				printJSON(map[string]any{"dry_run": dry, "older_than_days": days, "tasks": moved})
				return nil
			}
			verb := "archived"
			if dry {
				verb = "would archive"
			}
			fmt.Fprintf(out, "%s %d task(s) closed >%dd ago\n", verb, len(moved), days)
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
	return cmd
}
