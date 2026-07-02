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
