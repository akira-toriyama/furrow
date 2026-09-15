package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// Every body the upgrade writes on the HOT store is journaled: a renamed
// converted-task body, a link-rewritten body, a dropped-dep note. They are
// tracked-modified files, and a plain `furrow sync` — what the CLI tells the
// operator to run next — commits such a body only when the journal names it,
// so unjournaled they stayed in pending_bodies, unpublished (t-gqe6). The
// archive store's writes are machine-sync paths and stay unjournaled.
func TestMigrateBodiesApplyJournalsHotStoreWrites(t *testing.T) {
	a := newApp()
	for id, body := range map[string]string{
		"t-old00": "# old\n",
		"t-aaaaa": "see [[t-old00]]\n",
		"t-ddddd": "# d\n",
	} {
		if err := a.Store.SaveBody(id, body); err != nil { // seeded, not touched
			t.Fatal(err)
		}
	}
	plans := []core.V6Epic{{TaskID: "t-old00", Epic: core.Epic{ID: "e-new00", Title: "old"}}}
	conv := map[string]string{"t-old00": "e-new00"}
	drops := []core.V6DroppedDep{{TaskID: "t-ddddd", DepID: "t-old00", EpicID: "e-new00"}}

	if _, err := a.migrateBodiesApply(a.Store, plans, conv, drops); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e-new00", "t-old00", "t-aaaaa", "t-ddddd"} {
		if !a.bodiesTouched[id] {
			t.Errorf("hot-store body %s written by upgrade is not journaled: %v", id, a.bodiesTouched)
		}
	}

	a.bodiesTouched = nil
	other := memstore.New("t-", "e-", 5)
	for id, body := range map[string]string{"t-old00": "# old\n", "t-aaaaa": "see [[t-old00]]\n", "t-ddddd": "# d\n"} {
		if err := other.SaveBody(id, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.migrateBodiesApply(other, plans, conv, drops); err != nil {
		t.Fatal(err)
	}
	if len(a.bodiesTouched) != 0 {
		t.Errorf("the archive store's writes must not enter the hot journal: %v", a.bodiesTouched)
	}
}

// The body an unarchive restores to the hot store is journaled too: a retry
// of an interrupted unarchive finds it already tracked, where an unjournaled
// write would be left pending by a plain sync (t-gqe6).
func TestUnarchiveJournalsTheRestoredBody(t *testing.T) {
	a := newFSApp(t)
	tk, err := a.Add("retired", AddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
		t.Fatal(err)
	}
	a.bodiesTouched = nil
	if _, err := a.Unarchive([]string{tk.ID}); err != nil {
		t.Fatal(err)
	}
	if !a.bodiesTouched[tk.ID] {
		t.Errorf("the restored body is not journaled: %v", a.bodiesTouched)
	}
}
