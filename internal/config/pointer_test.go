package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePointer(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".furrow-pointer.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPointer_BoardAndRepo(t *testing.T) {
	p, warn, err := LoadPointer(writePointer(t, "board = \"../projects/.furrow\"\ndefault_repo = \"me/chord\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if p.Board != "../projects/.furrow" {
		t.Errorf("Board = %q, want ../projects/.furrow", p.Board)
	}
	if p.DefaultRepo != "me/chord" {
		t.Errorf("DefaultRepo = %q, want me/chord", p.DefaultRepo)
	}
}

func TestLoadPointer_RepoAuto(t *testing.T) {
	p, _, err := LoadPointer(writePointer(t, "board = \"/abs/.furrow\"\ndefault_repo = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.DefaultRepo != "auto" {
		t.Errorf("DefaultRepo = %q, want auto", p.DefaultRepo)
	}
}

func TestLoadPointer_BoardOnly(t *testing.T) {
	p, warn, err := LoadPointer(writePointer(t, "board = \"/abs/.furrow\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty", p.DefaultRepo)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
}

// The retired default_label key is a tombstone: ignored (never a repo, never a
// tag), but warned about — silently dropping it would silently un-scope the repo.
func TestLoadPointer_RetiredDefaultLabelWarns(t *testing.T) {
	p, warn, err := LoadPointer(writePointer(t, "board = \"/abs/.furrow\"\ndefault_label = \"chord\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty (default_label must not leak into it)", p.DefaultRepo)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "default_repo") {
		t.Errorf("warn = %v, want one tombstone warning pointing at default_repo", warn)
	}
}

func TestLoadPointer_MissingBoardErrors(t *testing.T) {
	if _, _, err := LoadPointer(writePointer(t, "default_repo = \"me/chord\"\n")); err == nil {
		t.Fatal("expected error for missing board, got nil")
	}
}

func TestLoadPointer_MalformedErrors(t *testing.T) {
	if _, _, err := LoadPointer(writePointer(t, "board = \"x\" this = is = broken\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}
