package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// archivable returns the ids of done-lane tasks closed strictly before cutoff —
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
func archivable(idx *core.Index, doneLane string, cutoff time.Time, repos ...string) []string {
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
		if slices.Contains(have, w) {
			return true
		}
	}
	return false
}

// Archive moves done tasks older than olderThanDays into .furrow/archive/
// (its own tasks/ shards + meta.json + bodies/, a sibling sharded store),
// keeping the hot store light. With dryRun it only reports what it would move.
// Returns the affected tasks. A selected id that archive/ already holds is
// archiveMove's to judge: the same task converges, any other refuses the sweep.
//
// Requires a file-backed store (a.Dir set) — the archive is a sibling .furrow
// directory; an in-memory app cannot archive to disk.
//
// repos, when non-empty, scopes the sweep to those (already-resolved)
// owner/repo identifiers — for folding one repo's done on a board that backs
// SEVERAL repos without touching another's. (Repo count, not mode: a standalone
// board may back several repos too, and a shared board usually backs one.)
// Empty repos keeps the sweep global (the default).
func (a *App) Archive(olderThanDays int, dryRun bool, repos ...string) (*ArchiveReport, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	cutoff := a.Clock.Now().AddDate(0, 0, -olderThanDays)
	ids := archivable(idx, a.Cfg.DoneLane, cutoff, repos...)

	// To a fixed point: a held task stays, so whatever IT depends on is held too.
	leaving := make(map[string]bool, len(ids))
	for _, id := range ids {
		leaving[id] = true
	}
	held := map[string][]string{}
	for {
		holds := depHolds(idx, leaving)
		if len(holds) == 0 {
			break
		}
		for id, by := range holds {
			held[id] = by
			delete(leaving, id)
		}
	}

	var moved []core.Task
	var kept []HeldTask
	for _, id := range ids {
		t, _ := idx.Find(id)
		if t == nil {
			continue
		}
		if by, ok := held[id]; ok {
			kept = append(kept, HeldTask{ID: t.ID, Title: t.Title, HeldBy: by})
			continue
		}
		moved = append(moved, *t)
	}
	rep, err := a.archiveMove(idx, moved, dryRun)
	if err != nil {
		return nil, err
	}
	if kept != nil {
		rep.Held = kept
	}
	return rep, nil
}

// depHolds maps each leaving id to the tasks STAYING in the hot store that name
// it in deps, in index order. Archiving such a task strands the edge: the hot
// index no longer holds the id, so the dependent reads as blocked (out of
// `next`) and lint raises dep-missing — and ready-blocked on an actionable one
// (t-tf56). Any staying dependent holds, whatever its lane: dep-missing is
// lane-blind. A dependent that leaves in the same move holds nothing — the
// edge lands whole in archive/.
func depHolds(idx *core.Index, leaving map[string]bool) map[string][]string {
	holds := map[string][]string{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if leaving[t.ID] {
			continue
		}
		for _, dep := range t.Deps {
			if leaving[dep] && !slices.Contains(holds[dep], t.ID) {
				holds[dep] = append(holds[dep], t.ID)
			}
		}
	}
	return holds
}

