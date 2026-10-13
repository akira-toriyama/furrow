package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// An archive on one side (delete under tasks/, add under archive/tasks/) and
// a reopen of the same task on the other must meet as the conflict they are.
// git's rename detection paired the archive's delete+add and applied the
// reopen onto the archive copy — cleanly: an open task in archive/ that no
// read showed and lint did not flag (t-pmz8).
func TestSyncArchiveVersusReopenIsAConflictNotASilentArchivedReopen(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)
	a := openBoard(t, cloneA)
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).ArchiveIDs([]string{x.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	b := openBoard(t, cloneB)
	if _, _, err := b.MoveMany([]string{x.ID}, "ready", nil); err != nil {
		t.Fatal(err)
	}
	_, err = openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncConflict {
		t.Fatalf("archive vs reopen must be a sync-conflict, got %v", err)
	}
	// The shard one side deleted is the conflict's path — the resolver has no
	// field-wise merge for a delete, and the envelope names what to look at.
	d, _ := fe.Details.(map[string]any)
	if paths, _ := d["paths"].([]string); len(paths) != 1 || paths[0] != ".furrow/tasks/"+x.ID+".json" {
		t.Errorf("details.paths = %v, want the deleted-on-one-side shard alone", d["paths"])
	}
	if _, err := os.Stat(filepath.Join(cloneB, ".furrow", "archive", "tasks", x.ID+".json")); err == nil {
		t.Fatal("the reopen landed in archive/ — the aborted rebase must restore the board")
	}
	bb := openBoard(t, cloneB)
	if tk, _, err := bb.Get(x.ID); err != nil || tk.Status != "ready" {
		t.Fatalf("the local reopen must survive the abort: %+v %v", tk, err)
	}
}

// lint names an open task that nonetheless sits in archive/.
func TestLintFlagsAnOpenTaskInTheArchive(t *testing.T) {
	a := newFSApp(t)
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{x.ID}, false); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Join(a.Dir, "archive", "tasks", x.ID+".json")
	raw, err := os.ReadFile(shard)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shard, []byte(replaceOnce(string(raw), `"status": "done"`, `"status": "ready"`)), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == "archived-not-done" && p.ID == x.ID && p.Severity == core.SevError {
			return
		}
	}
	t.Fatalf("no archived-not-done for %s in %+v", x.ID, ps)
}

func replaceOnce(s, old, new string) string {
	i := -1
	for j := 0; j+len(old) <= len(s); j++ {
		if s[j:j+len(old)] == old {
			i = j
			break
		}
	}
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}
