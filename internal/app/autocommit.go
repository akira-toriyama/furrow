package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// AutoCommitResult reports what a post-mutation autocommit did, for the CLI to
// surface. Warnings are the best-effort skips/failures bound for stderr; the
// command still exits 0 whatever happened here (see AutoCommitFlush). On a
// silent success only Committed is set; when the board did not opt in, Attempted
// is false and everything else is empty.
type AutoCommitResult struct {
	Attempted       bool     // the board opted into autocommit and the flush ran
	Committed       bool     // a commit was created
	CommittedBodies []string // task ids whose bodies rode in the commit
	PendingBodies   []string // tracked-dirty bodies deliberately left (another operator's WIP)
	Warnings        []string // best-effort skips/failures, one line each, for stderr
}

// AutoCommitFlush git-commits the board's .furrow/ after a SUCCESSFUL mutating
// command, when this board opted into autocommit (the user-config [[board]]
// `autocommit = true` key; a per-machine choice, never the board's committed
// config). It turns the standalone-board convention "touch furrow → always
// commit" into a tool guarantee, so the backup/undo record no longer depends on
// a human or agent remembering to run `furrow sync`.
//
// BEST-EFFORT by contract. The mutation has already hit disk, so a commit
// failure must NEVER turn a successful command into a non-zero exit — that would
// make an agent retry and double-apply. Every failure mode becomes a one-line
// warning bound for stderr while the command still exits 0: a board outside git,
// an ownership-guard skip, a clean tree, a rebase/merge in progress, an
// index.lock race, a `commit.gpgsign` prompt, a conflict-marker body.
//
// Scope reuses partitionSync — the EXACT rule `furrow sync` uses — so
// machine-written shards always commit and a co-located operator's untouched
// tracked-dirty body is never swept in under the wrong author. The one addition
// over a plain sync commit is SyncOpts.Bodies = the ids THIS command wrote
// (App.bodiesTouched): autocommit commits the command's OWN prose (e.g. `furrow
// note`) even though the file is already tracked — which is the whole point on a
// standalone board — while everyone else's WIP stays put.
//
// It NEVER fetches or pushes; multi-machine convergence stays `furrow sync`'s
// job. autocommit is a LOCAL backup/undo guarantee.
func (a *App) AutoCommitFlush(ctx context.Context, cmd string, args []string) *AutoCommitResult {
	res := &AutoCommitResult{}
	if !a.AutoCommit {
		return res
	}
	res.Attempted = true
	warn := func(format string, v ...any) {
		res.Warnings = append(res.Warnings, "autocommit: "+fmt.Sprintf(format, v...))
	}

	r, err := gitrepo.Open(ctx, a.Dir)
	if err != nil {
		warn("board %s is not inside a git repository — nothing committed. Run `git init` in the board's directory to enable autocommit", a.Dir)
		return res
	}
	spec, err := r.RelPath(a.Dir)
	if err != nil {
		warn("could not locate the board under its git repo (%v) — nothing committed", err)
		return res
	}
	// Ownership guard. Only commit when the board sits at the git repo's own top
	// level — the board dir IS the toplevel, or its immediate child like
	// `.furrow` (RelPath has no slash). A deeper path means gitrepo.Open walked
	// UP past the board's own (missing) repo into an ENCLOSING one — a code repo,
	// a dotfiles home — where committing .furrow/ would drop board commits into
	// an unrelated history on whatever branch is checked out. Skip loudly rather
	// than pollute someone else's repo (the standalone recipe's classic slip is
	// forgetting `git init` in the board's directory).
	if strings.Contains(spec, "/") {
		warn("the board's enclosing git repository is %s, which looks like an unrelated repo rather than the board's own "+
			"(the board is nested at %q) — nothing committed. Run `git init` in the board's own directory to enable autocommit",
			r.Toplevel(), spec)
		return res
	}

	changes, err := r.DirtyChanges(ctx, spec)
	if err != nil {
		warn("could not read the board's git status (%v) — nothing committed", err)
		return res
	}
	if len(changes) == 0 {
		return res // a mutation that changed no bytes (e.g. `set` to the same value) — silent
	}

	commitPaths, committedBodies, pendingBodies := partitionSync(spec, changes, SyncOpts{Bodies: a.touchedBodyIDs()})
	res.PendingBodies = pendingBodies
	// Never publish a body still carrying conflict markers — a commit cannot be
	// un-published, and autocommit runs unattended. Unlike sync's guard (exit 2,
	// PRE-write), this is best-effort: warn, drop that one body from the commit,
	// and commit the rest. The mutation already succeeded.
	commitPaths, committedBodies = a.dropMarkedBodies(spec, commitPaths, committedBodies, warn)
	if len(commitPaths) == 0 {
		return res
	}

	if err := r.Commit(ctx, autoCommitMessage(cmd, a.Cfg.IDPrefix, args), commitPaths...); err != nil {
		warn("git commit failed (%v) — the change is on disk but uncommitted; commit it by hand or run `furrow sync`", err)
		return res
	}
	res.Committed = true
	res.CommittedBodies = committedBodies
	return res
}

