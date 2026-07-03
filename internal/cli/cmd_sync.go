package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// newSyncCmd wires `furrow sync` — the multi-machine ritual (commit the board,
// pull --rebase, push) as one non-interactive command. The progress object is
// printed to stdout on success AND failure (the error envelope still goes to
// stderr), so an agent always learns how far the sync got.
func newSyncCmd() *cobra.Command {
	var message string
	c := &cobra.Command{
		Use:   "sync",
		Short: "Commit the board, pull --rebase, push (thin git wrapper)",
		Long: "sync runs the multi-machine board ritual as one command, against the git\n" +
			"repository enclosing the board:\n\n" +
			"  1. auto-commit, pathspec-limited to the .furrow/ directory (other dirty\n" +
			"     files in the repo are never swept in)\n" +
			"  2. git -c rebase.autoStash=true pull --rebase\n" +
			"  3. git push (one pull→push retry on non-fast-forward)\n\n" +
			"On a rebase conflict the rebase is aborted automatically (the board is never\n" +
			"left with conflict markers; your local sync commit survives) and the error\n" +
			"envelope carries id \"sync-conflict\" plus the conflicted paths. The progress\n" +
			"object {committed, pulled, pushed, conflict} goes to stdout even on failure.\n" +
			"It is a thin git wrapper — not a daemon or a sync server (see docs/non-goals.md).",
		Example: "  furrow sync                   # commit .furrow/, pull --rebase, push\n" +
			"  furrow sync -m \"triage inbox\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			prog, err := a.Sync(message)
			switch {
			case flagNDJSON:
				printNDJSONValue(prog) // one compact line, honoring the NDJSON contract
			case flagJSON:
				printJSON(prog)
			default:
				fmt.Fprintln(out, prog.SyncSummary())
			}
			return err
		},
	}
	c.Flags().StringVarP(&message, "message", "m", "", "auto-commit message (default \""+app.DefaultSyncMessage+"\")")
	return c
}
