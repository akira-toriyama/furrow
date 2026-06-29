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

func TestDiscover_NearestPointerBeatsAncestorFurrow(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	if _, err := Init(root); err != nil { // root/.furrow — an ANCESTOR real store
		t.Fatal(err)
	}
	central := filepath.Join(root, "central")
	if _, err := Init(central); err != nil { // the board the pointer redirects to
		t.Fatal(err)
	}
	ptrDir := filepath.Join(root, "sub")
	if err := os.MkdirAll(ptrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_label = \"near\"\n"
	if err := os.WriteFile(filepath.Join(ptrDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(ptrDir, "deep")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// Walking up from child, the pointer at root/sub is nearer than root/.furrow,
	// so it must win (nearest wins).
	a, err := Open(child)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(central, DirName) {
		t.Errorf("Dir = %q, want the central board (nearest pointer should beat ancestor .furrow)", a.Dir)
	}
	if a.DefaultLabel != "near" {
		t.Errorf("DefaultLabel = %q, want near", a.DefaultLabel)
	}
}

func TestAdd_InjectedLabelSatisfiesRequired(t *testing.T) {
	repoDir, boardDir := pointerLayout(t, "chord")
	// Turn on [labels].required on the central board.
	if err := os.WriteFile(filepath.Join(boardDir, "config.toml"), []byte("[labels]\nrequired = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Cfg.LabelsRequired {
		t.Fatal("precondition: labels.required should be on")
	}
	task, err := a.Add("x", AddOpts{}) // no explicit label — must pass via the injected one
	if err != nil {
		t.Fatalf("Add should succeed via injected default_label: %v", err)
	}
	if !hasLabel(task.Labels, "chord") {
		t.Errorf("labels = %v, want chord", task.Labels)
	}
}

func TestDiscover_PointerTildeUserRejected(t *testing.T) {
	t.Setenv(EnvDir, "")
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte("board = \"~bob/.furrow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repoDir); err == nil {
		t.Fatal("expected ~user board to be rejected, got nil")
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

func hasLabel(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestAdd_InjectsDefaultLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !hasLabel(task.Labels, "chord") {
		t.Errorf("labels = %v, want to contain chord", task.Labels)
	}
}

func TestAdd_UnionsWithExplicitLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !hasLabel(task.Labels, "chord") || !hasLabel(task.Labels, "bug") {
		t.Errorf("labels = %v, want both chord and bug", task.Labels)
	}
}

func TestAddMany_InjectsDefaultLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.AddMany([]AddSpec{{Title: "x"}, {Title: "y"}})
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	for _, task := range created {
		if !hasLabel(task.Labels, "chord") {
			t.Errorf("%s labels = %v, want to contain chord", task.ID, task.Labels)
		}
	}
}
