package app

import (
	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// `furrow tidy` — the mechanized tidy pass (preview unless --yes), for the two
// prunes a hand-tidy kept performing and no command could: dead bookkeeping
// whose DETECTION is deterministic and whose REMOVAL changes no task's
// meaning. Everything judgment-shaped (retiring anchors, retitling, deciding
// what a parked dep should become) deliberately stays lint/revisit's to REPORT
// and the human's to do — tidy applies only what is safe to apply blind.
//
// Two classes, each behind its own selector because each is a policy decision:
//
//   - done-deps: dep edges from OPEN tasks to done-LANE tasks. Satisfied edges
//     gate nothing (`next` already treats them met) and only accrete — but a
//     board may keep them as HISTORY on purpose (this repo's own board wires
//     epic slices as deps and reads closed edges as a record), so pruning is
//     per-run opt-in (--done-deps), never config, never default. The hot store
//     alone: archive/ holds done tasks only (lint's archived-not-done).
//   - unknown-keys: the extras the passthrough parks (task, epic, and repo
//     shards, plus meta.json) in EVERY store of the board — the hot one and
//     archive/, which upgrade sweeps too. Preserving them is the store's
//     invariant; DROPPING them is legitimately a human's call — upgrade says
//     "kept for a human", lint nags unknown-shard-key — and this is the tool
//     that call was missing: the only alternative was hand-editing a
//     machine-written shard, which the store contract forbids. A sweep that
//     stopped at the hot store left an archived shard's key with that
//     alternative alone (t-c6nn). The preview lists every (record, keys) pair;
//     --unknown-keys is the operator accepting that a key a NEWER binary wrote
//     (the passthrough's other cause) is dropped with the junk.
//
// The apply deliberately does NOT stamp `updated`: both prunes are positional
// bookkeeping in the reorder-respace sense — no content, estimate, lane, or
// promise changed — and a tidy that touched 30 tasks' clocks would reset
// `is:stale`, revisit's stale, and `ls --since` across the board, making the
// staleness signals lie in bulk. The board repo's git history is the record of
// what a tidy removed.
type TidyOpts struct {
	// DoneDeps / UnknownKeys select the classes to report (and, with Apply, to
	// prune). The CLI defaults both ON for a bare preview and requires an
	// explicit selector beside --yes.
	DoneDeps    bool
	UnknownKeys bool
	Apply       bool
}

// TidyDoneDep is one open task whose dep set carries satisfied (done-lane)
// edges — the edges themselves, so the preview is the exact diff.
type TidyDoneDep struct {
	ID   string   `json:"id"`
	Deps []string `json:"deps"`
}

// TidyUnknownKeys is one record carrying parked unknown keys: the entity id (a
// task id, an epic id, an owner/repo, or meta.json's — "meta" for the board's,
// "archive" for its archive store's, lint's naming), the file those keys live
// in relative to the board directory (an `archive/` prefix is the archive
// store), and the keys — the same blame lint's unknown-shard-key assigns.
type TidyUnknownKeys struct {
	ID   string   `json:"id"`
	File string   `json:"file"`
	Keys []string `json:"keys"`
}

// TidyReport is what `furrow tidy` emits — preview and apply share the shape,
// upgrade-style: Changed says whether the selected classes found anything,
// Applied whether --yes made it real.
type TidyReport struct {
	Applied     bool              `json:"applied"`
	Changed     bool              `json:"changed"`
	DoneDeps    []TidyDoneDep     `json:"done_deps,omitempty"`
	UnknownKeys []TidyUnknownKeys `json:"unknown_keys,omitempty"`
}

// Tidy computes (and with o.Apply, performs) the selected prunes. The apply is
// per-shard atomic and idempotent — a re-run after any interruption finds only
// what is still there — and an apply on a clean board writes nothing.
func (a *App) Tidy(o TidyOpts) (*TidyReport, error) {
	idx, err := a.load() // canonical order: the report lists rows as ls does
	if err != nil {
		return nil, err
	}

	rep := &TidyReport{}
	doneIDs := a.doneSet(idx)

	if o.DoneDeps {
		for i := range idx.Tasks {
			t := &idx.Tasks[i]
			if t.Closed != nil || t.Status == a.Cfg.DoneLane {
				continue // a finished task's edges are pure history already
			}
			var dead []string
			for _, dep := range t.Deps {
				if doneIDs[dep] {
					dead = append(dead, dep)
				}
			}
			if len(dead) > 0 {
				rep.DoneDeps = append(rep.DoneDeps, TidyDoneDep{ID: t.ID, Deps: dead})
			}
		}
	}

	var stores []*tidyStore
	if o.UnknownKeys {
		if stores, err = a.tidyStores(idx); err != nil {
			return nil, err
		}
		for _, s := range stores {
			rep.UnknownKeys = append(rep.UnknownKeys, s.rows...)
		}
	}

	rep.Changed = len(rep.DoneDeps) > 0 || len(rep.UnknownKeys) > 0
	if !o.Apply || !rep.Changed {
		return rep, nil
	}

	// A refusal has to be total (raiseMoveRefusals' rule). The hot store is written
	// first, so its own refusals fire before a byte lands; another store's —
	// its write gate, and Save's duplicate-id check when its index is rewritten
	// — are raised HERE, or one would fail the run after the hot prune landed.
	for _, s := range stores {
		if s.store == a.Store || len(s.rows) == 0 {
			continue
		}
		if err := s.store.Writable(); err != nil {
			return nil, err
		}
		if s.reindex {
			if err := core.CheckUniqueIDs(s.idx); err != nil {
				return nil, err
			}
		}
	}

	// Apply. One pass over the in-memory records, then each store's own write
	// paths — Save/SaveEpic/SaveRepo rewrite only the shards whose bytes
	// changed, and NOTHING here touches `updated` (see the type comment).
	prunedDeps := make(map[string]bool, len(rep.DoneDeps))
	for _, d := range rep.DoneDeps {
		prunedDeps[d.ID] = true
	}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if prunedDeps[t.ID] {
			kept := t.Deps[:0]
			for _, dep := range t.Deps {
				if !doneIDs[dep] {
					kept = append(kept, dep)
				}
			}
			t.Deps = kept
		}
	}
	switch {
	case len(stores) == 0: // --done-deps alone
		if err := a.saveIndex(idx); err != nil {
			return nil, err
		}
	case len(rep.DoneDeps) > 0:
		stores[0].reindex = true // the hot store's one index save lands the dep prune
	}
	for _, s := range stores {
		if err := s.pruneUnknownKeys(); err != nil {
			return nil, err
		}
	}
	rep.Applied = true
	return rep, nil
}

