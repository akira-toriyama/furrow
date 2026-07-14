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

// boardScopeRepo is the board's auto scope repo (owner/repo) when auto-filtering
// is on, else "" (whole board). Shared by the sync summary's scope and its label.
func boardScopeRepo(a *app.App) string {
	if a.DefaultRepo != "" && a.AutoFilter {
		return a.DefaultRepo
	}
	return ""
}

// revisitScopeLabel is the tag shown after the counts: the auto repo's short
// name (segment after the last "/"), or "board" when the sync ran board-wide.
func revisitScopeLabel(a *app.App) string {
	repo := boardScopeRepo(a)
	if repo == "" {
		return "board"
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 && i+1 < len(repo) {
		return repo[i+1:]
	}
	return repo
}

// revisitLine is the one human line appended after the sync summary. Empty
// summary -> "" (the caller prints nothing, so a clean board stays quiet). When
// a repo's human review has gone stale it appends an unreviewed nudge (the
// oldest few, so the line stays short).
func revisitLine(sum app.RevisitSummary, scope string) string {
	if sum.Empty() {
		return ""
	}
	line := fmt.Sprintf("revisit: %d dep_done, %d stale (%s) — furrow revisit",
		len(sum.DepDone), len(sum.Stale), scope)
	if len(sum.Unreviewed) > 0 {
		line += "\n" + unreviewedLine(sum.Unreviewed)
	}
	return line
}

// unreviewedLine renders the per-repo staleness nudge: "⚠ N repo(s) unreviewed:
// owner/repo (21d), … — furrow review <repo>". At most three repos are named
// (the store-sorted first few) so the line stays legible; the count is exact.
func unreviewedLine(repos []app.UnreviewedRepo) string {
	const maxNamed = 3
	named := repos
	if len(named) > maxNamed {
		named = named[:maxNamed]
	}
	parts := make([]string, len(named))
	for i, r := range named {
		parts[i] = fmt.Sprintf("%s (%dd)", r.Repo, r.Days)
	}
	suffix := ""
	if len(repos) > len(named) {
		suffix = fmt.Sprintf(", +%d more", len(repos)-len(named))
	}
	return fmt.Sprintf("⚠ %d repo(s) unreviewed: %s%s — furrow review <repo>",
		len(repos), strings.Join(parts, ", "), suffix)
}

// syncScope builds the strict repo scope for the post-sync summary: the board's
// auto repo when auto-filtering, else the whole board.
func syncScope(a *app.App) app.QueryOpts {
	return app.QueryOpts{ScopeRepo: boardScopeRepo(a)}
}

// newSyncCmd wires `furrow sync` — the multi-machine ritual (commit the board,
// pull --rebase, push) as one non-interactive command. The progress object is
// printed to stdout on success AND failure (the error envelope still goes to
// stderr), so an agent always learns how far the sync got.
func newSyncCmd() *cobra.Command {
	var message string
	var bodies []string
	var allBodies bool
	c := &cobra.Command{
		Use:   "sync",
		Short: "Commit the board, pull --rebase, push (thin git wrapper)",
		Long: "sync runs the multi-machine board ritual as one command, against the git\n" +
			"repository enclosing the board:\n\n" +
			"  1. auto-commit, scoped to the .furrow/ directory (other dirty files in the\n" +
			"     repo are never swept in). Within .furrow/, machine-written shards\n" +
			"     (tasks/, meta.json) are always committed; a hand-edited bodies/<id>.md\n" +
			"     is committed only when it is new or named with -b/--body — a merely\n" +
			"     modified body is left for its author (listed in pending_bodies) so a\n" +
			"     shared checkout never commits a co-located operator's WIP. --all-bodies\n" +
			"     restores the old sweep for a checkout you know is yours alone.\n" +
			"  2. git fetch, then git rebase --autostash @{u} (onto the tracking ref, not\n" +
			"     FETCH_HEAD, so a co-writer's concurrent fetch can't race it)\n" +
			"  3. git push (one pull→push retry on non-fast-forward)\n\n" +
			"On a rebase conflict the rebase is aborted automatically (the board is never\n" +
			"left with conflict markers; your local sync commit survives) and the error\n" +
			"envelope carries id \"sync-conflict\" plus the conflicted paths. A co-writer's\n" +
			"fetch racing a ref/index lock during the pull is waited out with a bounded\n" +
			"backoff; a live race clears in under a second, so a lock that persists is a\n" +
			"likely-stale .git/*.lock and sync fails terminally naming it (a pre-flight\n" +
			"foreign rebase that stays stuck instead exits with the retryable id\n" +
			"\"sync-busy\").\n\n" +
			"Your OWN dirty files are autostashed for the rebase. If git cannot put them\n" +
			"back (its re-apply conflicts with what was pulled), it keeps them in the stash,\n" +
			"warns only on stderr, and exits 0 — so sync probes the stash itself and fails\n" +
			"with id \"sync-stash-stranded\" (nothing is pushed), reporting them in\n" +
			"pending_stash until they are popped. The unmerged index that failure leaves\n" +
			"behind is explained by a pre-flight (id \"sync-unmerged\", exit 2), not relayed\n" +
			"as git's opaque \"<path>: unmerged\". Relatedly, a body still carrying conflict\n" +
			"markers is never auto-committed (id \"body-conflict-marker\", exit 2): a commit\n" +
			"cannot be un-published, and `furrow lint` flags any that got in already.\n\n" +
			"The progress object {committed, pulled, pushed, conflict, committed_bodies,\n" +
			"pending_bodies, pending_stash}\n" +
			"goes to stdout even on failure. After a successful sync it also reports a\n" +
			"revisit summary (repo-scoped counts of tasks with a done dependency or gone\n" +
			"stale, plus any repos whose human review is older than [review].stale_after_days\n" +
			"— run furrow review <repo>) so freshly-pulled staleness surfaces in the loop;\n" +
			"the JSON gains a \"revisit\" key when non-empty. It is a thin git wrapper — not a\n" +
			"daemon or a sync server (see docs/non-goals.md).",
		Example: "  furrow sync                   # commit shards + new bodies, pull --rebase, push\n" +
			"  furrow sync -m \"triage inbox\"\n" +
			"  furrow sync -b t-k3m9p        # also commit that task's edited body\n" +
			"  furrow sync --all-bodies      # commit every dirty body (solo checkout)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			prog, syncErr := a.Sync(cmd.Context(), app.SyncOpts{Message: message, Bodies: bodies, AllBodies: allBodies})

			// Compute the revisit summary only on a fully-successful sync (a
			// fresh, consistent, freshly-pulled board). On failure, skip it.
			var sum app.RevisitSummary
			if syncErr == nil {
				if s, err := a.RevisitSummary(syncScope(a), a.Cfg.RevisitStaleDays); err == nil {
					sum = s
				} else {
					fmt.Fprintf(errOut, "revisit summary skipped: %v\n", err)
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
				if len(prog.PendingBodies) > 0 {
					fmt.Fprintf(errOut, "note: %d body edit(s) left uncommitted (rerun with -b <id> or --all-bodies): %s\n",
						len(prog.PendingBodies), strings.Join(prog.PendingBodies, ", "))
				}
				// A stranded autostash is reported on EVERY sync, not just the one that
				// stranded it: the entry sits there silently until someone pops it, and
				// the whole defect this guards against is a leftover nobody was told about.
				// (The sync that created it also fails — see sync-stash-stranded.)
				if len(prog.PendingStash) > 0 {
					fmt.Fprintf(errOut, "warning: %d autostash entr(ies) hold working-tree changes git could not restore — %s\n"+
						"  recover with `git stash pop` (or `git stash drop` if the changes are already in the tree)\n",
						len(prog.PendingStash), stashNote(prog.PendingStash))
				}
			}
			return syncErr
		},
	}
	c.Flags().StringVarP(&message, "message", "m", "", "auto-commit message (default \""+app.DefaultSyncMessage+"\")")
	c.Flags().StringSliceVarP(&bodies, "body", "b", nil, "also commit these task ids' hand-edited bodies/<id>.md (repeatable)")
	c.Flags().BoolVar(&allBodies, "all-bodies", false, "commit every dirty body (the pre-scoping sweep; only on a checkout that is yours alone)")
	return c
}

// stashNote renders the stranded autostash entries for the human warning:
// "stash@{0}: .furrow/bodies/t-k3m9p.md, notes.md". The paths are the point — they
// answer "is my body in there?" without a second command.
func stashNote(entries []app.StashEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Ref
		if len(e.Paths) > 0 {
			parts[i] += ": " + strings.Join(e.Paths, ", ")
		}
	}
	return strings.Join(parts, "; ")
}

// summaryPtr returns nil for an empty summary so the revisit JSON key is omitted.
func summaryPtr(sum app.RevisitSummary) *app.RevisitSummary {
	if sum.Empty() {
		return nil
	}
	return &sum
}
