package cli

import (
	"fmt"
	"sort"
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
	Revisit *app.RevisitSummary   `json:"revisit,omitempty"`
	Lint    *app.LintErrorSummary `json:"lint,omitempty"`
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
	line := fmt.Sprintf("revisit: %d dep_done, %d stale", len(sum.DepDone), len(sum.Stale))
	// Epic counts ride the same line, but only when non-zero — so a board with no
	// boxes prints a short nudge. EVERY epic key the summary counts must appear
	// here: Empty() gates this line, so a key counted but not rendered would
	// print a nudge that names nothing (measured with epic_review_due pre-fix).
	for _, c := range []struct {
		label string
		ids   []string
	}{{"epic_all_done", sum.EpicAllDone}, {"epic_stuck", sum.EpicStuck}, {"epic_stale", sum.EpicStale}, {"epic_dep_done", sum.EpicDepDone}, {"epic_review_due", sum.EpicReviewDue}} {
		if len(c.ids) > 0 {
			line += fmt.Sprintf(", %d %s", len(c.ids), c.label)
		}
	}
	line += fmt.Sprintf(" (%s) — furrow revisit", scope)
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
	// Name the precondition inline: a bare `furrow review <repo>` records a HUMAN
	// review (advancing this very clock), so an agent that blindly runs it fakes a
	// human review and silences the nudge without one happening. Tell the agent to
	// use --by agent (logs a sweep, leaves the human clock alone) instead.
	return fmt.Sprintf("⚠ %d repo(s) unreviewed: %s%s — human review: `furrow review <repo>`; an agent sweep must use `furrow review <repo> --by agent` (a bare review counts as a HUMAN review)",
		len(repos), strings.Join(parts, ", "), suffix)
}

