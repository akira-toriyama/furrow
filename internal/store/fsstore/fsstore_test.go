package fsstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

var lanes = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), ".furrow"), lanes, "t-", 4)
}

func TestLoadMissingIndexIsEmpty(t *testing.T) {
	s := newStore(t)
	idx, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if idx.SchemaVersion != core.SchemaVersion || len(idx.Tasks) != 0 {
		t.Errorf("fresh store should load an empty index, got %+v", idx)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	idx := &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{
		{ID: "t-0001", Title: "畝", Status: "ready", Priority: 100, Created: now, Updated: now, Body: core.BodyPath("t-0001")},
	}}
	if err := s.Save(idx); err != nil {
		t.Fatal(err)
	}

	// the file on disk equals the marshaller's bytes (no surprises).
	raw, err := os.ReadFile(s.indexPath())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := core.Marshal(idx, lanes)
	if string(raw) != string(want) {
		t.Errorf("on-disk index != marshalled bytes\n--- disk ---\n%s\n--- want ---\n%s", raw, want)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Title != "畝" {
		t.Errorf("round-trip lost data: %+v", got)
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	s := newStore(t)
	if err := s.Save(&core.Index{SchemaVersion: 1, Tasks: []core.Task{}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.root)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == "" && len(e.Name()) > 0 && e.Name()[0] == '.' && e.Name() != ".furrow" {
			// any leftover ".tmp-*" would fail us
			if len(e.Name()) >= 4 && e.Name()[:4] == ".tmp" {
				t.Errorf("atomic write left a temp file: %s", e.Name())
			}
		}
	}
}

func TestBodyLazyAndExists(t *testing.T) {
	s := newStore(t)
	// absent body reads as "" and does not exist.
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

func TestNextIDMonotonic(t *testing.T) {
	s := newStore(t)
	var got []string
	for i := 0; i < 3; i++ {
		id, err := s.NextID()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	want := []string{"t-0001", "t-0002", "t-0003"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("NextID[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// a fresh Store over the same root continues the sequence (seq persisted).
	s2 := New(s.root, lanes, "t-", 4)
	id, _ := s2.NextID()
	if id != "t-0004" {
		t.Errorf("seq did not persist across stores: got %q, want t-0004", id)
	}

	// BumpSeqTo advances but never rewinds.
	if err := s2.BumpSeqTo(100); err != nil {
		t.Fatal(err)
	}
	if id, _ := s2.NextID(); id != "t-0101" {
		t.Errorf("after BumpSeqTo(100), NextID = %q, want t-0101", id)
	}
	if err := s2.BumpSeqTo(5); err != nil { // lower -> no-op
		t.Fatal(err)
	}
	if id, _ := s2.NextID(); id != "t-0102" {
		t.Errorf("BumpSeqTo with a lower value should not rewind: got %q", id)
	}
}
