package app

import (
	"path/filepath"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// Archivable returns the ids of done-lane tasks closed strictly before cutoff —
// the pure selection rule behind Archive, split out so it is testable without a
// filesystem. Only tasks with a Closed timestamp qualify (a done task always
// has one; a parked/icebox task does not and is left in the hot index).
func Archivable(idx *core.Index, doneLane string, cutoff time.Time) []string {
	var ids []string
	for _, t := range idx.Tasks {
		if t.Status == doneLane && t.Closed != nil && t.Closed.Before(cutoff) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// Archive moves done tasks older than olderThanDays into .furrow/archive/
// (its own index.json + bodies/), keeping the hot store light. With dryRun it
// only reports what it would move. Returns the affected tasks.
//
// Requires a file-backed store (a.Dir set) — the archive is a sibling .furrow
// directory; an in-memory app cannot archive to disk.
func (a *App) Archive(olderThanDays int, dryRun bool) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	cutoff := a.Clock.Now().AddDate(0, 0, -olderThanDays)
	ids := Archivable(idx, a.Cfg.DoneLane, cutoff)

	var moved []core.Task
	for _, id := range ids {
		if t, _ := idx.Find(id); t != nil {
			moved = append(moved, *t)
		}
	}
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
		arcIdx.Add(t)
		idx.Remove(t.ID)
		if err := a.Store.DeleteBody(t.ID); err != nil {
			return nil, err
		}
	}
	if err := arc.Save(arcIdx); err != nil {
		return nil, err
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	return moved, nil
}
