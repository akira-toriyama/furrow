package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// syncOutput is the agent-facing sync object: the SyncProgress fields, plus a
// revisit summary (omitted entirely when empty). The embedded pointer promotes
// {committed,pulled,pushed,conflict} flat, so the JSON shape is a superset of
// the historical one.
type syncOutput struct {
	*app.SyncProgress
	Revisit *app.RevisitSummary `json:"revisit,omitempty"`
}

// revisitScopeLabel is the tag shown after the counts: the auto repo's short
// name (segment after the last "/"), or "board" when the sync ran board-wide.
func revisitScopeLabel(a *app.App) string {
	if a.DefaultRepo != "" && a.AutoFilter {
		if i := strings.LastIndex(a.DefaultRepo, "/"); i >= 0 && i+1 < len(a.DefaultRepo) {
			return a.DefaultRepo[i+1:]
		}
		return a.DefaultRepo
	}
	return "board"
}

// revisitLine is the one human line appended after the sync summary. Empty
// summary -> "" (the caller prints nothing, so a clean board stays quiet).
func revisitLine(sum app.RevisitSummary, scope string) string {
	if sum.Empty() {
		return ""
	}
	return fmt.Sprintf("revisit: %d dep_done, %d stale (%s) — furrow revisit",
		len(sum.DepDone), len(sum.Stale), scope)
}

// syncScope builds the strict repo scope for the post-sync summary: the board's
// auto repo when auto-filtering, else the whole board.
func syncScope(a *app.App) app.QueryOpts {
	o := app.QueryOpts{}
	if a.DefaultRepo != "" && a.AutoFilter {
		o.ScopeRepo = a.DefaultRepo
	}
	return o
}

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
			"After a successful sync it also reports a revisit summary (repo-scoped counts\n" +
			"of tasks with a done dependency or gone stale) so freshly-pulled staleness\n" +
			"surfaces in the loop; the JSON gains a \"revisit\" key when non-empty.\n" +
			"It is a thin git wrapper — not a daemon or a sync server (see docs/non-goals.md).",
		Example: "  furrow sync                   # commit .furrow/, pull --rebase, push\n" +
			"  furrow sync -m \"triage inbox\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			prog, syncErr := a.Sync(message)

			// Compute the revisit summary only on a fully-successful sync (a
			// fresh, consistent, freshly-pulled board). On failure, skip it.
			var sum app.RevisitSummary
			if syncErr == nil {
				if s, err := a.RevisitSummary(syncScope(a), a.Cfg.RevisitStaleDays); err == nil {
					sum = s
				}
			}

			switch {
			case flagNDJSON:
				printNDJSONValue(syncOutput{prog, summaryPtr(sum)})
			case flagJSON:
				printJSON(syncOutput{prog, summaryPtr(sum)})
			default:
				fmt.Fprintln(out, prog.SyncSummary())
				if line := revisitLine(sum, revisitScopeLabel(a)); line != "" {
					fmt.Fprintln(out, line)
				}
			}
			return syncErr
		},
	}
	c.Flags().StringVarP(&message, "message", "m", "", "auto-commit message (default \""+app.DefaultSyncMessage+"\")")
	return c
}

// summaryPtr returns nil for an empty summary so the revisit JSON key is omitted.
func summaryPtr(sum app.RevisitSummary) *app.RevisitSummary {
	if sum.Empty() {
		return nil
	}
	return &sum
}
