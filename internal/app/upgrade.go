package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
)

// UpgradeStore is one store's part of a flag day. A board is usually two stores
// on disk — the hot one and, once anything has been archived, .furrow/archive/ —
// and BOTH carry a meta.json. Raising only the hot one leaves the archive on the
// old layout, where the next `furrow archive` would meet its own write gate and
// fail on a store nobody remembers exists.
type UpgradeStore struct {
	Path  string `json:"path"`  // absolute store path
	From  int    `json:"from"`  // the layout it declares now (0 = unstamped)
	To    int    `json:"to"`    // the layout it will declare after
	Tasks int    `json:"tasks"` // shards re-serialized through the current marshaller
}

// UpgradeReport is what `furrow upgrade` emits. Changed says whether anything is
// out of date at all; Applied distinguishes the default preview from a real run,
// so a machine can tell "nothing to do" (changed:false) from "I would do this"
// (changed:true, applied:false) without parsing prose.
type UpgradeReport struct {
	From    int            `json:"from"`    // the hot store's current layout
	To      int            `json:"to"`      // core.SchemaVersion
	Changed bool           `json:"changed"` // any store is behind
	Applied bool           `json:"applied"` // --yes was passed and the write happened
	Stores  []UpgradeStore `json:"stores"`
}

// Upgrade raises the board's on-disk layout version to the one this binary
// writes. It is the ONLY thing in furrow that may do that — every ordinary write
// refuses instead (core.CheckWritable), because a routine `furrow sync` from a
// source build once performed this exact migration as a silent side effect and
// locked every pinned release out of the shared tracker.
//
// It is a FLAG DAY, not a fix-up: once it lands, no binary older than this one
// can write the board (and none older than the release that introduced the
// layout can read it). The ordering that keeps that safe — release furrow, bump
// every caller's pin, THEN upgrade — is a human's to run; furrow cannot see the
// fleet's pins, so it previews by default and states the checklist.
//
// A board that is already current is a clean no-op (Changed:false, zero bytes
// written, exit 0), so this is safe to run at any time.
func (a *App) Upgrade(apply bool) (*UpgradeReport, error) {
	if a.Dir == "" {
		return nil, core.Internalf("", "upgrade requires a file-backed store")
	}

	// Every store the board is made of — the hot one and, when it exists, the
	// archive. Both carry a meta.json; raising only the hot one would leave the
	// archive on the old layout, where the next `furrow archive` meets its own
	// write gate and fails on a store nobody remembers exists.
	rep := &UpgradeReport{To: core.SchemaVersion, Stores: []UpgradeStore{}}
	// `from` is the OLDEST store's version, not the hot one's. Taking it from the
	// hot store alone made a board whose only outdated store was the archive report
	// {"from":4,"to":4,"changed":true} — two keys contradicting each other, and a
	// machine branching on from != to reads "nothing to do".
	rep.From = core.SchemaVersion

	for _, t := range a.boardStores() {
		if t.Err != nil {
			return nil, t.Err
		}
		// A board NEWER than this binary is not an upgrade problem — it is a stale
		// binary. Refuse loudly (schema-too-new, exit 3) rather than "downgrading"
		// it: there is no downgrade path, and inventing one would strip the very
		// fields the gate exists to protect. Recovery is `git revert` on the board.
		if err := core.CheckSchemaVersion(t.Version); err != nil {
			return nil, err
		}
		if t.Version < rep.From {
			rep.From = t.Version
		}
		if t.Version == core.SchemaVersion {
			continue
		}
		idx, err := t.Store.Load()
		if err != nil {
			return nil, err
		}
		rep.Changed = true
		rep.Stores = append(rep.Stores, UpgradeStore{
			Path: t.Path, From: t.Version, To: core.SchemaVersion, Tasks: len(idx.Tasks),
		})
		if !apply {
			continue
		}
		// Raise the version FIRST, then re-save: Save's own gate now passes, and
		// every shard is re-serialized through core.MarshalTask, so the bytes on
		// disk become canonical for the new layout in one deliberate commit.
		if err := t.Store.SetBoardVersion(core.SchemaVersion); err != nil {
			return nil, err
		}
		if err := t.Store.Save(idx); err != nil {
			return nil, err
		}
		rep.Applied = true
	}
	return rep, nil
}
