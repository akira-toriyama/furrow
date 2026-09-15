package memstore

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// The two behaviors pinned here are the ones the double actually got wrong: a
// Save that did not canonicalize, and a ListRepos that handed out the store's own
// *time.Time. Both made memstore promise LESS than fsstore, and since every
// app-layer test is memstore-backed while production is entirely fsstore, the
// difference showed up as app code growing point-fixes for shapes the real store
// never produces — plus one lint test that was green only because a value of 9
// survived a write it cannot survive on disk.
//
// The third is Save's ownership rule (t-msqv): a Save touches only what its own
// index changed. fsstore's sweep used to delete every shard the index did not
// name, so ten concurrent `furrow add` on one board left zero shards, and its
// compare-against-disk rewrite put a stale copy over a co-writer's edit; the
// double replaced its whole map, the same failure in one line. Both stores now
// answer to the same two questions per shard — did THIS index meet it, and did
// THIS index move it — so an app test on memstore sees exactly the survival a
// second process gets on disk.
//
// This is deliberately NOT a port-wide contract suite over every method of
// core.Store: that was weighed and rejected as premature mechanization (furrow
// task t-0mmj). Three behaviors, three demonstrated failures.

type storeCase struct {
	name string
	make func(t *testing.T) core.Store
}

func storeCases() []storeCase {
	lanes := []string{"inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"}
	return []storeCase{
		{"memstore", func(t *testing.T) core.Store { return New("t-", "e-", 5) }},
		{"fsstore", func(t *testing.T) core.Store {
			// A fresh dir: fsstore's gateWrite stamps meta.json on the first write,
			// so no init dance is needed.
			return fsstore.New(filepath.Join(t.TempDir(), ".furrow"), lanes, "t-", "e-", 5)
		}},
	}
}

// Save canonicalizes in BOTH directions, identically for both stores: the stored
// task takes the on-disk shape, and the caller's index is normalized in place
// (fsstore gets that from marshalling &idx.Tasks[i] on the way to a shard).
func TestSaveCanonicalizesLikeFsstore(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			st := sc.make(t)
			nine := 9
			// Deliberately messy on every axis canonicalizeTask touches: unsorted +
			// duplicated sets, nil collections, an out-of-range estimate, and a
			// zoned sub-second stamp.
			zoned := time.Date(2026, 6, 25, 12, 0, 0, 500_000_000, time.FixedZone("JST", 9*60*60))
			idx := &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{{
				ID: "t-00001", Title: "messy", Status: "inbox",
				Labels:  []string{"zeta", "alpha", "zeta"},
				Deps:    []string{"t-9", "t-1x"},
				Value:   &nine,
				Created: zoned, Updated: zoned, Due: &zoned,
			}}}
			if err := st.Save(idx); err != nil {
				t.Fatal(err)
			}

			wantCreated := zoned.UTC().Truncate(time.Second)
			check := func(where string, tk core.Task) {
				t.Helper()
				if !reflect.DeepEqual(tk.Labels, []string{"alpha", "zeta"}) {
					t.Errorf("%s: labels not sorted+deduped: %v", where, tk.Labels)
				}
				if !reflect.DeepEqual(tk.Deps, []string{"t-1x", "t-9"}) {
					t.Errorf("%s: deps not sorted: %v", where, tk.Deps)
				}
				if tk.Repos == nil || tk.Refs == nil || tk.Checklist == nil {
					t.Errorf("%s: nil collection survived (must be []): %+v", where, tk)
				}
				if tk.Value == nil || *tk.Value != core.EstimateMax {
					t.Errorf("%s: value 9 was not clamped to %d: %v", where, core.EstimateMax, tk.Value)
				}
				if !tk.Created.Equal(wantCreated) || tk.Created.Location() != time.UTC {
					t.Errorf("%s: created not UTC whole-second: %s", where, tk.Created)
				}
				// due matters twice over: it is the one stamp an operator authors in
				// wall clock, and app.ParseDue relies on this instead of normalizing
				// at the parse boundary a second time.
				if tk.Due == nil || !tk.Due.Equal(wantCreated) || tk.Due.Location() != time.UTC {
					t.Errorf("%s: due not UTC whole-second: %v", where, tk.Due)
				}
			}
			got, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Tasks) != 1 {
				t.Fatalf("%s: expected 1 task, got %d", sc.name, len(got.Tasks))
			}
			check("after Save+Load", got.Tasks[0])
			// app code returns the in-memory task from a mutation without
			// re-reading, so a store that skips canonicalizing the CALLER's index
			// leaks the pre-Save shape into the --json envelope.
			check("caller's index after Save", idx.Tasks[0])
		})
	}
}

