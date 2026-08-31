package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// A v5-era leftover, parked by the passthrough on the next load.
	plantExtra(t, a.Dir, "tasks/"+holder.ID+".json", "parent", "t-gone")

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
	if len(before.Deps) != 1 || len(before.ExtraKeys()) != 1 {
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
