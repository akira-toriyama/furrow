package fsstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// PruneMetaExtras drops exactly the parked keys, keeps the declared version
// byte-recipe-clean, no-ops on a clean meta, and — being an ordinary write —
// obeys the version gate: an outdated board keeps its parked keys too.
func TestPruneMetaExtras(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, []string{"inbox", "done"}, "t-", "e-", 5)
	if err := s.SetBoardVersion(core.SchemaVersion); err != nil {
		t.Fatal(err)
	}

	if keys, err := s.PruneMetaExtras(); err != nil || keys != nil {
		t.Fatalf("clean prune = %v, %v; want nil, nil", keys, err)
	}

	// Plant a forward-compatible key the way it really arrives: on disk.
	p := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(p, []byte("{\n  \"schema_version\": "+jsonInt(core.SchemaVersion)+",\n  \"flux\": true\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := s.PruneMetaExtras()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "flux" {
		t.Fatalf("pruned keys = %v, want [flux]", keys)
	}
	m, err := s.LoadMeta()
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != core.SchemaVersion || len(m.ExtraKeys()) != 0 {
		t.Fatalf("after prune: version %d extras %v", m.SchemaVersion, m.ExtraKeys())
	}

	old := t.TempDir()
	s2 := New(old, []string{"inbox", "done"}, "t-", "e-", 5)
	if err := os.WriteFile(filepath.Join(old, "meta.json"), []byte("{\n  \"schema_version\": "+jsonInt(core.SchemaVersion-1)+",\n  \"flux\": true\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.PruneMetaExtras(); err == nil {
		t.Fatal("prune on an outdated board must hit the write gate")
	} else if fe, ok := err.(*core.Error); !ok || fe.Kind != core.KindSchemaUpgradeRequired {
		t.Fatalf("gate error = %v, want schema-upgrade-required", err)
	}
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
