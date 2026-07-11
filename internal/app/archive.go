package app

import (
	"path/filepath"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// Archivable returns the ids of done-lane tasks closed strictly before cutoff —
// the pure selection rule behind Archive, split out so it is testable without a
// filesystem. Only tasks with a Closed timestamp qualify: Add and Move guarantee
// a done task has one, and the Closed==nil guard below skips any hand-edited
// zombie (which `furrow lint` flags) instead of archiving it undated. A
// parked/icebox task has no Closed and is left in the hot index.
//
// repos scopes the selection to tasks carrying at least one of the given
// (already-resolved) owner/repo identifiers — the age guard and the repo scope
// AND together, and multiple repos are a union (a task in ANY of them counts).
// An empty repos leaves the selection age-only across the whole board.
func Archivable(idx *core.Index, doneLane string, cutoff time.Time, repos ...string) []string {
	var ids []string
	for _, t := range idx.Tasks {
		if t.Status != doneLane || t.Closed == nil || !t.Closed.Before(cutoff) {
			continue
		}
		if len(repos) > 0 && !containsAny(t.Repos, repos) {
			continue
		}
		ids = append(ids, t.ID)
	}
	return ids
}

// containsAny reports whether have and want share at least one element.
func containsAny(have, want []string) bool {
	for _, w := range want {
		if contains(have, w) {
			return true
		}
	}
	return false
}

// Archive moves done tasks older than olderThanDays into .furrow/archive/
// (its own tasks/ shards + meta.json + bodies/, a sibling sharded store),
// keeping the hot store light. With dryRun it only reports what it would move.
// Returns the affected tasks.
//
// Requires a file-backed store (a.Dir set) — the archive is a sibling .furrow
// directory; an in-memory app cannot archive to disk.
//
// repos, when non-empty, scopes the sweep to those (already-resolved)
// owner/repo identifiers — for folding one repo's done on a shared board
// without touching another's. Empty repos keeps the sweep global (the default).
func (a *App) Archive(olderThanDays int, dryRun bool, repos ...string) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	cutoff := a.Clock.Now().AddDate(0, 0, -olderThanDays)
	ids := Archivable(idx, a.Cfg.DoneLane, cutoff, repos...)

	var moved []core.Task
	for _, id := range ids {
		if t, _ := idx.Find(id); t != nil {
			moved = append(moved, *t)
		}
	}
	return a.archiveMove(idx, moved, dryRun)
}

// ArchiveIDs archives exactly the named tasks — retiring specific done tasks by
// id, the targeted counterpart to the age sweep (so folding one finished task no
// longer needs a board-wide `--older-than 0`). Every id must exist AND be in the
// done lane; a non-done id is a validation error naming it (archiving an
// in-progress task would strand live work in archive/). Duplicate ids collapse.
// dryRun reports without moving. Uses the same destination-before-source move as
// Archive.
func (a *App) ArchiveIDs(ids []string, dryRun bool) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	var moved []core.Task
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, i := idx.Find(id)
		if i < 0 {
			return nil, core.NotFound(id)
		}
		if t.Status != a.Cfg.DoneLane {
			return nil, core.Validationf(id, "only done-lane tasks can be archived by id; %s is in %q (move it to %s first)", id, t.Status, a.Cfg.DoneLane)
		}
		moved = append(moved, *t)
	}
	return a.archiveMove(idx, moved, dryRun)
}

// archiveMove commits `moved` (tasks currently in the loaded hot index idx) to
// the sibling .furrow/archive/ store and removes them from the hot store — the
// shared engine behind the age sweep (Archive) and by-id retire (ArchiveIDs).
// With dryRun (or nothing to move) it just returns moved. It commits the
// destination BEFORE destroying the source: copy every body into the archive and
// update both in-memory indexes, persist both, and only after BOTH succeed
// delete the hot bodies. An interrupted run then leaves at worst a harmless
// duplicate body in archive/ (lint-visible) — it never deletes a hot body while
// the hot index still references it.
func (a *App) archiveMove(idx *core.Index, moved []core.Task, dryRun bool) ([]core.Task, error) {
	if dryRun || len(moved) == 0 {
		return moved, nil
	}
	if a.Dir == "" {
		return nil, core.Internalf("", "archive requires a file-backed store")
	}
	arc := fsstore.New(filepath.Join(a.Dir, "archive"), a.Cfg.Lanes, a.Cfg.IDPrefix, a.Cfg.IDWidth)
	arcIdx, err := arc.Load()
	if err != nil {
		return nil, err
	}
	for _, t := range moved {
		body, err := a.Store.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		if err := arc.SaveBody(t.ID, body); err != nil {
			return nil, err
		}
		if !arcIdx.Has(t.ID) { // idempotent: a retry won't double-add
			arcIdx.Add(t)
		}
		idx.Remove(t.ID)
	}
	if err := arc.Save(arcIdx); err != nil {
		return nil, err
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	for _, t := range moved { // both indexes are durable now — safe to delete
		if err := a.Store.DeleteBody(t.ID); err != nil {
			return nil, err
		}
	}
	return moved, nil
}
