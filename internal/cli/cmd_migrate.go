package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/migrate"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	var write bool
	var labels []string
	var epicRef string
	cmd := &cobra.Command{
		Use:   "migrate <task-file.md>",
		Short: "Import a Task.md-style tracker into furrow (preview unless --yes)",
		Long: "Parse a hand-maintained Task.md (## emoji lanes, ### / bold-bullet items,\n" +
			"a Done <details> archive, file:line + URL refs) into furrow tasks. Defaults\n" +
			"to a dry-run preview; pass --yes to actually create the tasks. Unmapped\n" +
			"headings and unresolved [[wikilinks]] are reported, never silently dropped.\n" +
			"Use --label to stamp every imported task with one or more labels (required\n" +
			"when the store sets [labels].required, e.g. a central cross-repo tracker).\n\n" +
			"--epic files every imported task under one box: the same\n" +
			"one-flag-for-the-whole-batch shape as --label. Omitted, an import inherits\n" +
			"the scope's single active box exactly as a bare `furrow add` does, and\n" +
			"`-e ''` imports unfiled on purpose. On a board that HAS boxes, an import\n" +
			"that lands open tasks under none is reported as a warning here rather than\n" +
			"surfacing later as one `furrow lint` epic-required error per task.",
		Example: "  furrow migrate Task.md                       # dry-run preview\n" +
			"  furrow migrate Task.md -e \"current work\" --yes\n" +
			"  furrow migrate Task.md -l imported -e e-k3m9 --yes",
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

			// Resolve the shared epic BEFORE either arm: a preview that skipped
			// this would report a plan `--yes` then refuses, and an unresolvable
			// -e would only surface after the import wrote its first body.
			// `-e ''` is add's explicit "unfiled, on purpose".
			plan, err := a.PlanImport(epicRef, cmd.Flags().Changed("epic") && epicRef == "")
			if err != nil {
				return err
			}
			statuses := make([]string, 0, len(res.Tasks))
			for _, t := range res.Tasks {
				statuses = append(statuses, t.Status)
			}
			// The store-derived warning joins the parser's own rather than
			// riding a separate channel: migrate's LOUD contract is that every
			// unmet expectation shows up in one list, in both output modes.
			warnings := res.Warnings
			if w := a.UnfiledImportWarning(plan, statuses); w != "" {
				warnings = append(append([]string(nil), warnings...), w)
			}

			if !write {
				return previewMigrate(args[0], res, labels, plan, warnings)
			}
			return applyMigrate(cmd, a, res, labels, plan, warnings)
		},
	}
	cmd.Flags().BoolVar(&write, "yes", false, "actually create the tasks (default: dry-run preview)")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "label applied to every imported task (repeatable)")
	cmd.Flags().StringVarP(&epicRef, "epic", "e", "", "epic every imported task is filed under (id, unique id prefix, or unique title substring; default: the scope's single active epic, '' imports unfiled)")
	return cmd
}

func previewMigrate(path string, res migrate.Result, labels []string, plan app.ImportPlan, warnings []string) error {
	if jsonMode() {
		tasks := res.Tasks
		if tasks == nil {
			tasks = []migrate.Task{}
		}
		if warnings == nil {
			warnings = []string{}
		}
		if labels == nil {
			labels = []string{}
		}
		emitObject(map[string]any{"dry_run": true, "source": path, "labels": labels, "epic": plan.Epic, "tasks": tasks, "warnings": warnings})
		return nil
	}
	fmt.Fprintf(out, "migrate %s — %d task(s) (dry-run)\n\n", path, len(res.Tasks))
	if len(labels) > 0 {
		fmt.Fprintf(out, "labels (applied to every task): %s\n\n", strings.Join(labels, ", "))
	}
	if plan.Epic != "" {
		suffix := ""
		if plan.Inherited {
			suffix = " (inherited from the active epic; -e '' imports unfiled)"
		}
		fmt.Fprintf(out, "epic (applied to every task): %s%s\n\n", plan.Epic, suffix)
	}
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
	if len(warnings) > 0 {
		fmt.Fprintf(out, "\n%d warning(s):\n", len(warnings))
		for _, w := range warnings {
			fmt.Fprintf(out, "  - %s\n", w)
		}
	}
	if len(res.Tasks) > 0 {
		fmt.Fprintln(out, "\nre-run with --yes to create these tasks")
	}
	return nil
}

func applyMigrate(cmd *cobra.Command, a *app.App, res migrate.Result, labels []string, plan app.ImportPlan, warnings []string) error {
	specs := make([]app.AddSpec, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		p := t.Priority
		specs = append(specs, app.AddSpec{
			Title: t.Title,
			AddOpts: app.AddOpts{
				Status:   t.Status,
				Priority: &p,
				Labels:   labels,
				Refs:     t.Refs,
				Body:     t.Body,
				// The PLAN's resolved id, never the raw ref: the preview
				// already resolved it (and already ran AddMany's inheritance),
				// so feeding the id back is what keeps "what --yes did" equal
				// to "what the dry-run said". NoEpic pins the unfiled outcome
				// for the same reason — otherwise a batch the plan called
				// unfiled would silently pick up an active box here.
				Epic:   plan.Epic,
				NoEpic: plan.Epic == "",
			},
		})
	}
	created, err := a.AddMany(specs)
	if err != nil {
		return err
	}
	noteInheritedEpic(cmd, created)
	if jsonMode() {
		if created == nil {
			created = []core.Task{}
		}
		if warnings == nil {
			warnings = []string{}
		}
		emitObject(map[string]any{"created": len(created), "epic": plan.Epic, "tasks": created, "warnings": warnings})
		return nil
	}
	fmt.Fprintf(out, "imported %d task(s)\n", len(created))
	for _, t := range created {
		fmt.Fprintf(out, "  %s  %-12s  %s\n", t.ID, t.Status, t.Title)
	}
	for _, w := range warnings {
		fmt.Fprintf(out, "warn: %s\n", w)
	}
	return nil
}
