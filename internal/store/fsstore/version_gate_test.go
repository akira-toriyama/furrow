package fsstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

var gateLanes = []string{"inbox", "ready", "done"}

// A board whose meta.json declares a NEWER layout than this binary must refuse
// to Load and to Save (exit 3): a lenient unmarshal would silently drop fields
// the binary doesn't know, and a Save would write that loss back to disk.
func TestVersionGateRefusesNewerBoard(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)

	// A normal Save stamps the current version — loads fine.
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{
		{ID: "t-0001", Title: "x", Status: "ready", Priority: 100, Body: core.BodyPath("t-0001")},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("current-version board must load: %v", err)
	}

	// Hand-bump meta.json to a future version: both directions gate.
	meta := filepath.Join(root, "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": 99\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Load()
	if err == nil {
		t.Fatal("Load of a v99 board must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Load exit code = %d, want %d", got, core.CodeInternal)
	}
	if !strings.Contains(err.Error(), "update the binary") {
		t.Errorf("Load error should say the fix: %q", err.Error())
	}

	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion}); err == nil {
		t.Fatal("Save onto a v99 board must fail")
	} else if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Save exit code = %d, want %d", got, core.CodeInternal)
	}

	// The refused Save must not have touched the board: the shard is intact.
	if b, err := os.ReadFile(filepath.Join(root, "tasks", "t-0001.json")); err != nil || len(b) == 0 {
		t.Errorf("refused Save must leave shards intact: %v", err)
	}
}

// An OLDER (or absent) meta version still loads — forward compatibility is the
// store's normal lenient read; only the future direction is fatal.
func TestVersionGateAllowsOlderMeta(t *testing.T) {
	root := t.TempDir()
	s := New(root, gateLanes, "t-", 5)
	if err := s.Save(&core.Index{SchemaVersion: core.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meta.json"), []byte("{\n  \"schema_version\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := s.Load()
	if err != nil {
		t.Fatalf("older board must load: %v", err)
	}
	if idx.SchemaVersion != 1 {
		t.Errorf("loaded SchemaVersion = %d, want the board's 1", idx.SchemaVersion)
	}
}
