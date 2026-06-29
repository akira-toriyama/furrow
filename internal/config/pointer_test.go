package config

import (
	"os"
	"path/filepath"
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

func TestLoadPointer_BoardAndLabel(t *testing.T) {
	p, err := LoadPointer(writePointer(t, "board = \"../projects/.furrow\"\ndefault_label = \"chord\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.Board != "../projects/.furrow" {
		t.Errorf("Board = %q, want ../projects/.furrow", p.Board)
	}
	if p.DefaultLabel != "chord" {
		t.Errorf("DefaultLabel = %q, want chord", p.DefaultLabel)
	}
}

func TestLoadPointer_BoardOnly(t *testing.T) {
	p, err := LoadPointer(writePointer(t, "board = \"/abs/.furrow\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", p.DefaultLabel)
	}
}

func TestLoadPointer_MissingBoardErrors(t *testing.T) {
	if _, err := LoadPointer(writePointer(t, "default_label = \"chord\"\n")); err == nil {
		t.Fatal("expected error for missing board, got nil")
	}
}

func TestLoadPointer_MalformedErrors(t *testing.T) {
	if _, err := LoadPointer(writePointer(t, "board = \"x\" this = is = broken\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}