// pendingBodiesNote is the stderr nudge when a sync deliberately left modified
// bodies uncommitted. It leads with the SAFE option (-b <id>, commits only what
// you name) and attaches --all-bodies' "yours-alone" precondition inline (t-nk62)
// — furrow must not offer --all-bodies as an equal one-word shortcut an agent
// reaches for on a shared checkout, sweeping a co-operator's WIP under it.
func pendingBodiesNote(ids []string) string {
	return fmt.Sprintf("note: %d body edit(s) left uncommitted — commit yours with `furrow sync -b <id>` (%s); --all-bodies commits ALL dirty bodies and is safe only on a checkout that is yours alone",
		len(ids), strings.Join(ids, ", "))
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
			"     shared checkout never commits a co-located operator's WIP — except a\n" +
			"     body furrow ITSELF wrote (note / edit --body / done --note / apply):\n" +
			"     the writing command journals the id per-checkout and a plain sync\n" +
			"     publishes it as if named with -b. --all-bodies\n" +
			"     restores the old sweep for a checkout you know is yours alone. A file\n" +
			"     furrow does not own (an editor swap, a backup ~, a stray .tmp-*) is\n" +
			"     never committed and is disclosed in foreign_files + a stderr note.\n" +
			"  2. git fetch, then git rebase --autostash @{u} (onto the tracking ref, not\n" +
			"     FETCH_HEAD, so a co-writer's concurrent fetch can't race it)\n" +
			"  3. git push (one pull→push retry on non-fast-forward)\n\n" +
			"On a rebase conflict the rebase is aborted automatically (the board is never\n" +
			"left with conflict markers; your local sync commit survives) and the error\n" +
			"envelope carries kind \"sync-conflict\" plus the conflicted paths. A co-writer's\n" +
			"fetch racing a ref/index lock during the pull is waited out with a bounded\n" +
			"backoff; a live race clears in under a second, so a lock that persists is a\n" +
			"likely-stale .git/*.lock and sync fails terminally naming it (kind\n" +
			"\"sync-lock-stale\"; a pre-flight\n" +
			"foreign rebase that stays stuck instead exits with the retryable kind\n" +
			"\"sync-busy\").\n\n" +
			"Your OWN dirty files are autostashed for the rebase. If git cannot put them\n" +
			"back (its re-apply conflicts with what was pulled), it keeps them in the stash,\n" +
			"warns only on stderr, and exits 0 — so sync probes the stash itself and fails\n" +
			"with kind \"sync-stash-stranded\" (nothing is pushed), reporting them in\n" +
			"pending_stash until they are popped. The unmerged index that failure leaves\n" +
			"behind is explained by a pre-flight (kind \"sync-unmerged\", exit 2), not relayed\n" +
			"as git's opaque \"<path>: unmerged\". Relatedly, a body still carrying conflict\n" +
			"markers is never auto-committed (kind \"body-conflict-marker\", exit 2): a commit\n" +
			"cannot be un-published, and `furrow lint` flags any that got in already.\n\n" +
			"The progress object {committed, pulled, pushed, conflict, complete,\n" +
			"committed_bodies, pending_bodies, pending_stash, incoming} goes to stdout\n" +
			"even on\n" +
			"failure; `complete` is false whenever a body or stash is left pending (and\n" +
			"the stdout summary line names that count), so a pushed-but-incomplete sync\n" +
			"never reads as fully published. `incoming` classifies the task changes the\n" +
			"pull brought IN — the other machines' and CI's writes, from the pre-pull vs\n" +
			"post-pull shard diff: created / closed / reopened / moved / refiled /\n" +
			"archived / updated, with the old and new lane or epic on the moves — and\n" +
			"prints as one \"incoming:\" line, so the CI that closed your in-progress\n" +
			"task surfaces in the sync that pulled it, not on a later re-read. After a successful sync it also reports a\n" +
			"revisit summary (repo-scoped counts of tasks with a done dependency or gone\n" +
			"stale, plus any repos whose human review is older than [review].stale_after_days\n" +
			"— a human runs `furrow review <repo>`, an agent sweep uses --by agent) so\n" +
			"freshly-pulled staleness surfaces in the loop;\n" +
			"the JSON gains a \"revisit\" key when non-empty. Two more ride-alongs share\n" +
			"that contract: a lint ERROR count by code (a \"lint\" key / one terse line;\n" +
			"detail via `furrow lint` — lint never changes sync's exit code), and the\n" +
			"`epic activate` records this sync publishes (a \"switches\" key / one\n" +
			"\"epic: activated …\" line each), so a focus switch surfaces in the same\n" +
			"session that made it. It is a thin git wrapper — not a\n" +
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
			warnReadOnly(a)
			prog, syncErr := a.Sync(cmd.Context(), app.SyncOpts{Message: message, Bodies: bodies, AllBodies: allBodies})

			// Compute the revisit + lint summaries only on a fully-successful sync
			// (a fresh, consistent, freshly-pulled board). On failure, skip them.
			var sum app.RevisitSummary
			var lint app.LintErrorSummary
			if syncErr == nil {
				if s, err := a.RevisitSummary(syncScope(a), a.Cfg.RevisitStaleDays); err == nil {
					sum = s
				} else {
					fmt.Fprintf(errOut, "revisit summary skipped: %v\n", err)
				}
				// The lint ride-along: sync is the one loop that always runs, so an
				// ERROR count surfaces here (codes only; the detail is `furrow lint`).
				// It never changes sync's exit code — sync's job is publishing the
				// board, and "lint is red so the board cannot sync" is the worst
				// possible coupling.
				if ls, err := a.LintErrorCounts(); err == nil {
					lint = ls
				}
			}

			if jsonMode() {
				emitObject(syncOutput{prog, summaryPtr(sum), lintPtr(lint)})
			} else {
				fmt.Fprintln(out, prog.SyncSummary())
				// What the pull brought in, before the board-state ride-alongs:
				// revisit/lint describe where the board IS, incoming says what
				// just MOVED it — the order a reader reconstructs events in.
				if line := incomingLine(prog.Incoming); line != "" {
					fmt.Fprintln(out, line)
				}
				if line := revisitLine(sum, revisitScopeLabel(a)); line != "" {
					fmt.Fprintln(out, line)
				}
				if lint.Errors > 0 {
					fmt.Fprintln(out, lintLine(lint))
				}
				// The switch records this sync published (the activate log's exit
				// point) — one line per switch, only when a switch happened.
				for _, sw := range prog.Switches {
					fmt.Fprintln(out, epicSwitchLine(sw))
				}
				if len(prog.PendingBodies) > 0 {
					fmt.Fprintln(errOut, pendingBodiesNote(prog.PendingBodies))
				}
				if len(prog.ForeignFiles) > 0 {
					fmt.Fprintf(errOut, "note: %d foreign file(s) in .furrow/ left uncommitted (%s) — not furrow's to publish; delete them or move them out\n",
						len(prog.ForeignFiles), strings.Join(prog.ForeignFiles, ", "))
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

// incomingKinds is the render order of the incoming line's groups — the
// classifier's own priority order (see app.IncomingChange), so the line reads
// most-consequential first.
var incomingKinds = []string{"created", "closed", "reopened", "moved", "refiled", "archived", "updated"}

// incomingLine renders what the pull brought in as one line, grouped by kind:
// "incoming: 2 created (t-a, t-b), 1 moved (t-c ready→in-progress)". At most
// three ids are named per kind (+N more, same cap as unreviewedLine) so a
// CI-heavy pull stays one legible line; the counts are exact and the full list
// is the JSON `incoming` key. Empty -> "" (quiet when nothing came in).
func incomingLine(changes []app.IncomingChange) string {
	if len(changes) == 0 {
		return ""
	}
	byKind := map[string][]app.IncomingChange{}
	for _, c := range changes {
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}
	var parts []string
	for _, kind := range incomingKinds {
		group := byKind[kind]
		if len(group) == 0 {
			continue
		}
		const maxNamed = 3
		named := group
		if len(named) > maxNamed {
			named = named[:maxNamed]
		}
		ids := make([]string, len(named))
		for i, c := range named {
			ids[i] = c.ID
			if c.From != "" || c.To != "" {
				from, to := c.From, c.To
				if kind == "refiled" { // "" is a real epic value: unfiled
					if from == "" {
						from = "unfiled"
					}
					if to == "" {
						to = "unfiled"
					}
				}
				ids[i] += " " + from + "→" + to
			}
		}
		part := fmt.Sprintf("%d %s (%s", len(group), kind, strings.Join(ids, ", "))
		if len(group) > len(named) {
			part += fmt.Sprintf(", +%d more", len(group)-len(named))
		}
		parts = append(parts, part+")")
	}
	return "incoming: " + strings.Join(parts, ", ")
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

// lintPtr returns nil for a clean board so the lint JSON key is omitted — the
// same "quiet when clean" contract as the revisit key.
func lintPtr(sum app.LintErrorSummary) *app.LintErrorSummary {
	if sum.Errors == 0 {
		return nil
	}
	return &sum
}

// lintLine renders the error counts as one terse line: "lint: 2 error(s)
// (epic-required 2) — furrow lint". Codes are sorted so two runs print alike.
func lintLine(sum app.LintErrorSummary) string {
	codes := make([]string, 0, len(sum.Codes))
	for c := range sum.Codes {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprintf("%s %d", c, sum.Codes[c])
	}
	return fmt.Sprintf("lint: %d error(s) (%s) — furrow lint", sum.Errors, strings.Join(parts, ", "))
}

// epicSwitchLine renders one published activation record: "epic: activated
// e-k3m9 \"title\" 2026-07-29 14:32 — reason" (the reason clause only when one
// was recorded).
func epicSwitchLine(sw app.EpicSwitch) string {
	line := fmt.Sprintf("epic: activated %s %q %s", sw.Epic, sw.Title, sw.At)
	if sw.Reason != "" {
		line += " — " + sw.Reason
	}
	return line
}
