package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The t-7hhb contract: a store loses an asset only when nothing remaining in
// it holds the file — archive, unarchive, rm, and epic rm all through
// planAssets. Before, the "<id>-" prefix alone decided, so archiving or
// removing the owner took a file another live body (a repeat successor's, a
// box's) was showing.

func lintCodes(t *testing.T, a *App, codes ...string) []core.Problem {
	t.Helper()
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var hits []core.Problem
	for _, p := range ps {
		for _, c := range codes {
			if p.Code == c {
				hits = append(hits, p)
			}
		}
	}
	return hits
}

func assetOnDisk(t *testing.T, s assetSide, name string) bool {
	t.Helper()
	_, err := s.LoadAsset(name)
	return err == nil
}

// sharedAssetBoard: owner attaches shot.png and shows it; reader (a task) and
// box (an epic) show the same file from their own bodies.
func sharedAssetBoard(t *testing.T, a *App) (owner, reader *core.Task, box *core.Epic, name string) {
	t.Helper()
	var err error
	if owner, err = a.Add("owner", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if reader, err = a.Add("reader", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if box, err = a.EpicAdd("box", EpicAddOpts{}); err != nil {
		t.Fatal(err)
	}
	if name, err = a.Store.SaveAsset(owner.ID, "shot.png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	line := "![shot](" + core.AssetRef(name) + ")"
	for _, id := range []string{owner.ID, reader.ID} {
		if _, err := a.AddNote(id, line); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Store.SaveBody(box.ID, "# box\n\n"+line+"\n"); err != nil {
		t.Fatal(err)
	}
	return owner, reader, box, name
}

func TestArchiveKeepsAssetAnotherBodyShows(t *testing.T) {
	a, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, reader, box, name := sharedAssetBoard(t, a)
	if _, err := a.Done(owner.ID); err != nil {
		t.Fatal(err)
	}
	rep, err := a.ArchiveIDs([]string{owner.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets.Copied) != 1 || len(rep.Assets.Deleted) != 0 || len(rep.Assets.Kept) != 1 {
		t.Fatalf("preview assets = %+v", rep.Assets)
	}
	rep, err = a.ArchiveIDs([]string{owner.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	arc, _ := a.archiveStore()
	if !assetOnDisk(t, a.Store, name) || !assetOnDisk(t, arc, name) {
		t.Fatalf("the asset must be on BOTH sides: hot=%v archive=%v", assetOnDisk(t, a.Store, name), assetOnDisk(t, arc, name))
	}
	kept := rep.Assets.Kept
	if len(rep.Assets.Deleted) != 0 || len(kept) != 1 || kept[0].Name != name || len(kept[0].HeldBy) != 2 || kept[0].HeldBy[0] != box.ID || kept[0].HeldBy[1] != reader.ID {
		t.Fatalf("assets = %+v; want kept by %s and %s", rep.Assets, box.ID, reader.ID)
	}
	if hits := lintCodes(t, a, "asset-missing", "orphan-asset"); len(hits) != 0 {
		t.Errorf("hot store must lint clean, got %+v", hits)
	}

	// The last hot holders leave: reader is archived, the box removed — the hot
	// copy goes with the LAST of them, no reaper needed.
	if _, err := a.Done(reader.ID); err != nil {
		t.Fatal(err)
	}
	rep, err = a.ArchiveIDs([]string{reader.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if kept := rep.Assets.Kept; len(kept) != 1 || kept[0].HeldBy[0] != box.ID {
		t.Fatalf("reader's archive must keep the asset for the box: %+v", rep.Assets)
	}
	erep, err := a.RemoveEpic(box.ID, RemoveOpts{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(erep.Assets.Deleted) != 1 || erep.Assets.Deleted[0] != name {
		t.Fatalf("removing the last holder must reap the hot copy: %+v", erep.Assets)
	}
	if assetOnDisk(t, a.Store, name) || !assetOnDisk(t, arc, name) {
		t.Errorf("hot copy must be gone and the archive copy stay: hot=%v archive=%v", assetOnDisk(t, a.Store, name), assetOnDisk(t, arc, name))
	}
	if hits := lintCodes(t, a, "asset-missing", "orphan-asset"); len(hits) != 0 {
		t.Errorf("hot store must lint clean after the last holder left, got %+v", hits)
	}
}

func TestUnarchiveKeepsArchiveCopyAnArchivedBodyShows(t *testing.T) {
	a, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, reader, box, name := sharedAssetBoard(t, a)
	if _, err := a.RemoveEpic(box.ID, RemoveOpts{Apply: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, reader.ID} {
		if _, err := a.Done(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ArchiveIDs([]string{owner.ID, reader.ID}, false); err != nil {
		t.Fatal(err)
	}
	arc, _ := a.archiveStore()
	if assetOnDisk(t, a.Store, name) || !assetOnDisk(t, arc, name) {
		t.Fatalf("after both left, only archive/ holds the file: hot=%v archive=%v", assetOnDisk(t, a.Store, name), assetOnDisk(t, arc, name))
	}

	rep, err := a.Unarchive([]string{owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets.Copied) != 1 || len(rep.Assets.Deleted) != 0 || len(rep.Assets.Kept) != 1 || rep.Assets.Kept[0].HeldBy[0] != reader.ID {
		t.Fatalf("unarchive assets = %+v; want copied back and kept for %s", rep.Assets, reader.ID)
	}
	if !assetOnDisk(t, a.Store, name) || !assetOnDisk(t, arc, name) {
		t.Fatalf("both sides must hold it while the reader is archived")
	}
	rep, err = a.Unarchive([]string{reader.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets.Deleted) != 1 || assetOnDisk(t, arc, name) {
		t.Fatalf("the last archived holder leaving must reap archive/'s copy: %+v", rep.Assets)
	}
	if hits := lintCodes(t, a, "asset-missing", "orphan-asset"); len(hits) != 0 {
		t.Errorf("round trip must lint clean, got %+v", hits)
	}
}

// An interrupted archive leaves the task registered in BOTH stores with its
// body and asset on both sides. A retry must converge — the copy is an
// idempotent overwrite, the reap re-judged — never fail, never lose the file.
func TestArchiveConvergesOnDoubleRegisteredTask(t *testing.T) {
	a, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, reader, _, name := sharedAssetBoard(t, a)
	if _, err := a.Done(owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{owner.ID}, false); err != nil {
		t.Fatal(err)
	}
	// Re-register the owner in the hot store as the interrupted run would have
	// left it (archive/ already has the shard, body, and asset).
	arc, _ := a.archiveStore()
	arcIdx, _ := arc.Load()
	archived, _ := arcIdx.Find(owner.ID)
	idx, _ := a.Store.Load()
	idx.Add(*archived)
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	body, _ := arc.LoadBody(owner.ID)
	if err := a.Store.SaveBody(owner.ID, body); err != nil {
		t.Fatal(err)
	}

	rep, err := a.ArchiveIDs([]string{owner.ID}, false)
	if err != nil {
		t.Fatalf("retry must converge: %v", err)
	}
	if kept := rep.Assets.Kept; len(kept) != 1 || len(kept[0].HeldBy) != 2 || kept[0].HeldBy[1] != reader.ID {
		t.Fatalf("assets = %+v; want kept for the box and %s", rep.Assets, reader.ID)
	}
	hot, _ := a.Store.Load()
	if hot.Has(owner.ID) || a.Store.BodyExists(owner.ID) {
		t.Error("the retry must clear the hot registration")
	}
	if !assetOnDisk(t, a.Store, name) || !assetOnDisk(t, arc, name) {
		t.Error("the reader still shows the file, so both sides keep it")
	}
	if hits := lintCodes(t, a, "asset-missing", "orphan-asset", "orphan-body"); len(hits) != 0 {
		t.Errorf("must lint clean, got %+v", hits)
	}
}

func TestRemoveTasksKeepsAssetAnotherBodyShows(t *testing.T) {
	a := newApp()
	owner, reader, box, name := sharedAssetBoard(t, a)
	rep, err := a.RemoveTasks([]string{owner.ID}, RemoveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets.Deleted) != 0 || len(rep.Assets.Kept) != 1 || rep.Assets.Kept[0].Name != name {
		t.Fatalf("preview assets = %+v", rep.Assets)
	}
	rep, err = a.RemoveTasks([]string{owner.ID}, RemoveOpts{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	kept := rep.Assets.Kept
	if len(kept) != 1 || len(kept[0].HeldBy) != 2 || kept[0].HeldBy[0] != box.ID || kept[0].HeldBy[1] != reader.ID {
		t.Fatalf("assets = %+v; want kept by %s and %s", rep.Assets, box.ID, reader.ID)
	}
	if !assetOnDisk(t, a.Store, name) {
		t.Fatal("rm deleted a file two other bodies show")
	}
	if hits := lintCodes(t, a, "asset-missing", "orphan-asset"); len(hits) != 0 {
		t.Errorf("must lint clean, got %+v", hits)
	}
	// The remaining holders go: the reader's removal keeps it for the box, the
	// box's removal reaps it.
	if rep, err = a.RemoveTasks([]string{reader.ID}, RemoveOpts{Apply: true}); err != nil || len(rep.Assets.Kept) != 1 {
		t.Fatalf("reader rm: %+v %v", rep, err)
	}
	erep, err := a.RemoveEpic(box.ID, RemoveOpts{Apply: true})
	if err != nil || len(erep.Assets.Deleted) != 1 || assetOnDisk(t, a.Store, name) {
		t.Fatalf("box rm must reap the ownerless file it alone showed: %+v %v", erep, err)
	}
}

// An owner holds its asset even when its own body no longer shows it: removing
// a task that merely referenced another live task's file leaves that file.
func TestRemoveTasksLeavesAnotherOwnersAsset(t *testing.T) {
	a := newApp()
	owner, reader, box, name := sharedAssetBoard(t, a)
	if _, err := a.RemoveEpic(box.ID, RemoveOpts{Apply: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetBody(owner.ID, "# owner\n"); err != nil {
		t.Fatal(err)
	}
	rep, err := a.RemoveTasks([]string{reader.ID}, RemoveOpts{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Assets.Kept) != 1 || rep.Assets.Kept[0].HeldBy[0] != owner.ID || !assetOnDisk(t, a.Store, name) {
		t.Fatalf("the owner holds its file: %+v", rep.Assets)
	}
}
