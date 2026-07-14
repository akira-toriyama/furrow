package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// newUpgradeCmd is the ONE deliberate way to raise a board's on-disk layout
// version. Every ordinary write refuses to do it (schema-upgrade-required, exit
// 2) — which is the whole point: on 2026-07-13 a routine `furrow sync` from a
// source build silently migrated the shared tracker 3->4 and every release the
// fleet's CI pinned lost the board at once.
//
// So this previews by default (the `archive` guard) and prints the flag-day
// checklist, because furrow cannot see the pins it is about to break.
func newUpgradeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Raise the board's on-disk layout to this furrow's schema (flag day; preview unless --yes)",
		Long: "Raise .furrow/meta.json's schema_version — and the archive store's, if there\n" +
			"is one — to the layout this binary writes, re-serializing every shard through\n" +
			"the current marshaller. This is the ONLY thing that moves a board's version:\n" +
			"an ordinary write never does (it refuses with schema-upgrade-required, exit 2),\n" +
			"so a layout migration can never again happen as the side effect of a sync.\n\n" +
			"It is a FLAG DAY. Afterwards, no older furrow can write this board — including\n" +
			"any CI pinned to an older release. furrow cannot see those pins, so the order\n" +
			"is yours to keep: release furrow, bump every caller's pin, THEN upgrade.\n\n" +
			"A board already on the current layout is a clean no-op (changed:false, exit 0).",
		Example: "  furrow upgrade          # preview: what would change\n" +
			"  furrow upgrade --yes    # apply, then `furrow sync` to publish it",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			rep, err := a.Upgrade(yes)
			if err != nil {
				return err
			}
			switch {
			case flagNDJSON, flagJSON:
				emitObject(rep)
			default:
				printUpgradeHuman(rep, a.Cfg.Standalone)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually raise the board (default: preview only)")
	return cmd
}

// printUpgradeHuman renders the upgrade preview/result. On a standalone board
// (config `standalone = true`) it drops the shared-board flag-day checklist and
// the `furrow sync` publish line — a single-machine board with no remote has no
// pinned CI to coordinate and nothing to publish, so that guidance only
// misdirects. Behavior is identical; only the wording differs.
func printUpgradeHuman(rep *app.UpgradeReport, standalone bool) {
	if !rep.Changed {
		fmt.Fprintf(out, "board is already schema v%d — nothing to do\n", rep.To)
		return
	}
	for _, s := range rep.Stores {
		from := fmt.Sprintf("v%d", s.From)
		if s.From == 0 {
			from = "unstamped"
		}
		fmt.Fprintf(out, "%s\n  schema: %s -> v%d (%d shard(s) re-serialized)\n", s.Path, from, s.To, s.Tasks)
	}
	if !rep.Applied {
		if standalone {
			fmt.Fprint(out, "\nstandalone board — no CI or other machines depend on it, so there is no\n")
			fmt.Fprint(out, "flag day to coordinate. Back up the board dir if you like, then apply.\n")
			fmt.Fprint(out, "\npreview — re-run with --yes to apply\n")
			return
		}
		fmt.Fprintf(out, "\nFLAG DAY — after --yes, only furrow releases that know schema v%d can write this\n", rep.To)
		fmt.Fprint(out, "board. Anything pinned to an older release loses it (that is how the fleet's\n")
		fmt.Fprint(out, "task-status CI broke on 2026-07-13). Do this first:\n")
		fmt.Fprint(out, "  1. release a furrow that ships this schema\n")
		fmt.Fprint(out, "  2. bump every caller's sync-task-status.yml@vX.Y.Z pin to it\n")
		fmt.Fprint(out, "  3. re-run with --yes, then `furrow sync`\n")
		fmt.Fprint(out, "\npreview — re-run with --yes to apply\n")
		return
	}
	if standalone {
		fmt.Fprint(out, "\nupgraded.\n")
		return
	}
	fmt.Fprint(out, "\nupgraded — run `furrow sync` to publish it\n")
}
