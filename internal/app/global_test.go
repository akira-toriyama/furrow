package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// globalLayout builds tmp/org/projects/.furrow (the central board) and writes a
// user-level furrow config.toml (under an isolated XDG_CONFIG_HOME) whose single
// [[board]] points at it with scopes = [tmp/org]. It returns the scope dir and
// the board dir; callers create repos under scope to Open from.
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
	writeGlobalConfig(t, boardEntry(board, label, scope))
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

// boardEntry renders one [[board]] table for a config file.
func boardEntry(path, label string, scopes ...string) string {
	q := make([]string, len(scopes))
	for i, s := range scopes {
		q[i] = "\"" + s + "\""
	}
	return "[[board]]\npath = \"" + path + "\"\nscopes = [" + strings.Join(q, ", ") + "]\nlabel = \"" + label + "\"\n"
}

// mustInitBoard inits a fresh central board at dir and returns its .furrow path.
func mustInitBoard(t *testing.T, dir string) string {
	t.Helper()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, DirName)
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

// A config [[board]] with no scopes is dropped by the clamp, so it never
// activates — the convenience "derive scope from the board's repo parent" now
// belongs only to FURROW_BOARD (see TestGlobal_EnvBoardActivates).
func TestGlobal_ConfigBoardMissingScopesIsInert(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	// [[board]] with a path but no scopes -> clamped away.
	writeGlobalConfig(t, "[[board]]\npath = \""+board+"\"\nlabel = \"auto\"\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	_, err := Open(repo)
	if err == nil {
		t.Fatal("expected the usual not-found error (scope-less board is inert), got nil")
	}
	if !strings.Contains(err.Error(), "furrow init") {
		t.Errorf("err = %v, want the usual run-`furrow init` error (inert == behaves as without a board)", err)
	}
}

func TestGlobal_EnvBoardActivates(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, "") // no [[board]] in the file; FURROW_BOARD drives it
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
	writeGlobalConfig(t, boardEntry(filepath.Join(scope, "nope", ".furrow"), "auto", scope))
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	_, err := Open(repo)
	if err == nil {
		t.Fatal("expected a loud error for a non-existent board under scope, got nil")
	}
	if !strings.Contains(err.Error(), "not an existing directory") {
		t.Errorf("err = %v, want the loud 'not an existing directory' validation", err)
	}
}

