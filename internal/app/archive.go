package app

import (
	"fmt"
	"path/filepath"
	"strings"
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
			return nil, a.notFoundTask(id)
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
	// Gate the HOT board before touching anything, because this is the one flow
	// that writes two stores and it commits the destination first. Without this,
	// a read-only board still got an archive/ born under it: that sibling store is
	// fresh, so it passed the store's fresh-stamp exemption and was stamped with
	// the BINARY's layout — one ahead of the board that owns it — and the task was
	// copied in before the hot store's own gate refused. A refusal has to be total.
	if err := a.Store.Writable(); err != nil {
		return nil, err
	}
	arc := fsstore.New(filepath.Join(a.Dir, "archive"), a.Cfg.Lanes, a.Cfg.IDPrefix, a.Cfg.EpicIDPrefix, a.Cfg.IDWidth)
	arcIdx, err := arc.Load()
	if err != nil {
		return nil, err
	}
	// Assets attached to each moved task travel with it into archive/ (t-j2e8) —
	// otherwise `furrow attach`ed media (bodies/assets/<id>-*) is orphaned in the
	// hot store, which lint then flags forever.
	assetsByID, err := a.assetsByOwner(moved)
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
		for _, name := range assetsByID[t.ID] { // copy assets before the source is touched
			data, err := a.Store.LoadAsset(name)
			if err != nil {
				return nil, err
			}
			if err := arc.SaveAssetRaw(name, data); err != nil {
				return nil, err
			}
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
	for _, t := range moved { // both indexes are durable now — safe to delete the source
		if err := a.deleteBody(t.ID); err != nil {
			return nil, err
		}
		for _, name := range assetsByID[t.ID] {
			if err := a.Store.DeleteAsset(name); err != nil {
				return nil, err
			}
		}
	}
	return moved, nil
}

// Unarchive moves the named tasks BACK from .furrow/archive/ to the hot board —
// the inverse of ArchiveIDs, making archive a round trip instead of a one-way
// door (t-yszb: recovery used to mean hand-moving furrow-owned shards between
// stores, exactly what the docs forbid). All-or-nothing: every id must be in
// the archive store, or nothing moves — a miss is the batch not-found shape
// (details.missing), except an id already on the hot board, which is its own
// validation error (there is nothing to restore). Duplicates collapse.
//
// A restored task comes back EXACTLY as archived — done lane, Closed stamp,
// every field byte-preserved: restoring answers "put it back on the board",
// and reopening is `furrow move <id> <lane>`'s job (which already clears
// Closed on leaving the done lane). Same commit order as archiveMove, with the
// stores swapped: the DESTINATION (hot) is persisted first — bodies, assets,
// index — and only then is the archive side updated and its copies deleted, so
// an interrupted run leaves at worst a harmless duplicate in archive/, never a
// hot index entry whose body is still trapped in the archive.
func (a *App) Unarchive(ids []string) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	if err := a.Store.Writable(); err != nil {
		return nil, err
	}
	arc, err := a.archiveStore()
	if err != nil {
		return nil, err
	}
	arcIdx, err := arc.Load()
	if err != nil {
		return nil, err
	}
	var moved []core.Task
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, i := arcIdx.Find(id)
		if i < 0 {
			if idx.Has(id) {
				return nil, core.Validationf(id, "%s is not archived — it is already on the hot board", id)
			}
			missing = append(missing, id)
			continue
		}
		moved = append(moved, *t)
	}
	if len(missing) > 0 {
		return nil, &core.Error{
			Code:    core.CodeNotFound,
			Kind:    core.KindNotFound,
			Msg:     fmt.Sprintf("%d of %d ids not found in the archive — nothing was restored", len(missing), len(moved)+len(missing)),
			Details: map[string]any{"missing": missing},
		}
	}
	arcAssets, err := assetsOwnedBy(arc, moved)
	if err != nil {
		return nil, err
	}
	for _, t := range moved {
		body, err := arc.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		if err := a.Store.SaveBody(t.ID, body); err != nil {
			return nil, err
		}
		for _, name := range arcAssets[t.ID] {
			data, err := arc.LoadAsset(name)
			if err != nil {
				return nil, err
			}
			if err := a.Store.SaveAssetRaw(name, data); err != nil {
				return nil, err
			}
		}
		if !idx.Has(t.ID) { // idempotent: a retry won't double-add
			idx.Add(t)
		}
		arcIdx.Remove(t.ID)
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	if err := arc.Save(arcIdx); err != nil {
		return nil, err
	}
	for _, t := range moved { // both indexes are durable now — safe to delete the source
		if err := arc.DeleteBody(t.ID); err != nil {
			return nil, err
		}
		for _, name := range arcAssets[t.ID] {
			if err := arc.DeleteAsset(name); err != nil {
				return nil, err
			}
		}
	}
	return moved, nil
}

// assetsByOwner groups the hot store's assets by the moved task that owns them —
// an asset named "<id>-…" belongs to task id (frozen ids can't be one another's
// prefix, so at most one owner matches). Only moved tasks are included, so
// archive touches no other repo's or task's media.
func (a *App) assetsByOwner(moved []core.Task) (map[string][]string, error) {
	return assetsOwnedBy(a.Store, moved)
}

// assetLister is the sliver of a store the asset grouping reads — letting
// assetsOwnedBy serve both directions of the archive round trip (the hot store
// on the way out, the archive store on the way back).
type assetLister interface {
	ListAssets() ([]core.AssetInfo, error)
}

// assetsOwnedBy is assetsByOwner against any store side.
func assetsOwnedBy(s assetLister, moved []core.Task) (map[string][]string, error) {
	want := make(map[string]bool, len(moved))
	for _, t := range moved {
		want[t.ID] = true
	}
	assets, err := s.ListAssets()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, as := range assets {
		for id := range want {
			if strings.HasPrefix(as.Name, id+"-") {
				out[id] = append(out[id], as.Name)
				break
			}
		}
	}
	return out, nil
}
