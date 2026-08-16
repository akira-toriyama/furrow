package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
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
//     per-run opt-in (--done-deps), never config, never default.
//   - unknown-keys: the extras the passthrough parks (task, epic, and repo
//     shards, plus meta.json). Preserving them is the store's invariant;
//     DROPPING them is legitimately a human's call — upgrade says "kept for a
//     human", lint nags unknown-shard-key — and this is the tool that call was
//     missing: the only alternative was hand-editing a machine-written shard,
//     which the store contract forbids. The preview lists every (record, keys)
//     pair; --unknown-keys is the operator accepting that a key a NEWER binary
//     wrote (the passthrough's other cause) is dropped with the junk.
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
// task id, an epic id, an owner/repo, or "meta"), the file those keys live in,
// and the keys — the same blame lint's unknown-shard-key assigns.
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
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	repos, err := a.Store.ListRepos()
	if err != nil {
		return nil, err
	}
	meta, err := a.Store.LoadMeta()
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

	if o.UnknownKeys {
		for i := range idx.Tasks {
			t := &idx.Tasks[i]
			if keys := t.ExtraKeys(); len(keys) > 0 {
				rep.UnknownKeys = append(rep.UnknownKeys, TidyUnknownKeys{ID: t.ID, File: core.TaskPath(t.ID), Keys: keys})
			}
		}
		for i := range epics {
			if keys := epics[i].ExtraKeys(); len(keys) > 0 {
				rep.UnknownKeys = append(rep.UnknownKeys, TidyUnknownKeys{ID: epics[i].ID, File: core.EpicPath(epics[i].ID), Keys: keys})
			}
		}
		for i := range repos {
			if keys := repos[i].ExtraKeys(); len(keys) > 0 {
				rep.UnknownKeys = append(rep.UnknownKeys, TidyUnknownKeys{ID: repos[i].Repo, File: core.RepoRecordPath(repos[i].Repo), Keys: keys})
			}
		}
		if keys := meta.ExtraKeys(); len(keys) > 0 {
			rep.UnknownKeys = append(rep.UnknownKeys, TidyUnknownKeys{ID: "meta", File: "meta.json", Keys: keys})
		}
	}

	rep.Changed = len(rep.DoneDeps) > 0 || len(rep.UnknownKeys) > 0
	if !o.Apply || !rep.Changed {
		return rep, nil
	}

	// Apply. One pass over the in-memory records, then the store's own write
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
		if o.UnknownKeys {
			t.ClearExtras()
		}
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	if o.UnknownKeys {
		for i := range epics {
			if len(epics[i].ExtraKeys()) == 0 {
				continue
			}
			epics[i].ClearExtras()
			if err := a.Store.SaveEpic(&epics[i]); err != nil {
				return nil, err
			}
		}
		for i := range repos {
			if len(repos[i].ExtraKeys()) == 0 {
				continue
			}
			repos[i].ClearExtras()
			if err := a.Store.SaveRepo(&repos[i]); err != nil {
				return nil, err
			}
		}
		if _, err := a.Store.PruneMetaExtras(); err != nil {
			return nil, err
		}
	}
	rep.Applied = true
	return rep, nil
}
