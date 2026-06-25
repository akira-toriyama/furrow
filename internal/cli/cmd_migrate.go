package cli

import (
	"fmt"
	"os"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/migrate"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "migrate <task-file.md>",
		Short: "Import a Task.md-style tracker into furrow (preview unless --write)",
		Long: "Parse a hand-maintained Task.md (## emoji lanes, ### / bold-bullet items,\n" +
			"a Done <details> archive, file:line + URL refs) into furrow tasks. Defaults\n" +
			"to a dry-run preview; pass --write to actually create the tasks. Unmapped\n" +
			"headings and unresolved [[wikilinks]] are reported, never silently dropped.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return core.Validationf("", "read %q: %v", args[0], err)
			}
			res := migrate.Parse(string(data), a.Cfg.Lanes, a.Cfg.DefaultLane, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)

			if !write {
				return previewMigrate(args[0], res)
			}
			return applyMigrate(a, res)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "actually create the tasks (default: dry-run preview)")
	return cmd
}

func previewMigrate(path string, res migrate.Result) error {
	if flagJSON {
		printJSON(map[string]any{"dry_run": true, "source": path, "tasks": res.Tasks, "warnings": res.Warnings})
		return nil
	}
	fmt.Fprintf(out, "migrate %s — %d task(s) (dry-run)\n\n", path, len(res.Tasks))
	wLane := len("LANE")
	for _, t := range res.Tasks {
		if len(t.Status) > wLane {
			wLane = len(t.Status)
		}
	}
	fmt.Fprintf(out, "%-*s  %5s  %4s  %s\n", wLane, "LANE", "PRIO", "REFS", "TITLE")
	for _, t := range res.Tasks {
		fmt.Fprintf(out, "%-*s  %5d  %4d  %s\n", wLane, t.Status, t.Priority, len(t.Refs), t.Title)
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(out, "\n%d warning(s):\n", len(res.Warnings))
		for _, w := range res.Warnings {
			fmt.Fprintf(out, "  - %s\n", w)
		}
	}
	if len(res.Tasks) > 0 {
		fmt.Fprintln(out, "\nre-run with --write to create these tasks")
	}
	return nil
}

func applyMigrate(a *app.App, res migrate.Result) error {
	specs := make([]app.AddSpec, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		p := t.Priority
		specs = append(specs, app.AddSpec{
			Title: t.Title,
			AddOpts: app.AddOpts{
				Status:   t.Status,
				Priority: &p,
				Refs:     t.Refs,
				Body:     t.Body,
			},
		})
	}
	created, err := a.AddMany(specs)
	if err != nil {
		return err
	}
	if flagJSON {
		printJSON(map[string]any{"created": len(created), "tasks": created, "warnings": res.Warnings})
		return nil
	}
	fmt.Fprintf(out, "imported %d task(s)\n", len(created))
	for _, t := range created {
		fmt.Fprintf(out, "  %s  %-12s  %s\n", t.ID, t.Status, t.Title)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warn: %s\n", w)
	}
	return nil
}
