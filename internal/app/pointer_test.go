package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pointerLayout builds tmp/central/.furrow (a real store) and a sibling repo dir
// holding a .furrow-pointer.toml with the given default_repo ("" = redirect
// only); it returns the repo dir to Open from.
func pointerLayout(t *testing.T, repo string) (repoDir, boardDir string) {
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
	if repo != "" {
		body += "default_repo = \"" + repo + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir, boardDir
}

func TestDiscover_PointerRedirectsAndScopes(t *testing.T) {
	repoDir, boardDir := pointerLayout(t, "me/chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != boardDir {
		t.Errorf("Dir = %q, want %q", a.Dir, boardDir)
	}
	if a.DefaultRepo != "me/chord" {
		t.Errorf("DefaultRepo = %q, want me/chord", a.DefaultRepo)
	}
	if len(a.BoardRepos) != 1 || a.BoardRepos[0] != "me/chord" {
		t.Errorf("BoardRepos = %v, want [me/chord]", a.BoardRepos)
	}
}

func TestDiscover_PointerBoardOnlyNoRepo(t *testing.T) {
	repoDir, _ := pointerLayout(t, "")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty", a.DefaultRepo)
	}
}

// default_repo = "auto" derives from the pointer repo's checkout, exactly like
// a central board's repo = "auto".
func TestDiscover_PointerDefaultRepoAuto(t *testing.T) {
	repoDir, _ := pointerLayout(t, "auto")
	mkGitRepoWithOrigin(t, repoDir, "git@github.com:me/ptr.git")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "me/ptr" {
		t.Errorf("DefaultRepo = %q, want me/ptr (derived)", a.DefaultRepo)
	}
}

// The retired default_label key is ignored with a tombstone warning — it never
// becomes a repo or a label.
func TestDiscover_PointerRetiredDefaultLabelWarns(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	central := filepath.Join(root, "central")
	if _, err := Init(central); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_label = \"chord\"\n"
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" || a.DefaultLabel != "" {
		t.Errorf("repo=%q label=%q, want both empty (default_label is retired)", a.DefaultRepo, a.DefaultLabel)
	}
	found := false
	for _, w := range a.ScopeWarnings {
		if strings.Contains(w, "default_repo") {
			found = true
		}
	}
	if !found {
		t.Errorf("ScopeWarnings = %v, want the default_label tombstone", a.ScopeWarnings)
	}
}

func TestDiscover_LocalFurrowBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "me/chord")
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
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty (local store, no pointer)", a.DefaultRepo)
	}
}

func TestDiscover_FurrowDirBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "me/chord")
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
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty (FURROW_DIR injects no scope)", a.DefaultRepo)
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
	body := "board = \"../central/.furrow\"\ndefault_repo = \"me/near\"\n"
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
	if a.DefaultRepo != "me/near" {
		t.Errorf("DefaultRepo = %q, want me/near", a.DefaultRepo)
	}
}

// The pointer's default_repo unions into repos on add — a repo, not a label, so
// [labels].required is NOT satisfied by it (labels stayed a pure-tag concern).
func TestAdd_PointerRepoDoesNotSatisfyLabelsRequired(t *testing.T) {
	repoDir, boardDir := pointerLayout(t, "me/chord")
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
	if _, err := a.Add("x", AddOpts{}); err == nil {
		t.Fatal("expected a required-label error (default_repo is not a label), got nil")
	}
	task, err := a.Add("x", AddOpts{Labels: []string{"tagged"}})
	if err != nil {
		t.Fatalf("Add with an explicit label: %v", err)
	}
	if !contains(task.Repos, "me/chord") {
		t.Errorf("repos = %v, want me/chord unioned", task.Repos)
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

func TestAdd_PointerUnionsDefaultRepo(t *testing.T) {
	repoDir, _ := pointerLayout(t, "me/chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains(task.Repos, "me/chord") {
		t.Errorf("repos = %v, want to contain me/chord", task.Repos)
	}
}

func TestAdd_PointerUnionsWithExplicitRepo(t *testing.T) {
	repoDir, _ := pointerLayout(t, "me/chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{Repos: []string{"other/repo"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains(task.Repos, "me/chord") || !contains(task.Repos, "other/repo") {
		t.Errorf("repos = %v, want both me/chord and other/repo", task.Repos)
	}
}

func TestAddMany_PointerUnionsDefaultRepo(t *testing.T) {
	repoDir, _ := pointerLayout(t, "me/chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.AddMany([]AddSpec{{Title: "x"}, {Title: "y"}})
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	for _, task := range created {
		if !contains(task.Repos, "me/chord") {
			t.Errorf("%s repos = %v, want to contain me/chord", task.ID, task.Repos)
		}
	}
}
