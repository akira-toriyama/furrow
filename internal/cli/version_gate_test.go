package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// End-to-end version gate: `furrow ls` against a board whose meta.json declares
// a future layout exits 3 (internal — the fix is a newer binary, not new args).
func TestLsRefusesNewerBoard(t *testing.T) {
	initStore(t)
	meta := filepath.Join(os.Getenv(app.EnvDir), "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": 99\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, code := run(t, "ls")
	if code != int(core.CodeInternal) {
		t.Errorf("ls on a v99 board: exit = %d, want %d", code, core.CodeInternal)
	}

	// Mutations are gated the same way (Load happens before any write).
	if _, code := run(t, "add", "nope"); code != int(core.CodeInternal) {
		t.Errorf("add on a v99 board: exit = %d, want %d", code, core.CodeInternal)
	}
}
