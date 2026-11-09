package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// plantExtra rewrites a shard on disk with an extra top-level key, so the next
// Load parks it — the only honest way into the passthrough (nothing in furrow
// can invent an unknown key).
func plantExtra(t *testing.T, dir, rel, key, val string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m[key] = val
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The whole loop on a real board: a satisfied dep edge and a planted legacy
// key are REPORTED by the preview (both classes, nothing written), pruned only
// by an --yes with that class selected, and the prune neither advances
// `updated` (bookkeeping, not progress — respace's rule) nor touches the other
// class. A second apply finds a clean board.
func TestTidyPreviewAndApply(t *testing.T) {
	a := newFSApp(t)
	dep, _ := a.Add("shipped slice", AddOpts{})
	holder, _ := a.Add("epic anchor", AddOpts{Deps: []string{dep.ID}})
	a.Done(dep.ID) //nolint:errcheck // the edge must read as satisfied

	// A v5-era leftover, parked by the passthrough on the next load. The
	// backdated `updated` makes the clock check below exact: a stamp in the
	// same second as Add would otherwise compare equal.
	plantExtra(t, a.Dir, "tasks/"+holder.ID+".json", "parent", "t-gone")
	plantExtra(t, a.Dir, "tasks/"+holder.ID+".json", "updated", pastStamp)

	rep, err := a.Tidy(TidyOpts{DoneDeps: true, UnknownKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || !rep.Changed {
		t.Fatalf("preview = %+v, want changed and not applied", rep)
	}
	if len(rep.DoneDeps) != 1 || rep.DoneDeps[0].ID != holder.ID || rep.DoneDeps[0].Deps[0] != dep.ID {
		t.Errorf("done_deps = %+v, want the %s->%s edge", rep.DoneDeps, holder.ID, dep.ID)
	}
	if len(rep.UnknownKeys) != 1 || rep.UnknownKeys[0].ID != holder.ID || rep.UnknownKeys[0].Keys[0] != "parent" {
		t.Errorf("unknown_keys = %+v, want parent on %s", rep.UnknownKeys, holder.ID)
	}

	before, _, _ := a.Get(holder.ID)
	if len(before.Deps) != 1 || len(before.ExtraKeys()) != 1 || !before.Updated.Equal(past) {
		t.Fatalf("preview mutated the board: %+v (extras %v)", before, before.ExtraKeys())
	}

	// Apply ONE class: the dep prune must not eat the unknown key.
	rep, err = a.Tidy(TidyOpts{DoneDeps: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied {
		t.Fatalf("apply = %+v, want applied", rep)
	}
	after, _, _ := a.Get(holder.ID)
	if len(after.Deps) != 0 {
		t.Errorf("dep edge survived the prune: %v", after.Deps)
	}
	if got := after.ExtraKeys(); len(got) != 1 || got[0] != "parent" {
		t.Errorf("unselected class was pruned too: extras = %v, want [parent]", got)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("updated advanced %v -> %v; a prune is bookkeeping and must not touch the staleness clocks", before.Updated, after.Updated)
	}

	if rep, err = a.Tidy(TidyOpts{UnknownKeys: true, Apply: true}); err != nil || !rep.Applied {
		t.Fatalf("unknown-keys apply = %+v, %v", rep, err)
	}
	final, _, _ := a.Get(holder.ID)
	if len(final.ExtraKeys()) != 0 {
		t.Errorf("extras survived: %v", final.ExtraKeys())
	}
	if rep, err = a.Tidy(TidyOpts{DoneDeps: true, UnknownKeys: true, Apply: true}); err != nil || rep.Changed || rep.Applied {
		t.Fatalf("clean board re-run = %+v, %v — want changed:false applied:false", rep, err)
	}
}

// The unknown-key sweep covers all four machine-written record kinds — task and
// epic shards here (repo shards and meta.json ride the same ExtraKeys API; the
// fsstore meta arm is pinned in the store's own test).
func TestTidyUnknownKeysEpicShard(t *testing.T) {
	a := newFSApp(t)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	plantExtra(t, a.Dir, "epics/"+e.ID+".json", "sprint", "s3")

	rep, err := a.Tidy(TidyOpts{UnknownKeys: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.UnknownKeys) != 1 || rep.UnknownKeys[0].ID != e.ID || !strings.HasPrefix(rep.UnknownKeys[0].File, "epics/") {
		t.Fatalf("unknown_keys = %+v, want sprint on the epic shard", rep.UnknownKeys)
	}
	got, _, err := a.Store.LoadEpic(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ExtraKeys()) != 0 {
		t.Errorf("epic extras survived: %v", got.ExtraKeys())
	}
}

// Tasks already finished keep their satisfied edges even under --done-deps: a
// done task's dep set is pure history, and rewriting closed records for
// tidiness is exactly the churn tidy exists to avoid.
func TestTidyDoneDepsSkipsClosedTasks(t *testing.T) {
	a := newFSApp(t)
	dep, _ := a.Add("first half", AddOpts{})
	whole, _ := a.Add("both halves", AddOpts{Deps: []string{dep.ID}})
	a.Done(dep.ID)   //nolint:errcheck // asserted below
	a.Done(whole.ID) //nolint:errcheck // asserted below

	rep, err := a.Tidy(TidyOpts{DoneDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed {
		t.Fatalf("a closed task's satisfied edge was offered for pruning: %+v", rep)
	}
}

// pastStamp is a shard `updated` far behind any clock a test runs on, so a
// prune that stamped the record cannot compare equal by landing in the same
// second (the system clock truncates to seconds).
const pastStamp = "2020-01-02T03:04:05Z"

var past = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

// archivedBoard retires one task and gives archive/ an epic shard (the v6
// migration files converted boxes there) and a repo review shard (no command
// writes one there, but the store reads it like any other), with no key
// planted. It returns the archived task and epic ids; the repo is owner/app.
func archivedBoard(t *testing.T, a *App) (taskID, epicID string) {
	t.Helper()
	tk, err := a.Add("retired", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil { // creates archive/
		t.Fatal(err)
	}
	arc, _, err := a.archiveLoad()
	if err != nil {
		t.Fatal(err)
	}
	now := a.Clock.Now()
	e := core.Epic{ID: "e-9zz9z", Title: "v5 box", Created: now, Updated: now, Closed: &now, Body: core.BodyPath("e-9zz9z")}
	if err := arc.SaveEpic(&e); err != nil {
		t.Fatal(err)
	}
	if err := arc.SaveRepo(&core.RepoRecord{Repo: "owner/app"}); err != nil {
		t.Fatal(err)
	}
	return tk.ID, e.ID
}

// archiveWithParkedKeys is archivedBoard with a legacy key planted in each
// kind of archive/ record: the task, epic and repo shards and meta.json.
func archiveWithParkedKeys(t *testing.T, a *App) (taskID, epicID string) {
	t.Helper()
	taskID, epicID = archivedBoard(t, a)
	plantExtra(t, a.Dir, "archive/"+core.TaskPath(taskID), "parent", "t-7ptfx")
	plantExtra(t, a.Dir, "archive/"+core.EpicPath(epicID), "sprint", "s3")
	plantExtra(t, a.Dir, "archive/"+core.RepoRecordPath("owner/app"), "cadence_days", "14")
	plantExtra(t, a.Dir, "archive/meta.json", "min_reader", "3")
	return taskID, epicID
}

// archive/ is a store of the board, and upgrade sweeps it and reports what it
// kept there — so --unknown-keys reaches it too: each archived record is
// listed with its file under archive/, pruned by the same apply without
// advancing `updated` or the layout version, and a re-run finds nothing. Tidy
// used to stop at the hot store, leaving a hand-edit as the only way out
// (t-c6nn).
func TestTidyUnknownKeysReachesArchiveStore(t *testing.T) {
	a := newFSApp(t)
	taskID, epicID := archiveWithParkedKeys(t, a)
	plantExtra(t, a.Dir, "archive/"+core.TaskPath(taskID), "updated", pastStamp)

	rep, err := a.Tidy(TidyOpts{UnknownKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []TidyUnknownKeys{
		{ID: taskID, File: "archive/tasks/" + taskID + ".json", Keys: []string{"parent"}},
		{ID: epicID, File: "archive/epics/" + epicID + ".json", Keys: []string{"sprint"}},
		{ID: "owner/app", File: "archive/repos/owner__app.json", Keys: []string{"cadence_days"}},
		{ID: "archive", File: "archive/meta.json", Keys: []string{"min_reader"}},
	}
	if rep.Applied || !reflect.DeepEqual(rep.UnknownKeys, want) {
		t.Fatalf("preview = %+v, want unapplied rows %+v", rep, want)
	}

	arc, idx, err := a.archiveLoad()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := idx.Find(taskID)
	if before == nil || len(before.ExtraKeys()) != 1 || !before.Updated.Equal(past) {
		t.Fatalf("the preview wrote archive/: %+v", before)
	}

	if rep, err = a.Tidy(TidyOpts{UnknownKeys: true, Apply: true}); err != nil || !rep.Applied {
		t.Fatalf("apply = %+v, %v", rep, err)
	}
	_, idx, err = a.archiveLoad()
	if err != nil {
		t.Fatal(err)
	}
	after, _ := idx.Find(taskID)
	if after == nil || len(after.ExtraKeys()) != 0 {
		t.Fatalf("archived task extras survived the apply: %+v", after)
	}
	if !after.Updated.Equal(past) {
		t.Errorf("updated advanced %v -> %v; a prune is bookkeeping", past, after.Updated)
	}
	e, _, err := arc.LoadEpic(epicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.ExtraKeys()) != 0 {
		t.Errorf("archived epic extras survived the apply: %v", e.ExtraKeys())
	}
	rec, _, err := arc.LoadRepo("owner/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.ExtraKeys()) != 0 {
		t.Errorf("archived repo record extras survived the apply: %v", rec.ExtraKeys())
	}
	m, err := arc.LoadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ExtraKeys()) != 0 || m.SchemaVersion != core.SchemaVersion {
		t.Errorf("archive/meta.json = version %d extras %v, want version %d and no extras", m.SchemaVersion, m.ExtraKeys(), core.SchemaVersion)
	}
	if rep, err = a.Tidy(TidyOpts{UnknownKeys: true}); err != nil || rep.Changed {
		t.Fatalf("re-run = %+v, %v — want a clean board", rep, err)
	}
}

// A refusal is total: when archive/ would refuse its part of the apply, the
// run refuses before the hot store's prune lands — not after it, which would
// leave half the board pruned behind an error. Two refusals can come from
// archive/: its write gate (an older layout) and Save's duplicate-id check (a
// misnamed copy of a shard, from a hand edit or a merge).
func TestTidyRefusalIsTotal(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		spoil func(t *testing.T, a *App, archivedID string)
	}{
		{"read-only archive", core.KindSchemaUpgradeRequired, func(t *testing.T, a *App, _ string) {
			old := fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion-1)
			if err := os.WriteFile(filepath.Join(a.Dir, "archive", "meta.json"), []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate archived id", core.KindValidation, func(t *testing.T, a *App, archivedID string) {
			b, err := os.ReadFile(filepath.Join(a.Dir, "archive", core.TaskPath(archivedID)))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(a.Dir, "archive", core.TaskPath("t-zzzzz")), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newFSApp(t)
			archivedID, _ := archiveWithParkedKeys(t, a)
			live, err := a.Add("live", AddOpts{})
			if err != nil {
				t.Fatal(err)
			}
			plantExtra(t, a.Dir, core.TaskPath(live.ID), "parent", "t-gone")
			c.spoil(t, a, archivedID)

			_, err = a.Tidy(TidyOpts{UnknownKeys: true, Apply: true})
			if fe := core.AsError(err); fe == nil || fe.Kind != c.kind {
				t.Fatalf("apply = %v, want kind %s", err, c.kind)
			}
			got, _, err := a.Get(live.ID)
			if err != nil {
				t.Fatal(err)
			}
			if keys := got.ExtraKeys(); len(keys) != 1 || keys[0] != "parent" {
				t.Errorf("hot extras = %v after the refusal, want [parent] untouched", keys)
			}
		})
	}
}

// A store with nothing parked is not written at all. On a board that never
// archived, a hot-only prune must not create archive/ (a no-op Save stamps a
// fresh store into being); and a clean archive/ on an older layout must
// neither refuse the hot prune nor be touched by it.
func TestTidyNeverWritesACleanArchive(t *testing.T) {
	t.Run("never archived", func(t *testing.T) {
		a := newFSApp(t)
		live, err := a.Add("live", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.ReviewRepo("owner/app", false); err != nil {
			t.Fatal(err)
		}
		plantExtra(t, a.Dir, core.TaskPath(live.ID), "parent", "t-gone")
		plantExtra(t, a.Dir, core.RepoRecordPath("owner/app"), "cadence_days", "14")

		rep, err := a.Tidy(TidyOpts{UnknownKeys: true, Apply: true})
		if err != nil || !rep.Applied || len(rep.UnknownKeys) != 2 {
			t.Fatalf("apply = %+v, %v — want the task and repo rows applied", rep, err)
		}
		if _, err := os.Stat(filepath.Join(a.Dir, "archive")); !os.IsNotExist(err) {
			t.Errorf("a hot-only prune created archive/ (stat: %v)", err)
		}
		rec, _, err := a.Store.LoadRepo("owner/app")
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.ExtraKeys()) != 0 {
			t.Errorf("repo record extras survived the apply: %v", rec.ExtraKeys())
		}
		if rep, err = a.Tidy(TidyOpts{UnknownKeys: true}); err != nil || rep.Changed {
			t.Fatalf("re-run = %+v, %v — want a clean board", rep, err)
		}
	})
	t.Run("clean read-only archive", func(t *testing.T) {
		a := newFSApp(t)
		old, err := a.Add("retired", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Done(old.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ArchiveIDs([]string{old.ID}, false); err != nil {
			t.Fatal(err)
		}
		live, err := a.Add("live", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		plantExtra(t, a.Dir, core.TaskPath(live.ID), "parent", "t-gone")
		metaPath := filepath.Join(a.Dir, "archive", "meta.json")
		stale := []byte(fmt.Sprintf("{\n  \"schema_version\": %d\n}\n", core.SchemaVersion-1))
		if err := os.WriteFile(metaPath, stale, 0o644); err != nil {
			t.Fatal(err)
		}

		if rep, err := a.Tidy(TidyOpts{UnknownKeys: true, Apply: true}); err != nil || !rep.Applied {
			t.Fatalf("apply = %+v, %v — a clean archive/ must not refuse the hot prune", rep, err)
		}
		got, _, err := a.Get(live.ID)
		if err != nil {
			t.Fatal(err)
		}
		if keys := got.ExtraKeys(); len(keys) != 0 {
			t.Errorf("hot extras = %v, want them pruned", keys)
		}
		if b, err := os.ReadFile(metaPath); err != nil || !bytes.Equal(b, stale) {
			t.Errorf("archive/meta.json = %q (%v), want it untouched", b, err)
		}
	})
}

// Both classes in one apply, with no parked key on a hot task shard: the dep
// prune is then the hot index's only change, and the apply must still save it
// — applied:true says the satisfied edge is gone from disk.
func TestTidyBothClassesLandTheDepPrune(t *testing.T) {
	a := newFSApp(t)
	archiveWithParkedKeys(t, a) // every parked key lives in archive/
	dep, err := a.Add("shipped slice", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := a.Add("anchor", AddOpts{Deps: []string{dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(dep.ID); err != nil {
		t.Fatal(err)
	}

	rep, err := a.Tidy(TidyOpts{DoneDeps: true, UnknownKeys: true, Apply: true})
	if err != nil || !rep.Applied || len(rep.DoneDeps) != 1 {
		t.Fatalf("apply = %+v, %v — want the %s edge applied", rep, err, holder.ID)
	}
	got, _, err := a.Get(holder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deps) != 0 {
		t.Errorf("applied:true, but the satisfied edge is still on disk: deps %v", got.Deps)
	}
	if rep, err = a.Tidy(TidyOpts{DoneDeps: true, UnknownKeys: true}); err != nil || rep.Changed {
		t.Fatalf("re-run = %+v, %v — want a clean board", rep, err)
	}
}

// archive/'s index is saved only when one of its task shards carried a key. A
// prune that reaches only an archived epic must leave the index alone: that
// Save would respell misnamed shards, and over a duplicate id (a hand copy, a
// merge) it would refuse after the hot prune had landed.
func TestTidySavesTheArchiveIndexOnlyForTaskKeys(t *testing.T) {
	a := newFSApp(t)
	archivedID, epicID := archivedBoard(t, a)
	plantExtra(t, a.Dir, "archive/"+core.EpicPath(epicID), "sprint", "s3")
	shard := filepath.Join(a.Dir, "archive", core.TaskPath(archivedID))
	orig, err := os.ReadFile(shard)
	if err != nil {
		t.Fatal(err)
	}
	dup := filepath.Join(a.Dir, "archive", core.TaskPath("t-zzzzz"))
	if err := os.WriteFile(dup, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := a.Add("live", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	plantExtra(t, a.Dir, core.TaskPath(live.ID), "parent", "t-gone")

	if rep, err := a.Tidy(TidyOpts{UnknownKeys: true, Apply: true}); err != nil || !rep.Applied {
		t.Fatalf("apply = %+v, %v — an epic-only archive prune must not meet the index's duplicate", rep, err)
	}
	got, _, err := a.Get(live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys := got.ExtraKeys(); len(keys) != 0 {
		t.Errorf("hot extras = %v, want them pruned", keys)
	}
	arc, _, err := a.archiveLoad()
	if err != nil {
		t.Fatal(err)
	}
	e, _, err := arc.LoadEpic(epicID)
	if err != nil {
		t.Fatal(err)
	}
	if keys := e.ExtraKeys(); len(keys) != 0 {
		t.Errorf("archived epic extras = %v, want them pruned", keys)
	}
	for _, p := range []string{shard, dup} {
		if b, err := os.ReadFile(p); err != nil || !bytes.Equal(b, orig) {
			t.Errorf("%s was rewritten (%v); the archive index must not be saved", filepath.Base(p), err)
		}
	}
}
