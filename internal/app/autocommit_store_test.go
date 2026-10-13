package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

// autocommit is keyed by the STORE, not by the discovery arm: the [[board]]
// entry naming the opened path supplies it whether the walk (a cwd inside the
// board's own tree), a pointer, FURROW_DIR, or the entry's own scope reached
// the store. Before applyStoreAutoCommit only the user-config arm carried the
// opt-in, so the documented standalone recipe committed from the repo root and
// silently did not from inside claude_workspace/ (t-19gf).

// storeEntry renders one [[board]] table naming store with the given scope and
// autocommit value.
func storeEntry(store, scope string, autocommit bool) string {
	return testutil.BoardEntry(store, "", scope) + "autocommit = " + map[bool]string{true: "true", false: "false"}[autocommit] + "\n"
}

// autoCommitLayout builds root/org/projects/.furrow with a user config whose one
// [[board]] names it (scope root/org) with autocommit on, and returns the scope,
// the board's parent (the tree the walk finds it from) and the store.
func autoCommitLayout(t *testing.T) (scope, central, store string) {
	t.Helper()
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	scope = filepath.Join(t.TempDir(), "org")
	central = filepath.Join(scope, "projects")
	store = mustInitBoard(t, central)
	testutil.UserConfig(t, storeEntry(store, scope, true))
	return scope, central, store
}

func TestAutoCommitFollowsStore_LocalArm(t *testing.T) {
	_, central, store := autoCommitLayout(t)
	inside := filepath.Join(central, "notes")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(inside)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != SourceLocal || a.Dir != store {
		t.Fatalf("Source=%q Dir=%q, want the walk to find %q (local)", a.Source, a.Dir, store)
	}
	if !a.AutoCommit {
		t.Errorf("AutoCommit = false from inside the board's own tree; the [[board]] entry names this store, so it must be on")
	}
	if len(a.ScopeWarnings) != 0 {
		t.Errorf("unexpected warnings: %q", a.ScopeWarnings)
	}
}

func TestAutoCommitFollowsStore_PointerArm(t *testing.T) {
	scope, _, store := autoCommitLayout(t)
	repo := filepath.Join(scope, "repoX")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, PointerName), []byte("board = \"../projects/.furrow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != SourcePointer || a.Dir != store {
		t.Fatalf("Source=%q Dir=%q, want pointer -> %q", a.Source, a.Dir, store)
	}
	if !a.AutoCommit {
		t.Errorf("AutoCommit = false via a pointer; the [[board]] entry names this store, so it must be on")
	}
}

func TestAutoCommitFollowsStore_FurrowDirArm(t *testing.T) {
	_, _, store := autoCommitLayout(t)
	t.Setenv(EnvDir, store)
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != SourceEnv || a.Dir != store {
		t.Fatalf("Source=%q Dir=%q, want FURROW_DIR -> %q", a.Source, a.Dir, store)
	}
	if !a.AutoCommit {
		t.Errorf("AutoCommit = false via FURROW_DIR; the [[board]] entry names this store, so it must be on")
	}
}

// A store the user config does not name stays off whatever arm found it — the
// default has not moved, only the key.
func TestAutoCommitFollowsStore_UnnamedStoreStaysOff(t *testing.T) {
	scope, _, _ := autoCommitLayout(t)
	other := mustInitBoard(t, filepath.Join(filepath.Dir(scope), "elsewhere"))
	a, err := Open(filepath.Dir(other))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != SourceLocal || a.Dir != other {
		t.Fatalf("Source=%q Dir=%q, want local %q", a.Source, a.Dir, other)
	}
	if a.AutoCommit {
		t.Errorf("AutoCommit = true for a store no [[board]] entry names")
	}
}

// Two entries naming one store: the entry whose scope resolved the command
// keeps its say; any other arm takes the first in file order (pickBoard's tie
// rule), so the answer never depends on which scope happens to be nearer.
func TestAutoCommitFollowsStore_WinnerThenFileOrder(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	central := filepath.Join(root, "boards", "central")
	store := mustInitBoard(t, central)
	scopeA, scopeB := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{filepath.Join(scopeA, "x"), filepath.Join(scopeB, "x"), filepath.Join(central, "x")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testutil.UserConfig(t, storeEntry(store, scopeA, false)+storeEntry(store, scopeB, true))

	a, err := Open(filepath.Join(scopeB, "x"))
	if err != nil {
		t.Fatalf("Open under scope B: %v", err)
	}
	if a.Source != SourceUserConfig || !a.AutoCommit {
		t.Errorf("under scope B: Source=%q AutoCommit=%v, want user-config winner (second entry) on", a.Source, a.AutoCommit)
	}

	a, err = Open(filepath.Join(central, "x"))
	if err != nil {
		t.Fatalf("Open inside the store's tree: %v", err)
	}
	if a.Source != SourceLocal || a.AutoCommit {
		t.Errorf("inside the store's tree: Source=%q AutoCommit=%v, want local and the FIRST entry in file order (off)", a.Source, a.AutoCommit)
	}
}

// A user config that cannot be parsed never breaks a board the walk found
// (it opened without that file before autocommit consulted it): autocommit
// stays off and one stderr note says so, instead of silence.
func TestAutoCommitFollowsStore_UnreadableConfigWarnsAndStaysOff(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	central := filepath.Join(t.TempDir(), "projects")
	store := mustInitBoard(t, central)
	testutil.UserConfig(t, "[[board]\npath = \"broken\n")
	a, err := Open(central)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != store || a.AutoCommit {
		t.Fatalf("Dir=%q AutoCommit=%v, want the local store with autocommit off", a.Dir, a.AutoCommit)
	}
	if !strings.Contains(strings.Join(a.ScopeWarnings, "\n"), "autocommit stays off") {
		t.Errorf("want a stderr note that autocommit stays off, got %q", a.ScopeWarnings)
	}
}
