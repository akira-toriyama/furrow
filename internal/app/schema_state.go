package app

import (
	"os"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/core"
)

// A board is not one store: once anything has been archived it is TWO — the hot
// `.furrow/` and the sibling `.furrow/archive/` — and BOTH carry a meta.json,
// so both have a layout version and both are gated on write.
//
// Everything that REPORTS the board's schema state has to say so about the whole
// board, not just the hot half. `furrow board`'s `writable` is the key the
// sync-task-status.yml pre-flight branches on, so a board that reports writable
// while its archive store is behind sends CI on its way toward a write that will
// be refused. This file is the one place that folds the stores together; board.go,
// lint.go and upgrade.go all read it, so the answer cannot diverge between them.

// storeVersion is one store of a board and the layout version it declares.
type storeVersion struct {
	Path    string
	Store   Store
	Version int
	Err     error // meta.json unreadable — reported, never guessed
}

// boardStores returns every store this board is made of, hot first, each with the
// version it declares. The archive store is included only when it exists on disk
// (a board that has never archived is one store). A non-file-backed store (the
// in-memory one used by tests) is just itself.
func (a *App) boardStores() []storeVersion {
	out := make([]storeVersion, 0, 2)
	v, err := a.Store.BoardVersion()
	out = append(out, storeVersion{Path: a.Dir, Store: a.Store, Version: v, Err: err})

	if a.Dir == "" {
		return out
	}
	arcDir := filepath.Join(a.Dir, "archive")
	if fi, err := os.Stat(arcDir); err != nil || !fi.IsDir() {
		return out
	}
	arc, err := a.archiveStore()
	if err != nil {
		return out
	}
	av, aerr := arc.BoardVersion()
	return append(out, storeVersion{Path: arcDir, Store: arc, Version: av, Err: aerr})
}

// schemaState folds the board's stores into the one answer `furrow board` prints
// and `furrow lint` warns on: the WORST state across them, plus the version to
// report (the oldest store's — that is the one holding the board back).
//
// It never fails. A board whose meta.json cannot be read is REPORTED as
// unreadable, not raised — `board` is the last command that still works when the
// board and the binary disagree, and CI reads it precisely then.
func (a *App) schemaState() (version int, state string, writable bool) {
	stores := a.boardStores()

	version, state, writable = core.SchemaVersion, SchemaCurrent, true
	for _, s := range stores {
		switch {
		case s.Err != nil:
			// Unreadable dominates everything: we cannot even say what it is.
			return 0, SchemaUnreadable, false
		case s.Version > core.SchemaVersion:
			// Too-new dominates outdated: the binary, not the board, is the problem.
			return s.Version, SchemaTooNew, false
		case s.Store.Writable() != nil:
			// Behind (or unstamped with shards). Report the OLDEST such version, so
			// `upgrade`'s from/to and `board`'s schema_version name the same number.
			if !writable || s.Version < version {
				version = s.Version
			}
			state, writable = SchemaOutdated, false
		case writable && s.Version < version:
			// All stores current so far; keep the lowest declared version (they agree).
			version = s.Version
		}
	}
	return version, state, writable
}
