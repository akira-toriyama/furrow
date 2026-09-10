package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

const rmLongTail = "It is NOT `archive`: archive RETIRES done work into .furrow/archive/ and is a\n" +
	"round trip (`unarchive`); rm WITHDRAWS a record that should not have been\n" +
	"filed — shard, body, and attached assets are deleted, and nothing brings them\n" +
	"back but the board repo's git history. Everyday parking is still the icebox\n" +
	"lane; rm is the exception for a filing made before the decision was.\n\n" +
	"Preview unless --yes (the destructive-op guard archive and tidy share). A\n" +
	"target something still points at is refused — exit 2, kind `referenced`,\n" +
	"every reference in details.references — unless --force, which SEVERS them:\n" +
	"a dep edge is dropped, a live [[id]] link is de-linked to the bare id (the\n" +
	"prose keeps its words), a member is unfiled, an epic dep is dropped. Severing\n" +
	"never advances `updated` (bookkeeping, not progress). The id is never reused.\n" +
	"Guarded like every write (the session write guard), on the targets and on\n" +
	"every entity --force edits; the deletion is one furrow-owned change, so a plain\n" +
	"`furrow sync` (or autocommit) publishes it."

func newRmCmd() *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rm <id>...",
		Short: "Delete tasks outright — withdraw a filing, never retire done work (preview unless --yes)",
		Long: "Delete tasks — the record itself, not a lane change. All-or-nothing: a miss\n" +
			"removes nothing (exit 1, details.missing; an archived id says so — unarchive\n" +
			"it first). References among the targets themselves never count, so a chain\n" +
			"removes in one call.\n\n" + rmLongTail + "\n\n" +
			"--json prints one report: {dry_run, force, tasks, references} — `tasks` as\n" +
			"they were, `references` what stands (and, with --force, was severed).",
		Example: "  furrow rm t-k3m9p                 # preview: what would go, what points at it\n" +
			"  furrow rm t-k3m9p --yes           # delete (refused while referenced)\n" +
			"  furrow rm t-k3m9p --force --yes   # sever the references, then delete\n" +
			"  furrow rm t-a1b2c t-d4e5f --yes   # several, one all-or-nothing write",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			rep, err := a.RemoveTasks(args, app.RemoveOpts{Force: force, Apply: yes})
			if err != nil {
				return err
			}
			extra := sessionGuardExtra(a)
			if jsonMode() {
				payload := map[string]any{"dry_run": rep.DryRun, "force": rep.Force, "tasks": rep.Tasks, "references": rep.References}
				emitObject(mergeExtra(payload, extra))
				return nil
			}
			fmt.Fprintf(out, "%s %d task(s)\n", rmVerb(rep.DryRun), len(rep.Tasks))
			for _, t := range rep.Tasks {
				fmt.Fprintf(out, "  %s  %s\n", t.ID, t.Title)
			}
			printReferences(rep.References, rep.DryRun)
			if rep.DryRun {
				fmt.Fprintln(out, "re-run with --yes to apply")
			}
			return nil
		},
	}
	addRmFlags(cmd, &force, &yes)
	return cmd
}

func newEpicRmCmd() *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rm <epic>",
		Short: "Delete an epic outright — withdraw a box, never close one (preview unless --yes)",
		Long: "Delete a box — the record itself, not `epic done`. Its references are its\n" +
			"members (each task's `epic` field), the boxes whose deps name it, and the live\n" +
			"[[e-…]] links in any body but its own.\n\n" + rmLongTail + "\n\n" +
			"--json prints one report: {dry_run, force, epic, references}. An unfiled\n" +
			"member is what `lint`'s epic-required then names — refile it with `set -e`.",
		Example: "  furrow epic rm e-k3m9                 # preview\n" +
			"  furrow epic rm e-k3m9 --yes           # delete (refused while referenced)\n" +
			"  furrow epic rm e-k3m9 --force --yes   # unfile the members, sever, delete",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			rep, err := a.RemoveEpic(args[0], app.RemoveOpts{Force: force, Apply: yes})
			if err != nil {
				return err
			}
			extra := sessionGuardExtra(a)
			if jsonMode() {
				payload := map[string]any{"dry_run": rep.DryRun, "force": rep.Force, "epic": rep.Epic, "references": rep.References}
				emitObject(mergeExtra(payload, extra))
				return nil
			}
			fmt.Fprintf(out, "%s epic\n  %s  %s\n", rmVerb(rep.DryRun), rep.Epic.ID, rep.Epic.Title)
			printReferences(rep.References, rep.DryRun)
			if rep.DryRun {
				fmt.Fprintln(out, "re-run with --yes to apply")
			}
			return nil
		},
	}
	addRmFlags(cmd, &force, &yes)
	return cmd
}

func addRmFlags(cmd *cobra.Command, force, yes *bool) {
	cmd.Flags().BoolVar(force, "force", false, "sever what still references the target (dep edges, [[links]], members) instead of refusing")
	cmd.Flags().BoolVar(yes, "yes", false, "actually delete (required; otherwise dry-run)")
}

func rmVerb(dry bool) string {
	if dry {
		return "would remove"
	}
	return "removed"
}

// printReferences renders the reference list under the target lines — what
// --force would sever on a preview, what it did sever on an apply. Silent
// when nothing referenced the targets (a report that passed on its own).
func printReferences(r app.References, dry bool) {
	if r.Empty() {
		return
	}
	if dry {
		fmt.Fprintln(out, "references (severed by --force):")
	} else {
		fmt.Fprintln(out, "references severed:")
	}
	for _, d := range r.Deps {
		fmt.Fprintf(out, "  dep      %s -> %s\n", d.From, d.To)
	}
	for _, m := range r.Members {
		fmt.Fprintf(out, "  member   %s in %s (unfiled)\n", m.Task, m.Epic)
	}
	for _, d := range r.EpicDeps {
		fmt.Fprintf(out, "  epic dep %s -> %s\n", d.From, d.To)
	}
	for _, l := range r.Links {
		fmt.Fprintf(out, "  link     %s [[%s]]\n", core.BodyPath(l.Body), l.To)
	}
}