// ArchiveIDs archives exactly the named tasks — retiring specific done tasks by
// id, the targeted counterpart to the age sweep (so folding one finished task no
// longer needs a board-wide `--older-than 0`). Every id must exist AND be in the
// done lane; a non-done id is a validation error naming it (archiving an
// in-progress task would strand live work in archive/). A named task that a
// task staying in the hot store depends on is refused (kind `referenced`, the
// edges in details.references.deps) — the sweep holds such a task back, but a
// named id that silently stayed would be a write that did not do what it was
// asked. There is no force: the result would be a board in lint error.
// An id archive/ already holds converges when it is the same task and refuses
// the call when it is another (archiveMove). Duplicate ids collapse.
// dryRun reports without moving. Uses the same destination-before-source move as
// Archive.
func (a *App) ArchiveIDs(ids []string, dryRun bool) (*ArchiveReport, error) {
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
	if holds := depHolds(idx, seen); len(holds) > 0 {
		refs := newReferences()
		for _, t := range moved {
			for _, by := range holds[t.ID] {
				refs.Deps = append(refs.Deps, DepEdge{From: by, To: t.ID})
			}
		}
		return nil, &core.Error{
			Code:    core.CodeValidation,
			Kind:    core.KindReferenced,
			Subject: refs.Deps[0].To,
			Msg: fmt.Sprintf("still depended on — %s; archiving would strand the edge (lint dep-missing, the dependent out of next), so nothing was archived: "+
				"archive the dependents in the same call once they are done, or drop the edge (`furrow dep <dependent> <id> --rm`)", refs.summary()),
			Details: map[string]any{"references": refs},
		}
	}
	return a.archiveMove(idx, moved, dryRun)
}

// archivedDepsOf lists the dep edges from the tasks being restored to tasks
// that stay in the archive (neither on the hot board nor restored in the same
// call), in index order.
func archivedDepsOf(moved []core.Task, arcIdx, hot *core.Index, leaving map[string]bool) References {
	refs := newReferences()
	for _, t := range moved {
		for _, dep := range t.Deps {
			if leaving[dep] || hot.Has(dep) || !arcIdx.Has(dep) {
				continue
			}
			refs.Deps = append(refs.Deps, DepEdge{From: t.ID, To: dep})
		}
	}
	return refs
}

// archivedDeps returns the distinct dep targets of refs.Deps, in order.
func (r References) archivedDeps() []string {
	var out []string
	for _, e := range r.Deps {
		if !slices.Contains(out, e.To) {
			out = append(out, e.To)
		}
	}
	return out
}

// ArchiveReport is what archive hands back: the tasks it moved (or would) and
// the asset half — every in-play asset copied into archive/, what the hot store
// then let go, and what it KEPT because a remaining body or owner still holds
// it (an asset held on both sides is on disk twice, one git blob). Held is the
// sweep's other stated non-move: the selected tasks it left in the hot store
// because a task staying there depends on them (always empty on a by-id
// retire, which refuses instead).
type ArchiveReport struct {
	Tasks  []core.Task   `json:"tasks"`
	Assets AssetTransfer `json:"assets"`
	Held   []HeldTask    `json:"held"`
}

// HeldTask is one done task the age sweep selected but left in the hot store.
type HeldTask struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	HeldBy []string `json:"held_by"` // the staying tasks whose deps name it
}

// UnarchiveReport is ArchiveReport's inverse: the tasks restored and the asset
// half read from the archive store's side (what came back, what archive/ let
// go, what it kept for an archived body still pointing at it).
type UnarchiveReport struct {
	Tasks  []core.Task
	Assets AssetTransfer
}

