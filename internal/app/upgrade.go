package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// UpgradeStore is one store's part of a flag day. A board is usually two stores
// on disk — the hot one and, once anything has been archived, .furrow/archive/ —
// and BOTH carry a meta.json. Raising only the hot one leaves the archive on the
// old layout, where the next `furrow archive` would meet its own write gate and
// fail on a store nobody remembers exists.
//
// The v6 fields report the one SEMANTIC conversion an upgrade performs (v5 ->
// v6: `type:"epic"` tasks become epic entities, `parent` edges become `epic`
// membership, bodies follow their epic). They are all zero on any other hop,
// and omitted from the JSON then.
type UpgradeStore struct {
	Path  string `json:"path"`  // absolute store path
	From  int    `json:"from"`  // the layout it declares now (0 = unstamped)
	To    int    `json:"to"`    // the layout it will declare after
	Tasks int    `json:"tasks"` // task shards after the migration (what Save re-serializes)

	// Epics lists each `type:"epic"` task and the epic it becomes — the same
	// rows in preview and apply, because the new id is DERIVED from the old
	// ([ids].epic_prefix + the task id's suffix), never minted at random.
	Epics []core.V6Conversion `json:"epics,omitempty"`
	// Rehomed counts tasks whose retired `parent` edge became `epic` membership.
	Rehomed int `json:"rehomed,omitempty"`
	// BodiesRenamed counts bodies/t-….md files that follow their task into
	// bodies/e-….md.
	BodiesRenamed int `json:"bodies_renamed,omitempty"`
	// LinksRewritten counts live [[t-…]] mentions of converted epics rewritten
	// to [[e-…]] across every body (documentation inside code stays verbatim).
	LinksRewritten int `json:"links_rewritten,omitempty"`
	// Kept lists the retired keys the migration deliberately left parked for a
	// human (lint's unknown-shard-key keeps naming them until someone decides).
	Kept []core.V6Kept `json:"kept,omitempty"`
	// DroppedDeps lists the dep edges removed because their target became an
	// epic (an unresolvable dep would wedge the task out of `furrow next`
	// forever); each drop is recorded as a note in the task's body.
	DroppedDeps []core.V6DroppedDep `json:"dropped_deps,omitempty"`
}

// UpgradeReport is what `furrow upgrade` emits. Changed says whether anything is
// out of date at all; Applied distinguishes the default preview from a real run,
// so a machine can tell "nothing to do" (changed:false) from "I would do this"
// (changed:true, applied:false) without parsing prose.
type UpgradeReport struct {
	From    int            `json:"from"`    // the oldest store's current layout
	To      int            `json:"to"`      // core.SchemaVersion
	Changed bool           `json:"changed"` // any store is behind
	Applied bool           `json:"applied"` // --yes was passed and the write happened
	Stores  []UpgradeStore `json:"stores"`
}

// pendingUpgrade is one outdated store, loaded and planned but not yet written —
// the unit pass 1 hands to pass 2.
type pendingUpgrade struct {
	sv    storeVersion
	idx   *core.Index
	plans []core.V6Epic
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
// Crossing v6 is not just a re-stamp: the retired v5 `type`/`parent` keys —
// parked in extras, since this binary has no such fields — are converted
// (core/migrate_v6.go) rather than stranded as unknown keys forever. The run is
// TWO passes because the conversion map must be global before any store is
// rewritten: a hot task's parent can point at an epic that was archived.
//
// A board that is already current is a clean no-op (Changed:false, zero bytes
// written, exit 0), so this is safe to run at any time.
func (a *App) Upgrade(apply bool) (*UpgradeReport, error) {
	if a.Dir == "" {
		return nil, core.Internalf("", "upgrade requires a file-backed store")
	}

	rep := &UpgradeReport{To: core.SchemaVersion, Stores: []UpgradeStore{}}
	// `from` is the OLDEST store's version, not the hot one's. Taking it from the
	// hot store alone made a board whose only outdated store was the archive report
	// {"from":4,"to":4,"changed":true} — two keys contradicting each other, and a
	// machine branching on from != to reads "nothing to do".
	rep.From = core.SchemaVersion

	// Pass 1: load every outdated store and plan its epic conversions.
	var pend []pendingUpgrade
	conv := map[string]string{}
	derived := map[string]string{} // epic id -> the task it was derived from
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
		plans := core.PlanV6Epics(idx, a.migrationEpicID, a.taskArrivesClosed)
		for _, p := range plans {
			// The derived id must be genuinely fresh. A clash can only come from a
			// hand-made shape (an id missing the task prefix, or a hand-created
			// epics/ shard on a pre-v6 board) — refuse rather than guess, so the
			// human resolves it with the evidence intact. conv is keyed by TASK id,
			// so the clash test needs the reverse map.
			if other, dup := derived[p.Epic.ID]; dup {
				return nil, core.Internalf(p.TaskID,
					"migration derives epic id %s for both %s and %s — resolve by hand and re-run",
					p.Epic.ID, other, p.TaskID)
			}
			if _, exists, err := t.Store.LoadEpic(p.Epic.ID); err != nil {
				return nil, err
			} else if exists {
				return nil, core.Internalf(p.TaskID,
					"cannot convert %s: epic %s already exists in %s", p.TaskID, p.Epic.ID, t.Path)
			}
			conv[p.TaskID] = p.Epic.ID
			derived[p.Epic.ID] = p.TaskID
		}
		pend = append(pend, pendingUpgrade{sv: t, idx: idx, plans: plans})
	}

	// Pass 2: rewrite each store with the whole board's conversion map.
	for _, p := range pend {
		rep.Changed = true
		st := UpgradeStore{Path: p.sv.Path, From: p.sv.Version, To: core.SchemaVersion}
		for _, pl := range p.plans {
			st.Epics = append(st.Epics, core.V6Conversion{
				TaskID: pl.TaskID, EpicID: pl.Epic.ID, Title: pl.Epic.Title, Closed: !pl.Epic.IsOpen(),
			})
			if p.sv.Store.BodyExists(pl.TaskID) {
				st.BodiesRenamed++
			}
		}
		ch := core.ApplyV6(p.idx, conv)
		st.Rehomed, st.Kept, st.DroppedDeps = ch.Rehomed, ch.Kept, ch.DroppedDeps
		st.Tasks = len(p.idx.Tasks)

		if !apply {
			n, err := a.countLinkRewrites(p.sv.Store, conv)
			if err != nil {
				return nil, err
			}
			st.LinksRewritten = n
			rep.Stores = append(rep.Stores, st)
			continue
		}

		// Write order is chosen so an interruption never leaves data existing
		// NOWHERE: raise the version (unblocking this binary's writes), create the
		// epics, and only then Save — which deletes the converted tasks' shards.
		// The board repo is git, so recovery from any half-state is `git checkout`
		// / `git revert` on the board, exactly as the flag-day docs say.
		if err := p.sv.Store.SetBoardVersion(core.SchemaVersion); err != nil {
			return nil, err
		}
		for _, pl := range p.plans {
			e := pl.Epic
			if err := p.sv.Store.SaveEpic(&e); err != nil {
				return nil, err
			}
		}
		if err := p.sv.Store.Save(p.idx); err != nil {
			return nil, err
		}
		n, err := a.migrateBodiesApply(p.sv.Store, p.plans, conv, ch.DroppedDeps)
		if err != nil {
			return nil, err
		}
		st.LinksRewritten = n
		rep.Stores = append(rep.Stores, st)
		rep.Applied = true
	}
	return rep, nil
}