// touchedBodyIDs is the sorted set of task ids whose bodies THIS process wrote or
// deleted (see saveBody/deleteBody) — passed as SyncOpts.Bodies so partitionSync
// commits the command's own body edits even when the file is already tracked.
func (a *App) touchedBodyIDs() []string {
	if len(a.bodiesTouched) == 0 {
		return nil
	}
	ids := make([]string, 0, len(a.bodiesTouched))
	for id := range a.bodiesTouched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// dropMarkedBodies removes from the commit set any body still carrying git
// conflict markers, warning once per body. A body that no longer loads (an
// archive's hot-side deletion, say) is left in — there is nothing to un-publish
// and the deletion must still be committed.
func (a *App) dropMarkedBodies(spec string, commitPaths, committedBodies []string, warn func(string, ...any)) (paths, bodies []string) {
	marked := map[string]bool{}
	for _, id := range committedBodies {
		body, err := a.Store.LoadBody(id)
		if err != nil {
			continue // gone (e.g. an archive deletion) — nothing to check or un-publish
		}
		if lines := core.ConflictMarkerLines(body); len(lines) > 0 {
			marked[id] = true
			warn("skipping %s: it still carries git conflict markers (line %s) — resolve them, then commit by hand or run `furrow sync`",
				core.BodyPath(id), joinInts(lines))
		}
	}
	if len(marked) == 0 {
		return commitPaths, committedBodies
	}
	markedPath := make(map[string]bool, len(marked))
	for id := range marked {
		markedPath[spec+"/bodies/"+id+".md"] = true
	}
	for _, p := range commitPaths {
		if !markedPath[p] {
			paths = append(paths, p)
		}
	}
	for _, id := range committedBodies {
		if !marked[id] {
			bodies = append(bodies, id)
		}
	}
	return paths, bodies
}

// autoCommitMessage builds autocommit's commit subject: gitmoji + Conventional
// `chore(board)` (chore = no version bump, right for board data), naming the
// command and any task ids it targeted so `git log` reads as a furrow audit
// trail. It is DISTINCT from DefaultSyncMessage ("sync via furrow") so a manual
// sync and an autocommit are tellable apart in history. Only id-shaped args are
// echoed (never a note's or title's free text), capped so a bulk `done t-a t-b
// …` never bloats the subject line.
func autoCommitMessage(cmd, idPrefix string, args []string) string {
	subject := "furrow " + cmd
	var ids []string
	for _, arg := range args {
		if idPrefix != "" && strings.HasPrefix(arg, idPrefix) {
			ids = append(ids, arg)
		}
	}
	const maxIDs = 5
	if n := len(ids); n > 0 {
		if n > maxIDs {
			subject += " " + strings.Join(ids[:maxIDs], " ") + fmt.Sprintf(" (+%d more)", n-maxIDs)
		} else {
			subject += " " + strings.Join(ids, " ")
		}
	}
	return ":card_file_box: chore(board): " + subject
}
