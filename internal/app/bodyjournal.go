package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// The touched-bodies journal makes "furrow itself wrote this body" durable
// across processes, so a plain `furrow sync` can commit the prose that `furrow
// note` / `edit --body` / `done --note` / `apply` wrote WITHOUT ever sweeping a
// co-located operator's hand edits. App.bodiesTouched already knows the ids
// within one process; this file carries that knowledge to the next one — as a
// recorded fact, never an inference from file content or mtime.
//
// The journal lives INSIDE the git directory (`git rev-parse --git-path
// furrow-touched-bodies`), one id per line, sorted. That location is the point:
// it is per-checkout local state, so it never appears in `git status`, never
// syncs to the shared board, and never shows up as a foreign file. Everything
// here is best-effort — a lost or unwritable journal degrades to the OLD
// behavior (the body stays pending and sync discloses it), never to a wrong
// commit. Concurrent furrow processes read-modify-write without a lock for the
// same reason: the worst case is a dropped id, i.e. a pending body.
const touchedBodiesJournal = "furrow-touched-bodies"

// journalPath resolves the journal file inside r's git dir.
func journalPath(ctx context.Context, r *gitrepo.Repo) (string, bool) {
	p, err := r.GitPath(ctx, touchedBodiesJournal)
	if err != nil {
		return "", false
	}
	return p, true
}

// readBodyJournal returns the journaled ids, sorted (empty on any failure).
func readBodyJournal(ctx context.Context, r *gitrepo.Repo) []string {
	p, ok := journalPath(ctx, r)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(p) // #nosec G304 -- a fixed name inside the repo's own git dir
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	sort.Strings(ids)
	return ids
}

// writeBodyJournal replaces the journal with ids (removing the file when
// empty, so a clean checkout carries no furrow droppings in .git).
func writeBodyJournal(ctx context.Context, r *gitrepo.Repo, ids []string) {
	p, ok := journalPath(ctx, r)
	if !ok {
		return
	}
	if len(ids) == 0 {
		_ = os.Remove(p)
		return
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(strings.Join(sorted, "\n")+"\n"), 0o644)
}

// JournalTouchedBodies persists the ids of bodies THIS process wrote (and that
// nothing has committed yet — AutoCommitFlush removes what it commits from
// bodiesTouched first) into the checkout's journal, unioned with what earlier
// processes left there. The CLI calls it after every successful mutating
// command; on a board outside git it is a silent no-op, since there is no sync
// to hand the knowledge to.
func (a *App) JournalTouchedBodies(ctx context.Context) {
	if len(a.bodiesTouched) == 0 {
		return
	}
	r, err := gitrepo.Open(ctx, a.Dir)
	if err != nil {
		return
	}
	set := map[string]bool{}
	for _, id := range readBodyJournal(ctx, r) {
		set[id] = true
	}
	for id := range a.bodiesTouched {
		set[id] = true
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	writeBodyJournal(ctx, r, ids)
}
