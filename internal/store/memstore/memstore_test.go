package memstore

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// memstore mirrors fsstore's version gate so app/cli tests can exercise the
// "board newer than binary" path without a disk.
func TestVersionGate(t *testing.T) {
	s := New("t-", 5)

	if _, err := s.Load(); err != nil {
		t.Fatalf("default store must load: %v", err)
	}

	s.SetSchemaVersion(core.SchemaVersion + 1)
	_, err := s.Load()
	if err == nil {
		t.Fatal("Load of a newer board must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("Load exit code = %d, want %d", got, core.CodeInternal)
	}
	if !strings.Contains(err.Error(), "update the binary") {
		t.Errorf("error should say the fix: %q", err.Error())
	}
	if err := s.Save(&core.Index{}); err == nil {
		t.Fatal("Save onto a newer board must fail")
	}

	s.SetSchemaVersion(core.SchemaVersion)
	if _, err := s.Load(); err != nil {
		t.Fatalf("restored version must load again: %v", err)
	}
}

func TestSaveAssetDedup(t *testing.T) {
	s := New("t-", 5)

	name, err := s.SaveAsset("t-0001", "shot.png", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if name != "t-0001-shot.png" {
		t.Fatalf("first name = %q, want t-0001-shot.png", name)
	}
	name2, err := s.SaveAsset("t-0001", "shot.png", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if name2 != "t-0001-shot-2.png" {
		t.Fatalf("second name = %q, want t-0001-shot-2.png", name2)
	}
}
