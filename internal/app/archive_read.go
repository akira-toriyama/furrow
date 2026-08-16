package app

import (
	"fmt"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// archiveStore opens the sibling .furrow/archive/ store — the full sharded
// fsstore that Archive writes retired tasks into. It backs the archived-read
// paths (show/ls --archived, the not-found archived hint). A non-file-backed app
// (an in-memory test store) has no archive on disk, which is a usage error:
// asking to read the archive of a store that can't have one.
func (a *App) archiveStore() (*fsstore.Store, error) {
	if a.Dir == "" {
		return nil, core.Validationf("", "no archive: this store is not file-backed")
	}
	return fsstore.New(filepath.Join(a.Dir, "archive"), a.Cfg.Lanes, a.Cfg.IDPrefix, a.Cfg.EpicIDPrefix, a.Cfg.IDWidth), nil
}

// listIndex picks List's source index: the archive store when o.Archived, else
// the hot index. Both are canonicalized the same way so display order matches.
// A missing archive dir loads as an empty index (fsstore.Load), so `ls
// --archived` on a never-archived board is a clean empty result.
func (a *App) listIndex(o QueryOpts) (*core.Index, error) {
	if !o.Archived {
		return a.load()
	}
	arc, err := a.archiveStore()
	if err != nil {
		return nil, err
	}
	idx, err := arc.Load()
	if err != nil {
		return nil, err
	}
	core.Canonicalize(idx, a.Cfg.Lanes)
	return idx, nil
}

// GetBatchArchived is GetBatch against the archive store — the read side of
// `show --archived`. A missing archive dir loads as an empty index, so an id
// that was never archived simply comes back in missing.
func (a *App) GetBatchArchived(ids []string, withBody bool) ([]ShowItem, []string, error) {
	arc, err := a.archiveStore()
	if err != nil {
		return nil, nil, err
	}
	idx, err := arc.Load()
	if err != nil {
		return nil, nil, err
	}
	return getBatchFrom(idx, arc.LoadBody, ids, withBody)
}

// notFoundTask builds the single-id task not-found error, enriched when the id
// is actually ARCHIVED: a retired task is a different situation from a typo,
// and until t-yszb every mutator returned the bare miss while only `show` knew
// to mention the archive. Best-effort probe (ArchivedContains' contract): no
// archive, no enrichment — never a different error.
func (a *App) notFoundTask(id string) *core.Error {
	e := core.NotFound(id)
	if arch := a.ArchivedContains([]string{id}); len(arch) > 0 {
		e.Msg += fmt.Sprintf(" (it is archived — restore it with `furrow unarchive %s`, or read it with `furrow show %s --archived`)", id, id)
		e.Details = map[string]any{"archived": arch}
	}
	return e
}

// batchMissingErr is the batch mutators' all-or-nothing not-found shape
// (details.missing, the show batch contract) with the same archived
// enrichment: the archived subset of the misses rides in details.archived and
// the message counts it. total is the whole (deduped) batch size; verb is the
// past participle the message reports ("moved", "set").
func (a *App) batchMissingErr(missing []string, total int, verb string) *core.Error {
	details := map[string]any{"missing": missing}
	msg := fmt.Sprintf("%d of %d ids not found — nothing was %s", len(missing), total, verb)
	if arch := a.ArchivedContains(missing); len(arch) > 0 {
		details["archived"] = arch
		msg += fmt.Sprintf(" (%d archived — restore with `furrow unarchive`, or read with `furrow show --archived`)", len(arch))
	}
	return &core.Error{Code: core.CodeNotFound, Kind: core.KindNotFound, Msg: msg, Details: details}
}

// ArchivedContains returns the subset of ids present in the archive store, in
// input order — used to enrich a hot-store not-found error with a "retry with
// --archived" hint. It is best-effort: a non-file-backed store or a missing
// archive dir means "nothing archived" (nil), never an error, so the hint path
// never turns a plain miss into a failure of a different kind.
func (a *App) ArchivedContains(ids []string) []string {
	if a.Dir == "" || len(ids) == 0 {
		return nil
	}
	arc, err := a.archiveStore()
	if err != nil {
		return nil
	}
	idx, err := arc.Load()
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if idx.Has(id) {
			out = append(out, id)
		}
	}
	return out
}
