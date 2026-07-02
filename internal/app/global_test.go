package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// globalLayout builds tmp/org/projects/.furrow (the central board) and writes a
// user-level furrow config.toml (under an isolated XDG_CONFIG_HOME) whose single
// [[board]] points at it with scopes = [tmp/org] and the given repo mode. It
// returns the scope dir and the board dir; callers create repos under scope to
// Open from.
func globalLayout(t *testing.T, repoMode string) (scope, board string) {
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
	writeGlobalConfig(t, boardEntry(board, repoMode, scope))
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
func boardEntry(path, repo string, scopes ...string) string {
	q := make([]string, len(scopes))
	for i, s := range scopes {
		q[i] = "\"" + s + "\""
	}
	return "[[board]]\npath = \"" + path + "\"\nscopes = [" + strings.Join(q, ", ") + "]\nrepo = \"" + repo + "\"\n"
}

// mustInitBoard inits a fresh central board at dir and returns its .furrow path.
func mustInitBoard(t *testing.T, dir string) string {
	t.Helper()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, DirName)
}

// mkGitRepo creates dir as a git checkout whose origin URL derives
// "me/<basename>" (e.g. repoX -> me/repoX).
func mkGitRepo(t *testing.T, dir string) string {
	t.Helper()
	return mkGitRepoWithOrigin(t, dir, "git@github.com:me/"+filepath.Base(dir)+".git")
}

func TestGlobal_ActivatesUnderScopeWithAutoRepo(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board {
		t.Errorf("Dir = %q, want board %q", a.Dir, board)
	}
	if a.DefaultRepo != "me/repoX" {
		t.Errorf("DefaultRepo = %q, want me/repoX", a.DefaultRepo)
	}
	// The derived repo flows into the short-name resolution seam.
	if len(a.BoardRepos) != 1 || a.BoardRepos[0] != "me/repoX" {
		t.Errorf("BoardRepos = %v, want [me/repoX]", a.BoardRepos)
	}
}

func TestGlobal_AutoRepoFromNestedSubdir(t *testing.T) {
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board %q and me/repoX", a.Dir, a.DefaultRepo, board)
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
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty (local store, no board scope)", a.DefaultRepo)
	}
}

func TestGlobal_PointerBeatsGlobal(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	// a pointer in the repo redirects to the same central board with its own repo
	body := "board = \"projects/.furrow\"\ndefault_repo = \"ptr/repo\"\n"
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
	if a.DefaultRepo != "ptr/repo" {
		t.Errorf("DefaultRepo = %q, want ptr/repo (pointer beats global board)", a.DefaultRepo)
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
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty (FURROW_DIR injects no scope)", a.DefaultRepo)
	}
}

func TestGlobal_NoGitRepoNoScopePlusWarn(t *testing.T) {
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
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty", a.DefaultRepo)
	}
	if len(a.ScopeWarnings) == 0 {
		t.Error("want a stderr-bound warning about the missing git repo, got none")
	}
}

// A checkout with no usable origin URL and no ghq-style path derives NOTHING:
// the board opens unscoped with a warning, `add` creates drafts, and the bare
// directory name is never written into repos (the invariant's discovery-level
// pin; deriveScopeRepo has the unit-level one).
func TestGlobal_NoOriginMeansDraftsNeverBareName(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepoWithOrigin(t, filepath.Join(scope, "repoX"), "") // .git, no origin
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" || len(a.BoardRepos) != 0 {
		t.Fatalf("DefaultRepo=%q BoardRepos=%v, want no derived repo", a.DefaultRepo, a.BoardRepos)
	}
	if len(a.ScopeWarnings) == 0 {
		t.Error("want a drafts warning, got none")
	}
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Repos) != 0 {
		t.Errorf("repos = %v, want [] (a draft; the dir name must never be written)", task.Repos)
	}
}

