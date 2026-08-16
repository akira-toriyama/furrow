package cli

import (
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newUnarchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unarchive <id>...",
		Short: "Restore archived tasks to the hot board (archive's inverse)",
		Long: "Move tasks BACK from .furrow/archive/ to the hot board — the inverse of\n" +
			"`furrow archive <id>`, so archiving is a round trip, not a one-way door\n" +
			"(before this, recovery meant hand-moving furrow-owned shards between the\n" +
			"two stores, which the docs forbid).\n\n" +
			"All-or-nothing, like every batch mutator: each id must be in the archive\n" +
			"store, or nothing is restored — a miss exits 1 with every miss in\n" +
			"details.missing, and an id already on the hot board is exit 2 (nothing to\n" +
			"restore). --json is ALWAYS an array of {before,after,changed} envelopes,\n" +
			"one per id (before is null — the task was not on the hot board — and each\n" +
			"envelope carries `unarchived: true`); --ndjson streams one per line.\n\n" +
			"A restored task comes back EXACTLY as it was archived: done lane, closed\n" +
			"stamp, every field preserved (its body and attached assets travel back\n" +
			"too). Restoring only puts it back on the board — to REOPEN it, follow with\n" +
			"`furrow move <id> <lane>`, which clears `closed` on leaving the done lane.\n" +
			"No --yes: restoring destroys nothing (the destructive direction, archive,\n" +
			"keeps its preview guard).",
		Example: "  furrow unarchive t-k3m9p\n" +
			"  furrow unarchive t-k3m9p t-x7q2          # one all-or-nothing write\n" +
			"  furrow unarchive t-k3m9p && furrow move t-k3m9p backlog   # restore AND reopen",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			return emitMutationManyWith(cmd, a, "unarchived", args,
				func() ([]*core.Task, error) {
					moved, err := a.Unarchive(args)
					if err != nil {
						return nil, err
					}
					out := make([]*core.Task, len(moved))
					for i := range moved {
						out[i] = &moved[i]
					}
					return out, nil
				},
				func(*core.Task) map[string]any { return map[string]any{"unarchived": true} })
		},
	}
}
