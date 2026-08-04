package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBoardConfig replaces the board's config.toml with body. Clamp-don't-reject
// means a partial file is legal — every absent value falls back to Default() — so
// a test can declare one key and nothing else. Bare keys go FIRST on purpose:
// TOML binds a bare key to the table above it, so `default_repo` written after
// `[lanes]` would decode as `lanes.default_repo` and vanish (with a warning).
func writeBoardConfig(t *testing.T, board, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(board, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// localBoardWithRepo inits a board at root/<name>, declares default_repo in its
// own config.toml, and returns the checkout dir to Open from (the board's own
// tree, which is where local discovery wins).
func localBoardWithRepo(t *testing.T, repo string) string {
	t.Helper()
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "# no boards\n")
	dir := t.TempDir()
	board := mustInitBoard(t, dir)
	writeBoardConfig(t, board, "default_repo = \""+repo+"\"\n")
	return dir
}

// The whole point of the key: a board reached by LOCAL discovery — cwd inside
// the board's own tree, the arm that injects no scope — still scopes.
func TestBoardDefaultRepo_ScopesLocalDiscovery(t *testing.T) {
	dir := localBoardWithRepo(t, "acme/app")
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != "local" {
		t.Fatalf("Source = %q, want local (the arm this key exists for)", a.Source)
	}
	if a.DefaultRepo != "acme/app" {
		t.Errorf("DefaultRepo = %q, want acme/app from the board's own config.toml", a.DefaultRepo)
	}
	if !a.AutoFilter {
		t.Error("a board that declares its repo filters reads by it; there is no board-side auto_filter knob")
	}
	if len(a.BoardRepos) != 1 || a.BoardRepos[0] != "acme/app" {
		t.Errorf("BoardRepos = %v, want [acme/app] so short names resolve before the first task", a.BoardRepos)
	}
	if len(a.Warnings) != 0 {
		t.Errorf("a well-formed default_repo warns about nothing, got %v", a.Warnings)
	}
}

// FURROW_DIR is the other scope-less arm. It stays "explicit, no scope
// INJECTION" — discovery still adds nothing — but the board it points at may
// carry a scope of its own, and a board must not answer differently depending on
// which of the two scope-less arms reached it.
func TestBoardDefaultRepo_ScopesFurrowDir(t *testing.T) {
	dir := localBoardWithRepo(t, "acme/app")
	t.Setenv(EnvDir, filepath.Join(dir, DirName))
	a, err := Open(t.TempDir()) // cwd is nowhere near the board
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Source != "env" {
		t.Fatalf("Source = %q, want env", a.Source)
	}
	if a.DefaultRepo != "acme/app" || !a.AutoFilter {
		t.Errorf("DefaultRepo/AutoFilter = %q/%t, want acme/app/true", a.DefaultRepo, a.AutoFilter)
	}
}

// The add-time consequence the task was filed for: a bare `add` from inside the
// board stops producing repo-less drafts.
func TestBoardDefaultRepo_AddAttachesInsteadOfDrafting(t *testing.T) {
	dir := localBoardWithRepo(t, "acme/app")
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	task, err := a.Add("no -r anywhere", AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(task.Repos) != 1 || task.Repos[0] != "acme/app" {
		t.Errorf("repos = %v, want [acme/app] — the draft-by-accident case is what this fixes", task.Repos)
	}
	draft, err := a.Add("explicitly a draft", AddOpts{Draft: true})
	if err != nil {
		t.Fatalf("Add --draft: %v", err)
	}
	if len(draft.Repos) != 0 {
		t.Errorf("--draft repos = %v, want none: --draft suppresses exactly this union", draft.Repos)
	}
}

// Precedence, arm 1: a pointer is nearer to the operator and keeps winning.
func TestBoardDefaultRepo_PointerWins(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "# no boards\n")
	root := t.TempDir()
	board := mustInitBoard(t, filepath.Join(root, "central"))
	writeBoardConfig(t, board, "default_repo = \"board/self\"\n")

	repo := filepath.Join(root, "code")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"" + board + "\"\ndefault_repo = \"ptr/repo\"\n"
	if err := os.WriteFile(filepath.Join(repo, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "ptr/repo" {
		t.Errorf("DefaultRepo = %q, want ptr/repo — the board key is a fallback, never an override", a.DefaultRepo)
	}
}

// Precedence, arm 2: a user-level [[board]] that supplies a repo keeps winning…
func TestBoardDefaultRepo_GlobalBoardRepoWins(t *testing.T) {
	scope, board := globalLayout(t, "cfg/repo")
	writeBoardConfig(t, board, "default_repo = \"board/self\"\n")
	work := filepath.Join(scope, "repoX")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "cfg/repo" {
		t.Errorf("DefaultRepo = %q, want cfg/repo from the [[board]] entry", a.DefaultRepo)
	}
}

// …and one that resolves to NO repo has still ANSWERED the scope question:
// `repo = ""` documents itself as no scope. The gate is the arm, not an empty
// repo — otherwise a committed, shared file would silently override a nearer
// per-machine choice, inverting furrow's nearest-wins rule.
func TestBoardDefaultRepo_DoesNotOverrideAnUnscopedGlobalBoard(t *testing.T) {
	scope, board := globalLayout(t, "")
	writeBoardConfig(t, board, "default_repo = \"board/self\"\n")
	work := filepath.Join(scope, "repoX")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty: `repo = \"\"` is an answer, not a hole", a.DefaultRepo)
	}
}

// The sharper half of the same rule. A `repo = "auto"` that cannot derive has
// already told the operator on stderr that there is NO scope and that new tasks
// will be drafts. Filling it in from the board would leave furrow filtering
// reads by a repo it just said did not exist.
func TestBoardDefaultRepo_DoesNotContradictAFailedAuto(t *testing.T) {
	scope, board := globalLayout(t, "auto")
	writeBoardConfig(t, board, "default_repo = \"board/self\"\n")
	work := filepath.Join(scope, "notgit") // no enclosing checkout: auto cannot derive
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !warnMentions(a.ScopeWarnings, "no repo scope") {
		t.Fatalf("ScopeWarnings = %v, want the failed-auto note (the premise of this test)", a.ScopeWarnings)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q while stderr says there is no repo scope — the two must agree", a.DefaultRepo)
	}
}

// A pointer with no default_repo is "redirect only" — also an answer.
func TestBoardDefaultRepo_DoesNotOverrideARedirectOnlyPointer(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "# no boards\n")
	root := t.TempDir()
	board := mustInitBoard(t, filepath.Join(root, "central"))
	writeBoardConfig(t, board, "default_repo = \"board/self\"\n")

	repo := filepath.Join(root, "code")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, PointerName), []byte("board = \""+board+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty: a pointer without default_repo redirects without scoping", a.DefaultRepo)
	}
}

