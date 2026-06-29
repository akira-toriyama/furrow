package app

import (
	"os"
	"path/filepath"
	"testing"
)

// globalLayout builds tmp/org/projects/.furrow (the central board) and writes a
// user-level furrow config.toml (under an isolated XDG_CONFIG_HOME) whose
// [board] points at it with scope = tmp/org. It returns the scope dir and the
// board dir; callers create repos under scope to Open from.
func globalLayout(t *testing.T, label string) (scope, board string) {
	t.Helper()
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope = filepath.Join(root, "org")
	central := filepath.Join(scope, "projects")
	if _, err := Init(central); err != nil {
		t.Fatal(err)
	}
	board = filepath.Join(central, DirName)
	writeGlobalConfig(t, "[board]\npath = \""+board+"\"\nscope = \""+scope+"\"\nlabel = \""+label+"\"\n")
	return scope, board
}

// writeGlobalConfig points XDG_CONFIG_HOME at a temp dir and writes the given
// furrow config.toml there (so tests never read the developer's real one).
func writeGlobalConfig(t *testing.T, body string) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	fdir := filepath.Join(cfgDir, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGlobal_ActivatesUnderScopeWithAutoLabel(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board {
		t.Errorf("Dir = %q, want board %q", a.Dir, board)
	}
	if a.DefaultLabel != "repoX" {
		t.Errorf("DefaultLabel = %q, want repoX", a.DefaultLabel)
	}
}

func TestGlobal_AutoLabelFromNestedSubdir(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	mkGitRepo(t, filepath.Join(scope, "repoX"))
	deep := filepath.Join(scope, "repoX", "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(deep)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board %q and repoX", a.Dir, a.DefaultLabel, board)
	}
}

func TestGlobal_InertOutsideScope(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	outside := mkGitRepo(t, filepath.Join(filepath.Dir(scope), "elsewhere"))
	if _, err := Open(outside); err == nil {
		t.Fatal("expected the usual not-found error outside scope, got nil")
	}
}

func TestGlobal_LocalFurrowBeatsGlobal(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	if _, err := Init(repo); err != nil { // its own store must win
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(repo, DirName) {
		t.Errorf("Dir = %q, want local .furrow", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (local store)", a.DefaultLabel)
	}
}

func TestGlobal_PointerBeatsGlobal(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	// a pointer in the repo redirects to the same central board with its own label
	body := "board = \"projects/.furrow\"\ndefault_label = \"ptr\"\n"
	if err := os.WriteFile(filepath.Join(repo, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// board = projects/.furrow relative to the pointer's dir (scope/repoX) does not
	// exist; make a board there so the pointer resolves.
	if _, err := Init(filepath.Join(repo, "projects")); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultLabel != "ptr" {
		t.Errorf("DefaultLabel = %q, want ptr (pointer beats global board)", a.DefaultLabel)
	}
}

func TestGlobal_FurrowDirBeatsGlobal(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	other := t.TempDir()
	if _, err := Init(other); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, filepath.Join(other, DirName))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(other, DirName) {
		t.Errorf("Dir = %q, want FURROW_DIR store", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", a.DefaultLabel)
	}
}

func TestGlobal_NoGitRepoNoLabelPlusWarn(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	plain := filepath.Join(scope, "plain") // under scope, but no .git anywhere
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(plain)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board {
		t.Errorf("Dir = %q, want board (activates even without a git repo)", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", a.DefaultLabel)
	}
	if len(a.ScopeWarnings) == 0 {
		t.Error("want a stderr-bound warning about the missing git repo, got none")
	}
}

func TestGlobal_ScopeDefaultsToBoardRepoParent(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	if _, err := Init(filepath.Join(scope, "projects")); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(scope, "projects", DirName)
	// no scope key -> derive from the board's repo parent (= scope)
	writeGlobalConfig(t, "[board]\npath = \""+board+"\"\nlabel = \"auto\"\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want %q and repoX", a.Dir, a.DefaultLabel, board)
	}
}

func TestGlobal_EnvBoardActivates(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	if _, err := Init(filepath.Join(scope, "projects")); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(scope, "projects", DirName)
	writeGlobalConfig(t, "") // no [board] in the file; FURROW_BOARD drives it
	t.Setenv(EnvBoard, board)
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want %q and repoX", a.Dir, a.DefaultLabel, board)
	}
}

func TestGlobal_BadBoardPathUnderScopeErrors(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, "[board]\npath = \""+filepath.Join(scope, "nope", ".furrow")+"\"\nscope = \""+scope+"\"\nlabel = \"auto\"\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	if _, err := Open(repo); err == nil {
		t.Fatal("expected a loud error for a non-existent board under scope, got nil")
	}
}

func TestGlobal_AddInjectsDerivedLabel(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !hasLabel(task.Labels, "repoX") {
		t.Errorf("labels = %v, want repoX", task.Labels)
	}
}

func TestGlobal_RequiredNoGitFailsLoud(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	if err := os.WriteFile(filepath.Join(board, "config.toml"), []byte("[labels]\nrequired = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(scope, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("x", AddOpts{}); err == nil {
		t.Fatal("expected a required-label error with no derivable label, got nil")
	}
}

func TestGlobal_GitFileDetected(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	wt := filepath.Join(scope, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// a worktree/submodule uses a .git FILE, not a directory
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(wt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "wt" {
		t.Errorf("Dir=%q label=%q, want board and wt", a.Dir, a.DefaultLabel)
	}
}