// archiveMove commits `moved` (tasks currently in the loaded hot index idx) to
// the sibling .furrow/archive/ store and removes them from the hot store — the
// shared engine behind the age sweep (Archive) and by-id retire (ArchiveIDs).
// With dryRun (or nothing to move) it just reports — the asset outcome
// included, read from the hot store as it stands. It commits the destination
// BEFORE destroying the source: copy every body and in-play asset into the
// archive and update both in-memory indexes, persist both, and only after BOTH
// succeed delete the hot bodies and reap the hot assets nothing remaining
// holds (asset_hold.go). It never deletes a hot file while the hot index or a
// hot body still references it. An interrupted run leaves one of three
// states: archive/ copies of bodies and assets that no archive/ shard names
// (stopped before archive/'s index was saved — no lint sees them, and the
// re-run overwrites them); the task in BOTH indexes (stopped between the two
// saves — lint's duplicate-id, finished by the re-run); or hot bodies and
// assets the hot index no longer names (stopped after the hot save — lint's
// orphan-body warns on the bodies).
//
// An id archive/ already holds is that middle state only when it is the same
// task (core.SameTask). The re-run then writes the hot copy over archive/'s,
// shard as well as body: the hot copy is the one an edit made after the
// interruption reached, and keeping archive/'s shard beside the hot body used
// to mix the two. The body goes with it only while it still holds archive/'s
// prose (pickFinishedBody): a blank one — a shard brought back by hand, or
// kept when resolving a sync conflict, then perhaps given edit's empty
// placeholder — leaves archive/ its own, and one that lacks archive/'s lines
// refuses the call (refuseBodyLoss), as does an asset the two sides hold
// under one name with other bytes (refuseAssetClash). Under any other task
// the id is refused before the first write (refuseIDClash).
func (a *App) archiveMove(idx *core.Index, moved []core.Task, dryRun bool) (*ArchiveReport, error) {
	rep := &ArchiveReport{Tasks: moved, Assets: newAssetTransfer(), Held: []HeldTask{}}
	if rep.Tasks == nil {
		rep.Tasks = []core.Task{}
	}
	if len(moved) == 0 {
		return rep, nil
	}
	leaving := idSet(moved)
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	plan, err := planAssets(a.Store, leaving, remainingIDs(idx, epics, leaving))
	if err != nil {
		return nil, err
	}
	if dryRun {
		rep.Assets.Copied = plan.inPlay
		rep.Assets.Deleted, rep.Assets.Kept = plan.outcome()
		return rep, nil
	}
	if a.Dir == "" {
		return nil, core.Internalf("", "archive requires a file-backed store")
	}
	arc, arcIdx, err := a.archiveLoad()
	if err != nil {
		return nil, err
	}
	if err := raiseMoveRefusals(a.Store, idx, arc, arcIdx); err != nil {
		return nil, err
	}
	if err := refuseIDClash(moved, arcIdx, "in archive/", "archived"); err != nil {
		return nil, err
	}
	keepArchived := map[string]bool{} // finished moves whose blank hot body leaves archive/ its own
	var lossy []string
	for _, t := range moved {
		if !arcIdx.Has(t.ID) {
			continue
		}
		board, err := a.Store.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		archived, err := arc.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		useBoard, ok := pickFinishedBody(board, archived)
		switch {
		case !ok:
			lossy = append(lossy, t.ID)
		case !useBoard:
			keepArchived[t.ID] = true
		}
	}
	if len(lossy) > 0 {
		return nil, refuseBodyLoss(lossy, "archived")
	}
	if err := refuseAssetClash(a.Store, arc, plan, "archived"); err != nil {
		return nil, err
	}
	for _, t := range moved {
		if !keepArchived[t.ID] {
			body, err := a.Store.LoadBody(t.ID)
			if err != nil {
				return nil, err
			}
			if err := arc.SaveBody(t.ID, body); err != nil {
				return nil, err
			}
		}
		if held, _ := arcIdx.Find(t.ID); held != nil {
			*held = t // the same task (refuseIDClash): the hot copy finishes the interrupted move
		} else {
			arcIdx.Add(t)
		}
		idx.Remove(t.ID)
	}
	copied, err := copyAssets(a.Store, arc, plan) // before the source is touched
	if err != nil {
		return nil, err
	}
	if err := arc.Save(arcIdx); err != nil {
		return nil, err
	}
	if err := a.saveIndex(idx); err != nil {
		return nil, err
	}
	for _, t := range moved { // both indexes are durable now — safe to delete the source
		if err := a.deleteBody(t.ID); err != nil {
			return nil, err
		}
	}
	deleted, kept, err := reapAssets(a.Store, plan)
	if err != nil {
		return nil, err
	}
	rep.Assets = AssetTransfer{Copied: copied, Deleted: deleted, Kept: kept}
	return rep, nil
}