// tidyStore is one store of the board as the unknown-key sweep reads it: its
// records, loaded once for both the report and the apply, and the rows found
// in them. The stores differ only in where they sit and how they are written —
// the hot store's index and box writes go through saveIndex/saveEpic (the
// write-lint snapshots every mutation rides), archive/'s straight to the
// store, as archiveMove writes it.
type tidyStore struct {
	store     Store
	saveIndex func(*core.Index) error
	saveEpic  func(*core.Epic) error
	idx       *core.Index
	epics     []core.Epic
	repos     []core.RepoRecord
	rows      []TidyUnknownKeys
	// reindex says the apply must save idx: a task shard carries a parked key,
	// or (hot store only) the dep prune changed one.
	reindex bool
}

// tidyStores returns the hot store over the index Tidy already loaded, then
// archive/ — absent for an app with no directory. A board that never archived
// reads as an empty archive (archiveLoad's contract): no rows, never written.
func (a *App) tidyStores(idx *core.Index) ([]*tidyStore, error) {
	hot, err := loadTidyStore(a.Store, idx, "", "meta", a.saveIndex, a.saveEpic)
	if err != nil {
		return nil, err
	}
	if a.Dir == "" {
		return []*tidyStore{hot}, nil
	}
	arc, arcIdx, err := a.archiveLoad()
	if err != nil {
		return nil, err
	}
	archived, err := loadTidyStore(arc, arcIdx, "archive/", "archive", arc.Save, arc.SaveEpic)
	if err != nil {
		return nil, err
	}
	return []*tidyStore{hot, archived}, nil
}

// loadTidyStore reads one store's boxes, repo records and meta.json beside its
// index, and lists every record carrying parked keys: tasks in the index's
// order, then epics, repos, meta. dir is the store's place under the board
// directory, prefixed to each row's File; metaID is the id meta.json's row
// carries.
func loadTidyStore(s Store, idx *core.Index, dir, metaID string, saveIndex func(*core.Index) error, saveEpic func(*core.Epic) error) (*tidyStore, error) {
	epics, err := s.LoadEpics()
	if err != nil {
		return nil, err
	}
	repos, err := s.ListRepos()
	if err != nil {
		return nil, err
	}
	meta, err := s.LoadMeta()
	if err != nil {
		return nil, err
	}
	ts := &tidyStore{store: s, saveIndex: saveIndex, saveEpic: saveEpic, idx: idx, epics: epics, repos: repos}
	row := func(id, file string, keys []string) bool {
		if len(keys) == 0 {
			return false
		}
		ts.rows = append(ts.rows, TidyUnknownKeys{ID: id, File: dir + file, Keys: keys})
		return true
	}
	for i := range idx.Tasks {
		if row(idx.Tasks[i].ID, core.TaskPath(idx.Tasks[i].ID), idx.Tasks[i].ExtraKeys()) {
			ts.reindex = true
		}
	}
	for i := range epics {
		row(epics[i].ID, core.EpicPath(epics[i].ID), epics[i].ExtraKeys())
	}
	for i := range repos {
		row(repos[i].Repo, core.RepoRecordPath(repos[i].Repo), repos[i].ExtraKeys())
	}
	row(metaID, "meta.json", meta.ExtraKeys())
	return ts, nil
}

// pruneUnknownKeys drops every parked key in the store and writes back only
// what changed: the index when reindex says so (Save then rewrites only the
// shards whose bytes changed), each box and repo record that carried a key,
// and meta.json (PruneMetaExtras writes nothing for a clean one). A store with
// nothing to prune writes nothing — not even a no-op Save, which would meet
// its write gate, stamp a not-yet-existing archive/ into being, and respell
// its misnamed shards.
func (s *tidyStore) pruneUnknownKeys() error {
	if s.reindex {
		for i := range s.idx.Tasks {
			s.idx.Tasks[i].ClearExtras()
		}
		if err := s.saveIndex(s.idx); err != nil {
			return err
		}
	}
	for i := range s.epics {
		if len(s.epics[i].ClearExtras()) == 0 {
			continue
		}
		if err := s.saveEpic(&s.epics[i]); err != nil {
			return err
		}
	}
	for i := range s.repos {
		if len(s.repos[i].ClearExtras()) == 0 {
			continue
		}
		if err := s.store.SaveRepo(&s.repos[i]); err != nil {
			return err
		}
	}
	_, err := s.store.PruneMetaExtras()
	return err
}
