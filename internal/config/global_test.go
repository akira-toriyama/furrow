package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGlobal(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadGlobalBoard_MissingFileIsNoOp(t *testing.T) {
	gb, warn, err := LoadGlobalBoard(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("LoadGlobalBoard: %v", err)
	}
	if gb != nil {
		t.Errorf("gb = %+v, want nil for a missing file", gb)
	}
	if warn != nil {
		t.Errorf("warn = %v, want nil", warn)
	}
}

func TestLoadGlobalBoard_FullParse(t *testing.T) {
	gb, warn, err := LoadGlobalBoard(writeGlobal(t,
		"[board]\npath = \"/ws/org/projects/.furrow\"\nscope = \"/ws/org\"\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoard: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if gb == nil {
		t.Fatal("gb = nil, want populated")
	}
	if gb.Path != "/ws/org/projects/.furrow" || gb.Scope != "/ws/org" || gb.Label != "auto" {
		t.Errorf("gb = %+v, want path/scope/label set", gb)
	}
}

func TestLoadGlobalBoard_EmptyPathClampsWithWarn(t *testing.T) {
	gb, warn, err := LoadGlobalBoard(writeGlobal(t, "[board]\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoard: %v", err)
	}
	if gb != nil {
		t.Errorf("gb = %+v, want nil when [board].path is absent", gb)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the missing path, got none")
	}
}

func TestLoadGlobalBoard_MalformedErrors(t *testing.T) {
	if _, _, err := LoadGlobalBoard(writeGlobal(t, "[board]\npath = broken = toml\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}