// raiseMoveRefusals raises, before a move between the hot store and archive/
// writes anything, every refusal its two Saves would raise: each store's
// write gate and each index's duplicate-id check. Both directions commit the
// destination first, so a refusal Save raised itself came after bodies,
// assets, or a whole index had landed, leaving the task in both stores behind
// an error that said nothing was written: a read-only board still got an
// archive/ born under it (the fresh sibling passes the store's fresh-stamp
// exemption, stamped with the BINARY's layout), and an archive/ on an older
// layout refused only after the restored task was on the hot board (t-paax).
// A refusal has to be total.
//
// The indexes are checked as loaded, not as they will be saved: a duplicate
// whose first copy is the one leaving would pass Save, and the move would
// then delete the body the remaining copy still names. The one refusal no
// Save raises — an id each store holds once, as two different tasks — needs
// the moving set, so it is refuseIDClash's, raised right after.
func raiseMoveRefusals(hot core.Store, hotIdx *core.Index, arc core.Store, arcIdx *core.Index) error {
	if err := hot.Writable(); err != nil {
		return err
	}
	if err := arc.Writable(); err != nil {
		return err
	}
	if err := core.CheckUniqueIDs(hotIdx); err != nil {
		return err
	}
	return core.CheckUniqueIDs(arcIdx)
}

// refuseIDClash refuses, before a move writes anything, an id the move brings
// into dest that dest already holds as a DIFFERENT task (core.SameTask). Each
// index alone is unique, so no Save objects, and the move went on to keep one
// task's shard beside the other's body and delete the source: archiving the
// projects board's t-ksee would have lost the newer task's record and the
// older task's body at exit 0 (t-erd6). Only a hand repair can say which task
// keeps the id. The same task is an interrupted move, which the caller
// finishes instead.
func refuseIDClash(moving []core.Task, dest *core.Index, where, verb string) error {
	held := make(map[string]*core.Task, len(dest.Tasks))
	for i := range dest.Tasks {
		held[dest.Tasks[i].ID] = &dest.Tasks[i]
	}
	var clash []string
	for i := range moving {
		if d := held[moving[i].ID]; d != nil && !core.SameTask(d, &moving[i]) {
			clash = append(clash, moving[i].ID)
		}
	}
	if len(clash) == 0 {
		return nil
	}
	slices.Sort(clash)
	ids, names := "id "+clash[0], "names"
	if len(clash) > 1 {
		ids, names = "ids "+strings.Join(clash, ", "), "name"
	}
	return core.Validationf(clash[0],
		"task %s already %s a different task %s — the move would write one over the other, so nothing was %s; repair it by hand (give one of them its own id) — `furrow lint` flags it as duplicate-id",
		ids, names, where, verb)
}

// pickFinishedBody chooses whose body a finished move keeps for one task the
// board and archive/ both hold (core.SameTask): board is the board's body (""
// when the file is missing), archived archive/'s. The board's copy is the one
// an edit after the interruption reached, so it wins while it still holds
// archive/'s prose (holdsProse). A blank one yields archive/'s: a shard can
// come back without its body, and edit's empty placeholder is no newer copy.
// ok=false when the board's body lacks archive/'s lines — a note, an attach
// link or retitle's seeded heading written onto a body-less copy — since
// keeping either body would drop lines only a hand merge can place.
func pickFinishedBody(board, archived string) (useBoard, ok bool) {
	if strings.TrimSpace(board) == "" {
		return false, true
	}
	return true, holdsProse(board, archived)
}

// holdsProse reports whether every non-blank line of archived below its title
// heading appears in board in the same order, trailing whitespace aside, so
// board is archived plus edits: a note or an attach link adds lines, and
// retitle rewrites only the heading. The heading is retitleHeading's — the
// first real line when it is an H1, blank lines and an occurrence's back-link
// allowed above it.
func holdsProse(board, archived string) bool {
	have := strings.Split(board, "\n")
	want := strings.Split(archived, "\n")
	start := 0
	for i, ln := range want {
		if strings.TrimSpace(ln) == "" || bodyLinkLine.MatchString(ln) {
			continue
		}
		if strings.HasPrefix(ln, "# ") {
			start = i + 1
		}
		break
	}
	j := 0
	for _, ln := range want[start:] {
		ln = strings.TrimRight(ln, " \t\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		for j < len(have) && strings.TrimRight(have[j], " \t\r") != ln {
			j++
		}
		if j == len(have) {
			return false
		}
		j++
	}
	return true
}