// migrationEpicID derives the epic id a converted task keeps: the epic prefix
// plus the task id's suffix (t-8qqz -> e-8qqz). Derived, not minted, for three
// reasons that compound: the preview names the exact ids the apply will write,
// a re-run derives the same ids (idempotence), and every [[t-…]] link is
// rewritable by pure map lookup.
func (a *App) migrationEpicID(taskID string) string {
	return a.Cfg.EpicIDPrefix + strings.TrimPrefix(taskID, a.Cfg.IDPrefix)
}

// taskArrivesClosed decides whether a v5 epic-task becomes a CLOSED epic: it
// carries a closed stamp, or sits in a terminal lane (icebox never stamps one).
func (a *App) taskArrivesClosed(t *core.Task) bool {
	return t.Closed != nil || a.Cfg.IsTerminal(t.Status)
}

// countLinkRewrites counts the live [[t-…]] links the migration would rewrite
// across every body in the store — the preview's read-only twin of the sweep in
// migrateBodiesApply (the apply reports the count the sweep actually performed).
func (a *App) countLinkRewrites(s Store, conv map[string]string) (int, error) {
	ids, err := s.ListBodyIDs()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		body, err := s.LoadBody(id)
		if err != nil {
			return 0, err
		}
		_, n := core.RewriteV6Links(body, conv, a.Cfg.IDPrefix)
		total += n
	}
	return total, nil
}

// migrateBodiesApply renames each converted task's body to its epic id (seeding
// the same "# title" EpicAdd would when the task never had one — an epic
// without a body file would fail lint's 1:1 check), then rewrites live [[t-…]]
// links in every body, then appends a note for each dropped dep. Rename first,
// sweep second, so the sweep covers the renamed bodies too; notes last, because
// they already name the NEW ids and must not be swept.
func (a *App) migrateBodiesApply(s Store, plans []core.V6Epic, conv map[string]string, drops []core.V6DroppedDep) (int, error) {
	for _, pl := range plans {
		if !s.BodyExists(pl.TaskID) {
			if err := s.SaveBody(pl.Epic.ID, "# "+pl.Epic.Title+"\n"); err != nil {
				return 0, err
			}
			continue
		}
		body, err := s.LoadBody(pl.TaskID)
		if err != nil {
			return 0, err
		}
		if err := s.SaveBody(pl.Epic.ID, body); err != nil {
			return 0, err
		}
		if err := s.DeleteBody(pl.TaskID); err != nil {
			return 0, err
		}
	}
	ids, err := s.ListBodyIDs()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		body, err := s.LoadBody(id)
		if err != nil {
			return 0, err
		}
		rewritten, n := core.RewriteV6Links(body, conv, a.Cfg.IDPrefix)
		if n == 0 {
			continue
		}
		if err := s.SaveBody(id, rewritten); err != nil {
			return 0, err
		}
		total += n
	}
	// The dropped-dep record goes in the task's BODY — the progress record of
	// authority — not just the one-shot upgrade report: "this task used to wait
	// on that box" must survive into the next session.
	for _, d := range drops {
		body, err := s.LoadBody(d.TaskID)
		if err != nil {
			return 0, err
		}
		note := "(v6 migration) dropped dep on " + d.DepID + " — it became epic [[" + d.EpicID +
			"]], and an epic cannot gate a task.\n"
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if body != "" {
			body += "\n"
		}
		if err := s.SaveBody(d.TaskID, body+note); err != nil {
			return 0, err
		}
	}
	return total, nil
}
