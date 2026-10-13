package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

// globalLayout builds tmp/org/projects/.furrow (the central board) and writes a
// user-level furrow config.toml (under an isolated config home) whose single
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
	testutil.UserConfig(t, testutil.BoardEntry(board, repoMode, scope))
	return scope, board
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
	testutil.UserConfig(t, "[[board]]\npath = \""+board+"\"\nrepo = \"auto\"\n")
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
	testutil.UserConfig(t, "") // no [[board]] in the file; FURROW_BOARD drives it
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
	testutil.UserConfig(t, testutil.BoardEntry(filepath.Join(scope, "nope", ".furrow"), "auto", scope))
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
	testutil.UserConfig(t, testutil.BoardEntry(outerBoard, "auto", org)+testutil.BoardEntry(innerBoard, "auto", inner))
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
	testutil.UserConfig(t, testutil.BoardEntry(innerBoard, "auto", inner)+testutil.BoardEntry(outerBoard, "auto", org))
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
	testutil.UserConfig(t, testutil.BoardEntry(boardA, "auto", org)+testutil.BoardEntry(boardB, "auto", org))
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", orgA, orgB))
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
	testutil.UserConfig(t, testutil.BoardEntry(broken, "auto", orgBad)+testutil.BoardEntry(good, "auto", orgGood))
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
	bad := testutil.BoardEntry("~bob/projects/.furrow", "auto", filepath.Join(root, "bad"))
	testutil.UserConfig(t, bad+testutil.BoardEntry(good, "auto", orgGood))
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", "~bob/nope", org))
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", org))
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", filepath.Join(link, "org")))
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", "~/org"))
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
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(root, "central"))
	// from root/cfg/furrow, "../../org" -> root/org (resolved against the config dir).
	testutil.UserConfigIn(t, filepath.Join(root, "cfg"), testutil.BoardEntry(board, "auto", "../../org"))
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
	testutil.UserConfig(t, testutil.BoardEntry(outer, "auto", org)+testutil.BoardEntry(brokenInner, "auto", inner))
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
	testutil.UserConfig(t, "")
	t.Setenv(EnvBoard, board)
	outside := mkGitRepo(t, filepath.Join(root, "other", "repoX")) // not under org
	if _, err := Open(outside); err == nil {
		t.Fatal("FURROW_BOARD must stay gated to its derived parent scope, got activation")
	}
}

// Outside FURROW_BOARD's derived scope the give-up error prescribed two exits
// that are dead while the override is set — a scope in the config file (never
// read) and FURROW_BOARD itself (already pointed) — even when the config
// already declares a scope enclosing the directory (t-xr78). It must name the
// override's scope and the exits that work, and unsetting must really work.
func TestGlobal_EnvBoardOutsideScopeNamesLiveRemedies(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(org, "projects"))
	other := filepath.Join(root, "other")
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", other))
	outside := mkGitRepo(t, filepath.Join(other, "repoX"))
	t.Setenv(EnvBoard, board)

	_, err := Open(outside)
	if err == nil {
		t.Fatal("FURROW_BOARD replaces the config's boards — its scope must not activate")
	}
	msg := err.Error()
	for _, want := range []string{`"` + org + `"`, `FURROW_DIR="` + board + `"`, "unset FURROW_BOARD"} {
		if !strings.Contains(msg, want) {
			t.Errorf("remedy must name %s, got: %s", want, msg)
		}
	}
	for _, dead := range []string{"board's scopes in", "point FURROW_BOARD"} {
		if strings.Contains(msg, dead) {
			t.Errorf("remedy must not prescribe %q while FURROW_BOARD is set: %s", dead, msg)
		}
	}
	var fe *core.Error
	if !errors.As(err, &fe) || fe.Kind != core.KindValidation || len(fe.Candidates) != 1 || fe.Candidates[0] != board {
		t.Errorf("want a validation error with the override's store as the one candidate, got %#v", err)
	}

	// A remedy pointing FURROW_DIR at a store that is not there would be dead
	// too: a missing override is itself the answer.
	t.Setenv(EnvBoard, filepath.Join(org, "gone", DirName))
	if _, err := Open(outside); err == nil || !strings.Contains(err.Error(), "not an existing directory") {
		t.Errorf("a missing FURROW_BOARD store must be named as the problem, got %v", err)
	}

	t.Setenv(EnvBoard, "")
	if a, err := Open(outside); err != nil || a.Dir != board {
		t.Errorf("the named exit must work: with FURROW_BOARD unset the config scope resolves the board, got %v / %+v", err, a)
	}
}

