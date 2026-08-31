package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newTidyCmd() *cobra.Command {
	var (
		doneDeps    bool
		unknownKeys bool
		yes         bool
	)
	cmd := &cobra.Command{
		Use:   "tidy",
		Short: "Prune dead bookkeeping: satisfied done-lane dep edges, parked unknown shard keys (preview unless --yes)",
		Long: "The mechanized tidy pass: report — and with --yes, prune — the two pieces of\n" +
			"dead bookkeeping a periodic hand-tidy keeps re-finding. Everything\n" +
			"judgment-shaped (what a parked dep should become, whether a task should\n" +
			"retire) stays lint/revisit's to report; tidy applies only what is safe blind.\n\n" +
			"Two classes, each behind its own selector because each is a policy call:\n\n" +
			"  --done-deps     dep edges from OPEN tasks to done-lane tasks. Satisfied\n" +
			"                  edges gate nothing (`next` treats them met) — but a board\n" +
			"                  may keep them as history on purpose (epic slices wired as\n" +
			"                  deps), so this is per-run opt-in, never config or default.\n" +
			"  --unknown-keys  the unknown keys the passthrough parks (task/epic/repo\n" +
			"                  shards and meta.json) — what `furrow upgrade` \"kept for a\n" +
			"                  human\" and lint's unknown-shard-key keeps naming. This IS\n" +
			"                  that human's tool: the only alternative was hand-editing a\n" +
			"                  machine-written shard, which the store contract forbids.\n" +
			"                  Accepting it accepts that a key a NEWER furrow wrote is\n" +
			"                  dropped with the junk — the preview names every key first.\n\n" +
			"With no selector it previews BOTH classes; --yes requires naming what to\n" +
			"apply. The apply never advances `updated` (a prune is bookkeeping, not\n" +
			"progress — respace's rule), rewrites only the shards whose bytes change, and\n" +
			"is idempotent: a re-run finds only what is still there. The board repo's git\n" +
			"history is the record of what a tidy removed.",
		Example: "  furrow tidy                        # preview both classes\n" +
			"  furrow tidy --unknown-keys --yes   # drop the parked legacy keys\n" +
			"  furrow tidy --done-deps --yes      # prune satisfied dep edges\n" +
			"  furrow tidy --json                 # machine preview",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			sel := doneDeps || unknownKeys
			if yes && !sel {
				return core.Validationf("", "tidy --yes needs an explicit selector — name what to apply: --done-deps and/or --unknown-keys")
			}
			o := app.TidyOpts{DoneDeps: doneDeps, UnknownKeys: unknownKeys, Apply: yes}
			if !sel {
				o.DoneDeps, o.UnknownKeys = true, true
			}
			rep, err := a.Tidy(o)
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(rep)
				return nil
			}
			for _, d := range rep.DoneDeps {
				fmt.Fprintf(out, "done-dep      %-8s  %s\n", d.ID, strings.Join(d.Deps, ", "))
			}
			for _, u := range rep.UnknownKeys {
				fmt.Fprintf(out, "unknown-keys  %-8s  %s (%s)\n", u.ID, strings.Join(u.Keys, ", "), u.File)
			}
			switch {
			case !rep.Changed:
				fmt.Fprintln(out, "ok — nothing to tidy")
			case rep.Applied:
				fmt.Fprintf(out, "pruned: %d task(s) with satisfied dep edges, %d record(s) with unknown keys\n",
					len(rep.DoneDeps), len(rep.UnknownKeys))
			default:
				fmt.Fprintf(out, "preview: %d task(s) with satisfied dep edges, %d record(s) with unknown keys — apply with --yes and a selector (--done-deps / --unknown-keys)\n",
					len(rep.DoneDeps), len(rep.UnknownKeys))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&doneDeps, "done-deps", false, "select the satisfied done-lane dep edges (required beside --yes to prune them)")
	cmd.Flags().BoolVar(&unknownKeys, "unknown-keys", false, "select the parked unknown shard keys (required beside --yes to drop them)")
	cmd.Flags().BoolVar(&yes, "yes", false, "actually prune the selected classes (otherwise dry-run)")
	return cmd
}
