package fsstore

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

var lanes = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), ".furrow"), lanes, "t-", 5)
}

// mkTask builds a minimal well-formed task at a fixed time.
func mkTask(id, title, status string, prio int) core.Task {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	return core.Task{ID: id, Title: title, Status: status, Priority: prio,
		Created: now, Updated: now, Body: core.BodyPath(id)}
}

// A fresh store (no meta.json, no tasks/) loads an empty, well-formed index so
// `furrow add` works day one.
func TestLoadFreshIsEmpty(t *testing.T) {
	s := newStore(t)
	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if idx.SchemaVersion != core.SchemaVersion || len(idx.Tasks) != 0 {
		t.Errorf("fresh store should load an empty index, got %+v", idx)
	}
}

// Save writes one shard per task under tasks/<id>.json — byte-identical to
// MarshalTask — plus a meta.json carrying the board-wide schema version, and it
// writes NO index.json (that monolith is abolished).
func TestSaveWritesShardsAndMeta(t *testing.T) {
	s := newStore(t)
	a := mkTask("t-0001", "畝", "ready", 100)
	b := mkTask("t-0002", "second", "in-progress", 110)
	if err := s.Save(&core.Index{Tasks: []core.Task{a, b}}); err != nil {
		t.Fatal(err)
	}

	// index.json must never appear.
	if _, err := os.Stat(filepath.Join(s.root, "index.json")); !os.IsNotExist(err) {
		t.Errorf("index.json must not exist under the sharded store (stat err = %v)", err)
	}

	// meta.json equals the canonical MarshalMeta bytes.
	wantMeta, _ := core.MarshalMeta(&core.Meta{SchemaVersion: core.SchemaVersion})
	gotMeta, err := os.ReadFile(filepath.Join(s.root, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if !bytes.Equal(gotMeta, wantMeta) {
		t.Errorf("meta.json bytes\n got %s\nwant %s", gotMeta, wantMeta)
	}

	// each shard equals MarshalTask's bytes (hand-edit == furrow write).
	for _, task := range []core.Task{a, b} {
		tt := task
		want, _ := core.MarshalTask(&tt)
		got, err := os.ReadFile(filepath.Join(s.root, "tasks", task.ID+".json"))
		if err != nil {
			t.Fatalf("read shard %s: %v", task.ID, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("shard %s bytes != MarshalTask\n got %s\nwant %s", task.ID, got, want)
		}
	}
}

// Load folds every tasks/<id>.json shard back into one in-memory index.
func TestLoadFoldsShards(t *testing.T) {
	s := newStore(t)
	if err := s.Save(&core.Index{Tasks: []core.Task{
		mkTask("t-0002", "b", "ready", 110),
		mkTask("t-0001", "a", "ready", 100),
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 2 || !got.Has("t-0001") || !got.Has("t-0002") {
		t.Errorf("Load did not fold both shards: %+v", got)
	}
}

// A no-op Save rewrites nothing: re-saving an untouched board leaves every shard
// (and meta.json) byte-for-byte on disk, so git sees zero churn. We prove it by
// stamping an old mtime and asserting Save left it untouched.
func TestSaveDirtyOnlyNoChurn(t *testing.T) {
	s := newStore(t)
	idx := &core.Index{Tasks: []core.Task{mkTask("t-0001", "a", "ready", 100)}}
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Join(s.root, "tasks", "t-0001.json")
	meta := filepath.Join(s.root, "meta.json")
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{shard, meta} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	// Save the very same index again.
	again, _ := s.Load()
	if err := s.Save(again); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{shard, meta} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(past) {
			t.Errorf("no-op Save rewrote %s (mtime moved to %v) — that is churn", filepath.Base(p), fi.ModTime())
		}
	}
}

// A changed task rewrites only its own shard; unrelated shards are left alone.
func TestSaveRewritesOnlyChanged(t *testing.T) {
	s := newStore(t)
	idx := &core.Index{Tasks: []core.Task{
		mkTask("t-0001", "a", "ready", 100),
		mkTask("t-0002", "b", "ready", 110),
	}}
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	p1 := filepath.Join(s.root, "tasks", "t-0001.json")
	p2 := filepath.Join(s.root, "tasks", "t-0002.json")
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{p1, p2} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	// mutate only t-0001.
	next, _ := s.Load()
	tk, _ := next.Find("t-0001")
	tk.Title = "a changed"
	if err := s.Save(next); err != nil {
		t.Fatal(err)
	}

	fi1, _ := os.Stat(p1)
	fi2, _ := os.Stat(p2)
	if fi1.ModTime().Equal(past) {
		t.Error("changed shard t-0001 was NOT rewritten")
	}
	if !fi2.ModTime().Equal(past) {
		t.Error("untouched shard t-0002 was rewritten (churn)")
	}
}

// A task dropped from the index has its shard deleted on the next Save, and
// Load no longer sees it.
func TestSaveDeletesRemovedShard(t *testing.T) {
	s := newStore(t)
	idx := &core.Index{Tasks: []core.Task{
		mkTask("t-0001", "a", "ready", 100),
		mkTask("t-0002", "b", "ready", 110),
	}}
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	idx.Remove("t-0002")
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "tasks", "t-0002.json")); !os.IsNotExist(err) {
		t.Errorf("removed task's shard should be deleted (stat err = %v)", err)
	}
	got, _ := s.Load()
	if got.Has("t-0002") {
		t.Error("removed task should be gone from a re-loaded index")
	}
}

// ListTaskIDs returns the id of every tasks/<id>.json, sorted, for lint.
func TestListTaskIDs(t *testing.T) {
	s := newStore(t)
	if err := s.Save(&core.Index{Tasks: []core.Task{
		mkTask("t-0002", "b", "ready", 110),
		mkTask("t-0001", "a", "ready", 100),
	}}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListTaskIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "t-0001" || ids[1] != "t-0002" {
		t.Errorf("ListTaskIDs = %v, want [t-0001 t-0002]", ids)
	}
	// a fresh store (no tasks/) returns no ids, no error.
	fresh := newStore(t)
	if ids, err := fresh.ListTaskIDs(); err != nil || len(ids) != 0 {
		t.Errorf("fresh ListTaskIDs = %v, %v", ids, err)
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	s := newStore(t)
	if err := s.Save(&core.Index{Tasks: []core.Task{mkTask("t-0001", "a", "ready", 100)}}); err != nil {
		t.Fatal(err)
	}
	// no ".tmp-*" leftovers in tasks/ or the root.
	for _, dir := range []string{s.root, filepath.Join(s.root, "tasks")} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if len(e.Name()) >= 4 && e.Name()[:4] == ".tmp" {
				t.Errorf("atomic write left a temp file: %s/%s", dir, e.Name())
			}
		}
	}
}

func TestBodyLazyAndExists(t *testing.T) {
	s := newStore(t)
	if got, _ := s.LoadBody("t-0001"); got != "" {
		t.Errorf("absent body should read empty, got %q", got)
	}
	if s.BodyExists("t-0001") {
		t.Error("absent body should not exist")
	}
	if err := s.SaveBody("t-0001", "# hello\n"); err != nil {
		t.Fatal(err)
	}
	if !s.BodyExists("t-0001") {
		t.Error("body should exist after save")
	}
	if got, _ := s.LoadBody("t-0001"); got != "# hello\n" {
		t.Errorf("body round-trip wrong: %q", got)
	}

	ids, err := s.ListBodyIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "t-0001" {
		t.Errorf("ListBodyIDs = %v", ids)
	}

	if err := s.DeleteBody("t-0001"); err != nil {
		t.Fatal(err)
	}
	if s.BodyExists("t-0001") {
		t.Error("body should be gone after delete")
	}
}

func TestNextIDRandom(t *testing.T) {
	// Production id shape: prefix + 5 Crockford-base32 chars, and no legacy
	// counter file. NextID is a *raw* random draw with no internal dedup, so
	// uniqueness of *persisted* ids is the app layer's job (App.uniqueID retries
	// on an in-store clash — see internal/app TestAddGeneratesUniqueRandomIDs /
	// TestAddManyGeneratesUniqueIDs), and the byte->alphabet mapping is pinned in
	// core.TestRandomIDSuffix. This test pins what the *store* owns.
	s := newStore(t)
	re := regexp.MustCompile(`^t-[0-9a-z]{5}$`)
	id, err := s.NextID()
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString(id) {
		t.Fatalf("id %q does not match the random id pattern", id)
	}
	// the legacy counter file is gone: NextID must not create .furrow/seq.
	if _, err := os.Stat(filepath.Join(s.root, "seq")); !os.IsNotExist(err) {
		t.Errorf("NextID must not create .furrow/seq (stat err = %v)", err)
	}

	// NextID is high-entropy and stateless: repeated draws are independent random
	// values, not a constant or a counter. We assert this with a strict
	// no-duplicate check — but only where that assertion is honest. A *raw* batch
	// can collide by the birthday bound: at the 5-char production width the space
	// is only 32^5 ≈ 3.4e7, so 64 draws collide with p ≈ 6e-5 — the CI flake this
	// de-flakes (t-09ca). A wide suffix makes the birthday collision
	// astronomically improbable (32^24 ≈ 1.3e36, so 1000 draws collide with
	// p ≈ 4e-31), so a duplicate at this width means NextID is genuinely broken
	// (constant/low-entropy), not unlucky.
	wide := New(filepath.Join(t.TempDir(), ".furrow"), lanes, "t-", 24)
	wideRe := regexp.MustCompile(`^t-[0-9a-z]{24}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := wide.NextID()
		if err != nil {
			t.Fatal(err)
		}
		if !wideRe.MatchString(id) {
			t.Fatalf("id %q does not match the random id pattern", id)
		}
		if seen[id] {
			t.Fatalf("NextID returned a duplicate within a batch: %q", id)
		}
		seen[id] = true
	}
}

func TestSaveAssetRoundTripAndDedup(t *testing.T) {
	s := newStore(t)
	assetPath := func(name string) string { return filepath.Join(s.root, "bodies", "assets", name) }
	img := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}

	name, err := s.SaveAsset("t-0001", "shot.png", img)
	if err != nil {
		t.Fatal(err)
	}
	if name != "t-0001-shot.png" {
		t.Fatalf("first asset name = %q, want t-0001-shot.png", name)
	}
	got, err := os.ReadFile(assetPath(name))
	if err != nil {
		t.Fatalf("asset not written: %v", err)
	}
	if !bytes.Equal(got, img) {
		t.Errorf("asset bytes round-trip wrong: %v", got)
	}

	// A second attach of the same source name must not overwrite: it gets a
	// numeric suffix, and the original survives.
	img2 := []byte("second")
	name2, err := s.SaveAsset("t-0001", "shot.png", img2)
	if err != nil {
		t.Fatal(err)
	}
	if name2 != "t-0001-shot-2.png" {
		t.Fatalf("second asset name = %q, want t-0001-shot-2.png", name2)
	}
	if got, _ := os.ReadFile(assetPath("t-0001-shot.png")); !bytes.Equal(got, img) {
		t.Error("first asset was clobbered by the second attach")
	}
	if got, _ := os.ReadFile(assetPath(name2)); !bytes.Equal(got, img2) {
		t.Error("second asset content wrong")
	}

	// The source name is sanitized for the on-disk file.
	spaced, err := s.SaveAsset("t-0002", "my clip.mp4", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if spaced != "t-0002-my-clip.mp4" {
		t.Errorf("sanitized name = %q, want t-0002-my-clip.mp4", spaced)
	}
}