// Every exit the FURROW_BOARD give-up names must work from where it was hit,
// and none may lead to init (t-xr78 review): "unset FURROW_BOARD" only when a
// configured board then resolves here — on a board-less machine unsetting lands
// on the init advice — "cd into that scope" never for a relative value, whose
// scope moves with the cwd, and an unusable override names itself as a
// validation error, with the scope it would still miss.
func TestGlobal_EnvBoardGiveUpOffersOnlyLiveExits(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(org, "projects"))
	outside := mkGitRepo(t, filepath.Join(root, "other", "repoX"))
	open := func(t *testing.T) string {
		t.Helper()
		_, err := Open(outside)
		var fe *core.Error
		if !errors.As(err, &fe) || fe.Kind != core.KindValidation {
			t.Fatalf("want a validation error, got %#v", err)
		}
		if strings.Contains(fe.Msg, "furrow init") {
			t.Errorf("no exit may lead to init: %s", fe.Msg)
		}
		return fe.Msg
	}

	t.Run("no board configured", func(t *testing.T) {
		testutil.UserConfig(t, "")
		t.Setenv(EnvBoard, board)
		msg := open(t)
		if strings.Contains(msg, "unset FURROW_BOARD:") || !strings.Contains(msg, "no [[board]] is configured") {
			t.Errorf("unsetting resolves nothing on a board-less machine and must say so: %s", msg)
		}
		if strings.Contains(msg, "entries are not read") {
			t.Errorf("with no [[board]] configured there is no shadow to name: %s", msg)
		}
	})
	t.Run("boards configured elsewhere", func(t *testing.T) {
		testutil.UserConfig(t, testutil.BoardEntry(board, "auto", filepath.Join(root, "elsewhere")))
		t.Setenv(EnvBoard, board)
		msg := open(t)
		if strings.Contains(msg, "unset FURROW_BOARD:") || !strings.Contains(msg, "no configured [[board]] scope encloses it either") {
			t.Errorf("unsetting would not help here and must say so: %s", msg)
		}
	})
	t.Run("relative value", func(t *testing.T) {
		testutil.UserConfig(t, "")
		rel, err := filepath.Rel(mustGetwd(t), board)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvBoard, rel)
		msg := open(t)
		// Neither cd nor "make it absolute" alone resolves this dir (same store,
		// same scope): the relative value is a note, not an exit.
		if strings.Contains(msg, "cd into that scope") || strings.Contains(msg, "make FURROW_BOARD absolute") || !strings.Contains(msg, "FURROW_BOARD is relative") {
			t.Errorf("a relative value's scope moves with the cwd — no cd exit: %s", msg)
		}
	})
	t.Run("missing store outside its scope", func(t *testing.T) {
		testutil.UserConfig(t, "")
		t.Setenv(EnvBoard, filepath.Join(org, "gone", DirName))
		msg := open(t)
		for _, want := range []string{"not an existing directory", "set FURROW_DIR to an existing store", "even once that store exists", `"` + org + `"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("want %q in: %s", want, msg)
			}
		}
		if strings.Contains(msg, "— fix it") {
			t.Errorf("fixing the store leaves this dir outside its scope — no fix-it exit: %s", msg)
		}
	})
	t.Run("missing store inside its scope", func(t *testing.T) {
		testutil.UserConfig(t, "")
		t.Setenv(EnvBoard, filepath.Join(org, "gone", DirName))
		_, err := Open(mkGitRepo(t, filepath.Join(org, "repoQ")))
		if err == nil || !strings.Contains(err.Error(), "FURROW_BOARD=") || !strings.Contains(err.Error(), "— fix it") || strings.Contains(err.Error(), "central board") {
			t.Errorf("inside its scope a missing store must name FURROW_BOARD with the fix-it exit: %v", err)
		}
	})
	t.Run("unresolvable value", func(t *testing.T) {
		testutil.UserConfig(t, "")
		t.Setenv(EnvBoard, "~nobody/x/.furrow")
		if msg := open(t); !strings.Contains(msg, `FURROW_BOARD="~nobody/x/.furrow" cannot be resolved`) {
			t.Errorf("an unresolvable override must name itself: %s", msg)
		}
	})
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