// Every repo read is deep-copied out: RepoRecord is two *time.Time behind a
// struct copy, so a consumer that advances a returned clock must not reach the
// store. (fsstore parses fresh bytes, so it gets this for free — which is exactly
// why the double must do it explicitly.)
func TestRepoReadsIsolateTimestampsLikeFsstore(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			st := sc.make(t)
			seeded := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			agent := seeded.AddDate(0, 1, 0)
			if err := st.SaveRepo(&core.RepoRecord{
				Repo: "me/x", LastReviewed: &seeded, LastAgentReviewed: &agent,
			}); err != nil {
				t.Fatal(err)
			}

			recs, err := st.ListRepos()
			if err != nil || len(recs) != 1 {
				t.Fatalf("ListRepos: %v (%d records)", err, len(recs))
			}
			*recs[0].LastReviewed = seeded.AddDate(10, 0, 0)
			*recs[0].LastAgentReviewed = agent.AddDate(10, 0, 0)

			one, ok, err := st.LoadRepo("me/x")
			if err != nil || !ok {
				t.Fatalf("LoadRepo: %v ok=%v", err, ok)
			}
			if !one.LastReviewed.Equal(seeded) || !one.LastAgentReviewed.Equal(agent) {
				t.Errorf("ListRepos leaked the store's clocks: %s / %s", one.LastReviewed, one.LastAgentReviewed)
			}
			*one.LastReviewed = seeded.AddDate(20, 0, 0)

			again, err := st.ListRepos()
			if err != nil {
				t.Fatal(err)
			}
			if !again[0].LastReviewed.Equal(seeded) {
				t.Errorf("LoadRepo leaked the store's clock: %s", again[0].LastReviewed)
			}
		})
	}
}

// Ten indexes loaded from the same empty board, each adding one task and saved
// in turn, must leave ten tasks: a shard an index never met is not its to
// delete. Sequential on purpose — it is the exact interleaving ten processes
// produce (every Load before any Save), reproduced deterministically, and the
// old sweep failed it at N=2.
func TestSaveNeverDeletesAShardItNeverMet(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			st := sc.make(t)
			if err := st.Save(&core.Index{Tasks: []core.Task{parityTask("t-pre01", "already there")}}); err != nil {
				t.Fatal(err)
			}
			const n = 10
			idxs := make([]*core.Index, n)
			for i := range idxs {
				idx, err := st.Load()
				if err != nil {
					t.Fatal(err)
				}
				idxs[i] = idx
			}
			for i, idx := range idxs {
				idx.Add(parityTask(fmt.Sprintf("t-add%02d", i), fmt.Sprintf("z%d", i)))
				if err := st.Save(idx); err != nil {
					t.Fatalf("save %d: %v", i, err)
				}
			}
			got, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Tasks) != n+1 {
				ids := make([]string, 0, len(got.Tasks))
				for _, tk := range got.Tasks {
					ids = append(ids, tk.ID)
				}
				t.Fatalf("after %d adds from separate loads: %d tasks survive, want %d: %v", n, len(got.Tasks), n+1, ids)
			}
		})
	}
}

// Two indexes loaded at the same moment edit DIFFERENT tasks: each Save lands
// its own edit and leaves the other's alone, because an index rewrites only a
// task whose bytes it moved. A deletion by a co-writer stands the same way: an
// index that did not touch the task does not put it back.
func TestSaveRewritesOnlyWhatThisIndexMoved(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			st := sc.make(t)
			if err := st.Save(&core.Index{Tasks: []core.Task{
				parityTask("t-xxxxx", "x"), parityTask("t-yyyyy", "y"),
			}}); err != nil {
				t.Fatal(err)
			}
			a, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			b, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			ty, _ := b.Find("t-yyyyy")
			ty.Title = "y by b"
			if err := st.Save(b); err != nil {
				t.Fatal(err)
			}
			tx, _ := a.Find("t-xxxxx")
			tx.Title = "x by a"
			if err := st.Save(a); err != nil { // a still holds the ORIGINAL y
				t.Fatal(err)
			}
			got, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			if gx, _ := got.Find("t-xxxxx"); gx == nil || gx.Title != "x by a" {
				t.Errorf("a's own edit did not land: %+v", gx)
			}
			if gy, _ := got.Find("t-yyyyy"); gy == nil || gy.Title != "y by b" {
				t.Errorf("b's edit of the task a never touched was overwritten by a's stale copy: %+v", gy)
			}

			// c removes y; a (holding y untouched since its last Save) saves again.
			c, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			c.Remove("t-yyyyy")
			if err := st.Save(c); err != nil {
				t.Fatal(err)
			}
			if err := st.Save(a); err != nil {
				t.Fatal(err)
			}
			got, err = st.Load()
			if err != nil {
				t.Fatal(err)
			}
			if got.Has("t-yyyyy") {
				t.Error("c's removal was undone by an index that never touched the task")
			}
			if !got.Has("t-xxxxx") {
				t.Error("x vanished")
			}
		})
	}
}

func parityTask(id, title string) core.Task {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	return core.Task{ID: id, Title: title, Status: "inbox", Priority: 100,
		Created: now, Updated: now, Body: core.BodyPath(id)}
}