// Of several boards whose scopes all enclose the cwd, the most specific (longest
// canonical scope) wins — independent of the order they appear in the file.
func TestGlobal_LongestScopeWins(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	inner := filepath.Join(org, "inner")
	outerBoard := mustInitBoard(t, filepath.Join(root, "central-outer"))
	innerBoard := mustInitBoard(t, filepath.Join(root, "central-inner"))
	// outer listed FIRST: first-match would pick it, longest-match must pick inner.
	writeGlobalConfig(t, boardEntry(outerBoard, "outer", org)+boardEntry(innerBoard, "inner", inner))
	repo := mkGitRepo(t, filepath.Join(inner, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != innerBoard {
		t.Errorf("Dir = %q, want inner board %q (longest scope wins)", a.Dir, innerBoard)
	}
}

func TestGlobal_LongestScopeWins_ReversedOrder(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	inner := filepath.Join(org, "inner")
	outerBoard := mustInitBoard(t, filepath.Join(root, "central-outer"))
	innerBoard := mustInitBoard(t, filepath.Join(root, "central-inner"))
	// inner listed FIRST this time; longest-match must still pick inner.
	writeGlobalConfig(t, boardEntry(innerBoard, "inner", inner)+boardEntry(outerBoard, "outer", org))
	repo := mkGitRepo(t, filepath.Join(inner, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != innerBoard {
		t.Errorf("Dir = %q, want inner board %q (longest scope wins)", a.Dir, innerBoard)
	}
}

// When two boards' scopes match equally specifically, the first in file order
// wins (deterministic tie-break).
func TestGlobal_EqualScopeTieBreaksToFileOrder(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	boardA := mustInitBoard(t, filepath.Join(root, "central-a"))
	boardB := mustInitBoard(t, filepath.Join(root, "central-b"))
	writeGlobalConfig(t, boardEntry(boardA, "a", org)+boardEntry(boardB, "b", org))
	repo := mkGitRepo(t, filepath.Join(org, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != boardA {
		t.Errorf("Dir = %q, want first board %q on an equal-scope tie", a.Dir, boardA)
	}
}

// One board may carry several scopes; the cwd matching any of them activates it.
func TestGlobal_MultipleScopesOnOneBoard(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	orgA := filepath.Join(root, "orgA")
	orgB := filepath.Join(root, "orgB")
	board := mustInitBoard(t, filepath.Join(root, "central"))
	writeGlobalConfig(t, boardEntry(board, "auto", orgA, orgB))
	repo := mkGitRepo(t, filepath.Join(orgB, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board %q and repoX", a.Dir, a.DefaultLabel, board)
	}
}

// A board with a broken path whose scope does NOT enclose the cwd is never
// stat'd, so it cannot break Open in an unrelated scope (only the winner is
// stat'd, after the scope gate).
func TestGlobal_BrokenBoardOutOfScopeIsIgnored(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	orgGood := filepath.Join(root, "good")
	orgBad := filepath.Join(root, "bad")
	good := mustInitBoard(t, filepath.Join(root, "central-good"))
	broken := filepath.Join(root, "central-bad", "nope", ".furrow") // never created
	writeGlobalConfig(t, boardEntry(broken, "auto", orgBad)+boardEntry(good, "auto", orgGood))
	repo := mkGitRepo(t, filepath.Join(orgGood, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a broken board in an unrelated scope must not break Open: %v", err)
	}
	if a.Dir != good {
		t.Errorf("Dir = %q, want the good board %q", a.Dir, good)
	}
}

// A sibling [[board]] whose path uses the unsupported ~user form must be dropped
// (clamp-don't-reject), never abort the whole scan — otherwise one half-written
// entry would break furrow in every directory on the machine.
func TestGlobal_BadSiblingBoardDoesNotBreakUnrelatedRepo(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	orgGood := filepath.Join(root, "good")
	good := mustInitBoard(t, filepath.Join(root, "central-good"))
	// A sibling board with a ~user path (unsupported) and an unrelated scope.
	bad := boardEntry("~bob/projects/.furrow", "auto", filepath.Join(root, "bad"))
	writeGlobalConfig(t, bad+boardEntry(good, "auto", orgGood))
	repo := mkGitRepo(t, filepath.Join(orgGood, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a ~user sibling board must not break an unrelated repo: %v", err)
	}
	if a.Dir != good {
		t.Errorf("Dir = %q, want the good board %q", a.Dir, good)
	}
}

// A single bad scope (~user form) is skipped, not fatal: another scope on the
// same board can still match.
func TestGlobal_BadScopeIsSkippedGoodScopeStillMatches(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(root, "central"))
	writeGlobalConfig(t, boardEntry(board, "auto", "~bob/nope", org))
	repo := mkGitRepo(t, filepath.Join(org, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a bad scope must be skipped, not fatal: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board %q and repoX", a.Dir, a.DefaultLabel, board)
	}
}

// A scope is a path-separator-bounded prefix: "/…/org" must not match a sibling
// "/…/org-evil" that merely shares a string prefix.
func TestGlobal_ScopeBoundaryDoesNotMatchPrefixSibling(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(root, "central"))
	writeGlobalConfig(t, boardEntry(board, "auto", org))
	evil := mkGitRepo(t, filepath.Join(root, "org-evil", "repoX"))
	if _, err := Open(evil); err == nil {
		t.Fatal("scope /…/org must not match cwd under /…/org-evil, got activation")
	}
	repo := mkGitRepo(t, filepath.Join(org, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open under the real scope: %v", err)
	}
	if a.Dir != board {
		t.Errorf("Dir = %q, want board %q (real descendant activates)", a.Dir, board)
	}
}

// Both cwd and the configured scope are canonicalized, so a scope written through
// a symlink still matches a cwd reached by the real path (e.g. macOS /var ->
// /private/var).
func TestGlobal_SymlinkedScopeStillMatches(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	realOrg := filepath.Join(root, "real", "org")
	if err := os.MkdirAll(realOrg, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	board := mustInitBoard(t, filepath.Join(root, "central"))
	// scope is written via the symlink; the repo is opened via the real path.
	writeGlobalConfig(t, boardEntry(board, "auto", filepath.Join(link, "org")))
	repo := mkGitRepo(t, filepath.Join(realOrg, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a symlinked scope must still match the real cwd: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board and repoX", a.Dir, a.DefaultLabel)
	}
}

// A scope may use ~, expanded against $HOME exactly like a board path.
func TestGlobal_TildeScopeExpandsAgainstHome(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	org := filepath.Join(home, "org")
	board := mustInitBoard(t, filepath.Join(home, "central"))
	writeGlobalConfig(t, boardEntry(board, "auto", "~/org"))
	repo := mkGitRepo(t, filepath.Join(org, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a ~/ scope must expand against HOME: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board and repoX", a.Dir, a.DefaultLabel)
	}
}

// A non-absolute scope resolves against the config file's directory, not cwd.
func TestGlobal_RelativeScopeResolvesAgainstConfigDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	cfgHome := filepath.Join(root, "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(root, "central"))
	// from cfgHome/furrow, "../../org" -> root/org (resolved against the config dir).
	cfg := boardEntry(board, "auto", "../../org")
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := mkGitRepo(t, filepath.Join(org, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("a relative scope must resolve against the config dir: %v", err)
	}
	if a.Dir != board || a.DefaultLabel != "repoX" {
		t.Errorf("Dir=%q label=%q, want board and repoX", a.Dir, a.DefaultLabel)
	}
}

// When the nearer (longest-scope) board wins selection but its path does not
// exist, that is a loud error — never a silent fall-through to a valid outer
// board (only the winner is stat'd, after the scope gate).
func TestGlobal_NearerBrokenBoardWinsAndErrorsLoud(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	inner := filepath.Join(org, "inner")
	outer := mustInitBoard(t, filepath.Join(root, "central-outer"))
	brokenInner := filepath.Join(root, "central-inner", "nope", ".furrow") // never created
	writeGlobalConfig(t, boardEntry(outer, "auto", org)+boardEntry(brokenInner, "auto", inner))
	repo := mkGitRepo(t, filepath.Join(inner, "repoX"))
	_, err := Open(repo)
	if err == nil {
		t.Fatal("the nearer broken board won selection; expected a loud error, not silent fall-through")
	}
	if !strings.Contains(err.Error(), "not an existing directory") {
		t.Errorf("err = %v, want the loud 'not an existing directory' validation", err)
	}
}

// FURROW_BOARD derives its scope from the board repo's parent and is still gated
// to it: a repo outside that parent is inert.
func TestGlobal_EnvBoardInertOutsideDerivedScope(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(org, "projects"))
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, board)
	outside := mkGitRepo(t, filepath.Join(root, "other", "repoX")) // not under org
	if _, err := Open(outside); err == nil {
		t.Fatal("FURROW_BOARD must stay gated to its derived parent scope, got activation")
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