// refuseBodyLoss is the refusal pickFinishedBody's ok=false raises, before
// anything is written.
func refuseBodyLoss(ids []string, verb string) error {
	slices.Sort(ids)
	which := "body of " + ids[0] + " lacks lines archive/'s copy has"
	if len(ids) > 1 {
		which = "bodies of " + strings.Join(ids, ", ") + " lack lines archive/'s copies have"
	}
	return core.Validationf(ids[0],
		"the board's %s below the title — finishing the move would drop them, so nothing was %s; copy them from archive/bodies/<id>.md into the board's (`furrow edit <id>`), or delete them there if they were dropped on purpose, then re-run",
		which, verb)
}

func idSet(tasks []core.Task) map[string]bool {
	set := make(map[string]bool, len(tasks))
	for i := range tasks {
		set[tasks[i].ID] = true
	}
	return set
}

// Unarchive moves the named tasks BACK from .furrow/archive/ to the hot board —
// the inverse of ArchiveIDs, making archive a round trip instead of a one-way
// door (t-yszb: recovery used to mean hand-moving furrow-owned shards between
// stores, exactly what the docs forbid). All-or-nothing: every id must be in
// the archive store, or nothing moves — a miss is the batch not-found shape
// (details.missing), except an id only on the hot board, which is its own
// validation error (there is nothing to restore). Duplicates collapse.
//
// A restored task comes back EXACTLY as archived — done lane, Closed stamp,
// every field byte-preserved: restoring answers "put it back on the board",
// and reopening is `furrow move <id> <lane>`'s job (which already clears
// Closed on leaving the done lane). Same commit order as archiveMove, with the
// stores swapped: the DESTINATION (hot) is persisted first — bodies, assets,
// index — and only then is the archive side updated and its copies deleted, so
// an interrupted run never leaves a hot index entry whose body is still
// trapped in the archive. It leaves one of three states: hot bodies and
// assets that no hot shard names (stopped before the hot index was saved —
// lint's orphan-body warns on the bodies, and the re-run overwrites them);
// the task in BOTH indexes (stopped between the two saves — lint's
// duplicate-id, finished by the re-run); or archive/ bodies and assets that no
// archive/ shard names (stopped after archive/'s save — no lint sees them, and
// no re-run clears them, the id being archived no longer).
//
// An id in both indexes is that middle state only when it is the same task
// (core.SameTask). The re-run keeps the hot copy as it stands — an edit made
// there since the interruption is the task's newest state — so that copy is
// the one the session guard judges; the dep check skips it, since the call
// adds no edge to the board; and only archive/'s copy goes. The hot body is
// rewritten from archive/'s while it is blank or still holds archive/'s bytes,
// the ones the interrupted run wrote, and a hot body that lacks archive/'s
// prose refuses the call (pickFinishedBody, refuseBodyLoss), as does an asset
// the two sides hold under one name with other bytes (refuseAssetClash).
// Under any other task the id is refused before the first write
// (refuseIDClash). The asset half is the same rule as the outbound move, read
// from archive/'s side: every asset a restored task owns or its body shows
// comes back, and archive/ keeps its copy of any that an archived body still
// points at (asset_hold.go).
func (a *App) Unarchive(ids []string) (*UnarchiveReport, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	arc, arcIdx, err := a.archiveLoad()
	if err != nil {
		return nil, err
	}
	if err := raiseMoveRefusals(a.Store, idx, arc, arcIdx); err != nil {
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
	if err := refuseIDClash(moved, idx, "on the hot board", "restored"); err != nil {
		return nil, err
	}
	onBoard := map[string]bool{} // ids whose interrupted move this call finishes
	var restoring []core.Task
	for i := range moved {
		if live, _ := idx.Find(moved[i].ID); live != nil {
			moved[i] = *live
			onBoard[live.ID] = true
			continue
		}
		restoring = append(restoring, moved[i])
	}
	board := map[string]string{}  // a finished move's hot body, read once
	useBoard := map[string]bool{} // ... and whether it stays as it stands
	var lossy []string
	for _, t := range moved {
		if !onBoard[t.ID] {
			continue
		}
		hot, err := a.Store.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		archived, err := arc.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		keep, ok := pickFinishedBody(hot, archived)
		if !ok {
			lossy = append(lossy, t.ID)
			continue
		}
		board[t.ID], useBoard[t.ID] = hot, keep
	}
	if len(lossy) > 0 {
		return nil, refuseBodyLoss(lossy, "restored")
	}
	leaving := idSet(moved)
	// Guarded like every task write: a restore puts tasks back on the hot
	// board of the repos they carry, so a session holding one of those repos
	// refuses it before anything moves (t-kw28).
	var restoredRepos []string
	for _, t := range moved {
		restoredRepos = unionRepos(restoredRepos, t.Repos)
	}
	if err := a.guardRepos(strings.Join(ids, ","), restoredRepos, ""); err != nil {
		return nil, err
	}
	// The mirror of ArchiveIDs' depHolds: a restored task whose dependency
	// stays archived would read as blocked on an id the hot index lacks (lint
	// dep-missing, the board's pre-push lint refusing the push). Restore them
	// together, or drop the edge first. A finished move adds no edge to the
	// board: its hot copy is already there.
	if refs := archivedDepsOf(restoring, arcIdx, idx, leaving); len(refs.Deps) > 0 {
		return nil, &core.Error{
			Code:    core.CodeValidation,
			Kind:    core.KindReferenced,
			Subject: refs.Deps[0].From,
			Msg: fmt.Sprintf("still depends on an archived task — %s; restoring would strand the edge (lint dep-missing), so nothing was restored: "+
				"unarchive them together (`furrow unarchive %s`), or drop the edge first", refs.summary(), strings.Join(append(ids, refs.archivedDeps()...), " ")),
			Details: map[string]any{"references": refs},
		}
	}
	arcEpics, err := arc.LoadEpics()
	if err != nil {
		return nil, err
	}
	plan, err := planAssets(arc, leaving, remainingIDs(arcIdx, arcEpics, leaving))
	if err != nil {
		return nil, err
	}
	if err := refuseAssetClash(arc, a.Store, plan, "restored"); err != nil {
		return nil, err
	}
	for _, t := range moved {
		body, err := arc.LoadBody(t.ID)
		if err != nil {
			return nil, err
		}
		// Through saveBody, so the restored body rides the journal: a retry of an
		// interrupted unarchive finds the hot body already TRACKED, and a plain
		// sync would otherwise leave it pending (t-gqe6). A finished move writes
		// the body only while it is blank or still holds archive/'s bytes, the
		// ones the interrupted run wrote: an edit made since is newer, and a hand
		// edit is not furrow's to publish.
		if !onBoard[t.ID] || !useBoard[t.ID] || board[t.ID] == body {
			if err := a.saveBody(t.ID, body); err != nil {
				return nil, err
			}
		}
		if !onBoard[t.ID] {
			idx.Add(t)
		}
		arcIdx.Remove(t.ID)
	}
	copied, err := copyAssets(arc, a.Store, plan)
	if err != nil {
		return nil, err
	}
	if err := a.saveIndex(idx); err != nil {
		return nil, err
	}
	if err := arc.Save(arcIdx); err != nil {
		return nil, err
	}
	for _, t := range moved { // both indexes are durable now — safe to delete the source
		if err := arc.DeleteBody(t.ID); err != nil {
			return nil, err
		}
	}
	deleted, kept, err := reapAssets(arc, plan)
	if err != nil {
		return nil, err
	}
	return &UnarchiveReport{Tasks: moved, Assets: AssetTransfer{Copied: copied, Deleted: deleted, Kept: kept}}, nil
}
