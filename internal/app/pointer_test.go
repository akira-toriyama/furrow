package app

import (
	"os"
	"path/filepath"
	"testing"
)

// pointerLayout builds tmp/central/.furrow (a real store) and a sibling repo dir
// holding a .furrow-pointer.toml; it returns the repo dir to Open from.
func pointerLayout(t *testing.T, label string) (repoDir, boardDir string) {
	t.Helper()
	t.Setenv(EnvDir, "") // ensure FURROW_DIR does not override discovery
	root := t.TempDir()
	central := filepath.Join(root, "central")
	if _, err := Init(central); err != nil {
		t.Fatal(err)
	}
	boardDir = filepath.Join(central, DirName)
	repoDir = filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\n"
	if label != "" {
		body += "default_label = \"" + label + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir, boardDir
}

func TestDiscover_PointerRedirectsAndScopes(t *testing.T) {
	repoDir, boardDir := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != boardDir {
		t.Errorf("Dir = %q, want %q", a.Dir, boardDir)
	}
	if a.DefaultLabel != "chord" {
		t.Errorf("DefaultLabel = %q, want chord", a.DefaultLabel)
	}
}

func TestDiscover_PointerBoardOnlyNoLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", a.DefaultLabel)
	}
}

func TestDiscover_LocalFurrowBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	// Give the repo dir its OWN .furrow; it must win over the pointer.
	if _, err := Init(repoDir); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(repoDir, DirName) {
		t.Errorf("Dir = %q, want local .furrow", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (local store, no pointer)", a.DefaultLabel)
	}
}

func TestDiscover_FurrowDirBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	other := t.TempDir()
	if _, err := Init(other); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, filepath.Join(other, DirName))
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(other, DirName) {
		t.Errorf("Dir = %q, want FURROW_DIR store", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (FURROW_DIR injects no label)", a.DefaultLabel)
	}
}

func TestDiscover_PointerBadBoardErrors(t *testing.T) {
	t.Setenv(EnvDir, "")
	repoDir := t.TempDir()
	body := "board = \"./nope/.furrow\"\n"
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repoDir); err == nil {
		t.Fatal("expected error for non-existent board, got nil")
	}
}