// "auto" is refused ON PURPOSE: config.toml is committed and shared, so deriving
// the repo from cwd would differ per checkout — the very cwd-dependence the key
// removes. The pointer/[[board]] files may say "auto" because they are not shared.
func TestBoardDefaultRepo_RefusesAuto(t *testing.T) {
	dir := localBoardWithRepo(t, "auto")
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty: a shared config may not derive a per-checkout repo", a.DefaultRepo)
	}
	// It must say WHY, not just "not owner/repo-shaped": a reader reached for
	// "auto" because the pointer and [[board]] keys of the same name accept it,
	// and the answer is that this file is shared, not that the word is malformed.
	if !warnMentions(a.Warnings, "committed and shared") {
		t.Errorf("warnings = %v, want the shared-config reason for refusing \"auto\"", a.Warnings)
	}
}

// A malformed literal clamps away like every other config.toml value — never a
// bare directory name written into repos.
func TestBoardDefaultRepo_RefusesBareName(t *testing.T) {
	dir := localBoardWithRepo(t, "app")
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" {
		t.Errorf("DefaultRepo = %q, want empty for a non-owner/repo value", a.DefaultRepo)
	}
	if !warnMentions(a.Warnings, "owner/repo-shaped") {
		t.Errorf("warnings = %v, want the shape clamp", a.Warnings)
	}
}

// The typo is reported even when the key would have been inert (a nearer arm
// answered) — otherwise whether you hear about a broken config would depend on
// the directory you ran from, which is the class of bug this change is about.
func TestBoardDefaultRepo_WarnsEvenWhenOutranked(t *testing.T) {
	scope, board := globalLayout(t, "cfg/repo")
	writeBoardConfig(t, board, "default_repo = \"app\"\n")
	work := filepath.Join(scope, "repoX")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "cfg/repo" {
		t.Fatalf("DefaultRepo = %q, want the nearer scope", a.DefaultRepo)
	}
	if !warnMentions(a.Warnings, "default_repo") {
		t.Errorf("warnings = %v, want the clamp reported anyway", a.Warnings)
	}
}

// An undeclared key changes nothing: every board that does not opt in behaves
// exactly as before (the local arm stays scope-less).
func TestBoardDefaultRepo_AbsentKeepsLocalUnscoped(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "# no boards\n")
	dir := t.TempDir()
	mustInitBoard(t, dir)
	a, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultRepo != "" || a.AutoFilter {
		t.Errorf("DefaultRepo/AutoFilter = %q/%t, want empty/false for a board that declares nothing", a.DefaultRepo, a.AutoFilter)
	}
}

// doctor must report the scope the real commands use, not the one discovery
// alone knows about — it reads no config, so it would otherwise report none.
func TestBoardDefaultRepo_DoctorReportsIt(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeBoardConfig(t, board, "default_repo = \"acme/board\"\n")
	writeGlobalConfig(t, boardEntry(board, "", scope))

	own := filepath.Dir(board) // the board's own checkout: source=local
	r, err := Doctor(context.Background(), own, nil)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(r.Resolutions) != 1 {
		t.Fatalf("resolutions = %+v, want the cwd probe alone", r.Resolutions)
	}
	if got := r.Resolutions[0]; got.Source != "local" || got.ScopeRepo != "acme/board" {
		t.Errorf("cwd probe = %+v, want a local resolution reporting scope acme/board", got)
	}
	// …and the finding that exists to say "you lose the scope here" must go
	// quiet, because you no longer do.
	if idx := findProblems(r, "scope-shadowed"); len(idx) != 0 {
		t.Errorf("scope-shadowed = %+v, want none: the board's own default_repo reproduces the scope", r.Problems)
	}
}

// The same layout WITHOUT the key still gets the finding — proof the test above
// measures the key and not the layout.
func TestBoardDefaultRepo_DoctorStillWarnsWithoutIt(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, boardEntry(board, "", scope))

	r, err := Doctor(context.Background(), filepath.Dir(board), nil)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	idx := findProblems(r, "scope-shadowed")
	if len(idx) != 1 {
		t.Fatalf("scope-shadowed = %+v, want the own-checkout info", r.Problems)
	}
	if !strings.Contains(r.Problems[idx[0]].Msg, "default_repo") {
		t.Errorf("the finding must name the fix: %q", r.Problems[idx[0]].Msg)
	}
}

func warnMentions(warns []string, sub string) bool {
	for _, w := range warns {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