// A ghq-style checkout path (…/github.com/<owner>/<repo>) is the fallback when
// the origin URL is unusable — e.g. a repo not pushed yet.
func TestGlobal_GhqPathFallbackScopes(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	dir := filepath.Join(scope, "github.com", "me", "proj")
	mkGitRepoWithOrigin(t, dir, "") // .git but no origin
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "me/proj" {
		t.Errorf("DefaultRepo = %q, want me/proj (ghq-path fallback)", a.DefaultRepo)
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
	writeGlobalConfig(t, "[[board]]\npath = \""+board+"\"\nrepo = \"auto\"\n")
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want %q and me/repoX", a.Dir, a.DefaultRepo, board)
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
	writeGlobalConfig(t, boardEntry(outerBoard, "auto", org)+boardEntry(innerBoard, "auto", inner))
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
	writeGlobalConfig(t, boardEntry(innerBoard, "auto", inner)+boardEntry(outerBoard, "auto", org))
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
	writeGlobalConfig(t, boardEntry(boardA, "auto", org)+boardEntry(boardB, "auto", org))
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board %q and me/repoX", a.Dir, a.DefaultRepo, board)
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board %q and me/repoX", a.Dir, a.DefaultRepo, board)
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board and me/repoX", a.Dir, a.DefaultRepo)
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board and me/repoX", a.Dir, a.DefaultRepo)
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
	if a.Dir != board || a.DefaultRepo != "me/repoX" {
		t.Errorf("Dir=%q repo=%q, want board and me/repoX", a.Dir, a.DefaultRepo)
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

// `add` inside a scoped board unions the derived repo into the task's repos —
// the repos mirror of the old label union.
func TestGlobal_AddUnionsDerivedRepo(t *testing.T) {
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
	if !contains(task.Repos, "me/repoX") {
		t.Errorf("repos = %v, want me/repoX", task.Repos)
	}
	if len(task.Labels) != 0 {
		t.Errorf("labels = %v, want none (the repo scope is not a label)", task.Labels)
	}
}

// --draft suppresses exactly the board-repo union: the task is created with
// repos == [] on a scoped board.
func TestGlobal_AddDraftSuppressesBoardRepo(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a draft", AddOpts{Draft: true})
	if err != nil {
		t.Fatalf("Add --draft: %v", err)
	}
	if len(task.Repos) != 0 {
		t.Errorf("repos = %v, want [] (--draft suppresses the board repo)", task.Repos)
	}
}

// An explicit -r adds to the board repo rather than replacing it (the old
// explicit-label union semantics, mirrored).
func TestGlobal_AddExplicitRepoUnionsWithBoardRepo(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("x", AddOpts{Repos: []string{"other/repo"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains(task.Repos, "other/repo") || !contains(task.Repos, "me/repoX") {
		t.Errorf("repos = %v, want both other/repo and me/repoX", task.Repos)
	}
}

// A short name on add resolves against the DERIVED repo (BoardRepos) even
// before its first task exists — and dedupes against the board union.
func TestGlobal_AddShortNameResolvesAgainstDerivedRepo(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("x", AddOpts{Repos: []string{"repoX"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(task.Repos) != 1 || task.Repos[0] != "me/repoX" {
		t.Errorf("repos = %v, want exactly [me/repoX] (short name resolved, union deduped)", task.Repos)
	}
}

// AddMany (add --stdin) unions the board repo per spec, and Draft suppresses it.
func TestGlobal_AddManyUnionsBoardRepo(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.AddMany([]AddSpec{
		{Title: "x"},
		{Title: "y", AddOpts: AddOpts{Draft: true}},
	})
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	if !contains(created[0].Repos, "me/repoX") {
		t.Errorf("x repos = %v, want me/repoX", created[0].Repos)
	}
	if len(created[1].Repos) != 0 {
		t.Errorf("y repos = %v, want [] (Draft suppresses the union)", created[1].Repos)
	}
}

// A board's literal `label` is a pure add-time tag: unioned into labels (and it
// satisfies [labels].required), while the repo scope stays a repos concern.
func TestGlobal_LiteralLabelUnionsOnAdd(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, boardEntry(board, "auto", scope)+"label = \"tracked\"\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultLabel != "tracked" {
		t.Fatalf("DefaultLabel = %q, want tracked", a.DefaultLabel)
	}
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasLabel(task.Labels, "tracked") {
		t.Errorf("labels = %v, want tracked", task.Labels)
	}
	if !contains(task.Repos, "me/repoX") {
		t.Errorf("repos = %v, want me/repoX (label and repo are orthogonal)", task.Repos)
	}
}

// The retired label="auto" mode is a tombstone: the board still activates, no
// label is injected, and the warning lands on ScopeWarnings (stderr).
func TestGlobal_LabelAutoTombstoneWarnsOnOpen(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, boardEntry(board, "auto", scope)+"label = \"auto\"\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (\"auto\" is reserved, not a literal)", a.DefaultLabel)
	}
	found := false
	for _, w := range a.ScopeWarnings {
		if strings.Contains(w, `repo="auto"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("ScopeWarnings = %v, want the label=\"auto\" tombstone", a.ScopeWarnings)
	}
	// The task still gets the derived repo; no phantom "auto" label.
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Labels) != 0 || !contains(task.Repos, "me/repoX") {
		t.Errorf("labels=%v repos=%v, want no labels + me/repoX", task.Labels, task.Repos)
	}
}

// [labels].required is a LABEL rule: the board-derived repo does not satisfy it
// (the label demotion is orthogonal to repos), so a bare add still fails.
func TestGlobal_RequiredNotSatisfiedByRepoScope(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	if err := os.WriteFile(filepath.Join(board, "config.toml"), []byte("[labels]\nrequired = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("x", AddOpts{}); err == nil {
		t.Fatal("expected a required-label error (a repo is not a label), got nil")
	}
}

// auto_filter threads from the winning [[board]] onto the App. Omitted -> true,
// so a global board scopes reads by default exactly as before.
func TestGlobal_AutoFilterDefaultsTrueOnApp(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !a.AutoFilter {
		t.Errorf("AutoFilter = false, want true by default")
	}
}

// auto_filter = false threads onto the App, but the repo is still derived: it
// remains the add-time attachment, only read-time scoping is turned off.
func TestGlobal_AutoFilterFalseThreadsToApp(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, boardEntry(board, "auto", scope)+"auto_filter = false\n")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "me/repoX" {
		t.Errorf("DefaultRepo = %q, want me/repoX (repo still derived for add)", a.DefaultRepo)
	}
	if a.AutoFilter {
		t.Errorf("AutoFilter = true, want false (auto_filter = false threaded to App)")
	}
}

// A pointer always scopes reads (no auto_filter knob), so the App carries
// AutoFilter = true whenever discovery came through a pointer.
func TestGlobal_PointerAlwaysAutoFilters(t *testing.T) {
	scope, _ := globalLayout(t, "auto")
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	body := "board = \"projects/.furrow\"\ndefault_repo = \"ptr/repo\"\n"
	if err := os.WriteFile(filepath.Join(repo, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(filepath.Join(repo, "projects")); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "ptr/repo" {
		t.Fatalf("DefaultRepo = %q, want ptr/repo", a.DefaultRepo)
	}
	if !a.AutoFilter {
		t.Errorf("AutoFilter = false, want true (a pointer always scopes reads)")
	}
}

// FURROW_BOARD is a synthetic single board; it scopes reads by default.
func TestGlobal_EnvBoardAutoFilters(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, board)
	repo := mkGitRepo(t, filepath.Join(scope, "repoX"))
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !a.AutoFilter {
		t.Errorf("AutoFilter = false, want true (FURROW_BOARD scopes by default)")
	}
}

// A .git FILE with a dangling gitdir still counts as "a git repo encloses cwd"
// for discovery (the board opens), but derivation fails softly: no repo scope,
// one warning — never a guess from the directory name.
func TestGlobal_GitFileDanglingGitdirWarns(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	wt := filepath.Join(scope, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nowhere/at/all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(wt)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != board || a.DefaultRepo != "" {
		t.Errorf("Dir=%q repo=%q, want board and no derived repo", a.Dir, a.DefaultRepo)
	}
	if len(a.ScopeWarnings) == 0 {
		t.Error("want a derivation warning, got none")
	}
}

// The board's literal label satisfies [labels].required — the union happens
// before the check (the positive twin of the "a repo does NOT satisfy it"
// case), so a required-labels board stays zero-friction under a literal tag.
func TestGlobal_LiteralLabelSatisfiesRequired(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	cfgPath := filepath.Join(board, "config.toml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(cfg), "# required = false", "required = true", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, boardEntry(board, "auto", scope)+"label = \"tracked\"\n")

	a, err := Open(mkGitRepo(t, filepath.Join(scope, "repoY")))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatalf("the literal board label must satisfy [labels].required: %v", err)
	}
}
