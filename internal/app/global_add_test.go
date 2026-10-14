package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

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
	if !slices.Contains(task.Repos, "me/repoX") {
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
	if !slices.Contains(task.Repos, "other/repo") || !slices.Contains(task.Repos, "me/repoX") {
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
	if !slices.Contains(created[0].Repos, "me/repoX") {
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", scope)+"label = \"tracked\"\n")
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
	if !slices.Contains(task.Labels, "tracked") {
		t.Errorf("labels = %v, want tracked", task.Labels)
	}
	if !slices.Contains(task.Repos, "me/repoX") {
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", scope)+"label = \"auto\"\n")
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
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Labels) != 0 || !slices.Contains(task.Repos, "me/repoX") {
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", scope)+"auto_filter = false\n")
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
	testutil.UserConfig(t, "")
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
	testutil.UserConfig(t, testutil.BoardEntry(board, "auto", scope)+"label = \"tracked\"\n")

	a, err := Open(mkGitRepo(t, filepath.Join(scope, "repoY")))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatalf("the literal board label must satisfy [labels].required: %v", err)
	}
}
