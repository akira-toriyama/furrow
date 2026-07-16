package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findProblems returns the report's problems carrying code.
func findProblems(r *DoctorReport, code string) []int {
	var idx []int
	for i, p := range r.Problems {
		if p.Code == code {
			idx = append(idx, i)
		}
	}
	return idx
}

// mustDoctor runs Doctor with the given cwd/dirs, failing the test on the
// reserved environment error (never an unhealthy machine).
func mustDoctor(t *testing.T, cwd string, dirs ...string) *DoctorReport {
	t.Helper()
	r, err := Doctor(context.Background(), cwd, dirs)
	if err != nil {
		t.Fatalf("Doctor must report, not fail: %v", err)
	}
	return r
}

func TestDoctorHealthyMachine(t *testing.T) {
	// House layout: scope = the org dir, the board in a child checkout, other
	// checkouts as siblings.
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	checkout := filepath.Join(scope, "repo1")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, boardEntry(board, "auto", scope))

	r := mustDoctor(t, checkout)
	if !r.Healthy {
		t.Fatalf("a healthy machine must report Healthy; problems: %+v", r.Problems)
	}
	if len(r.Boards) != 1 || !r.Boards[0].Exists || !r.Boards[0].Writable {
		t.Fatalf("boards = %+v, want one existing writable entry", r.Boards)
	}
	if r.Boards[0].Git.State != GitNotARepo {
		t.Errorf("a board outside git must probe %q, got %q", GitNotARepo, r.Boards[0].Git.State)
	}
	// cwd resolves through the user-config arm, informationally.
	if len(r.Resolutions) != 1 {
		t.Fatalf("resolutions = %+v, want the cwd probe alone", r.Resolutions)
	}
	cw := r.Resolutions[0]
	if cw.Asserted || !cw.Resolved || cw.Source != "user-config" || cw.Store != board {
		t.Errorf("cwd probe = %+v, want unasserted user-config resolution to %s", cw, board)
	}
	// The board's own checkout is visible as INFO — seen, but never unhealthy.
	shadows := findProblems(r, "scope-shadowed")
	if len(shadows) != 1 {
		t.Fatalf("want exactly one scope-shadowed info (the board's own checkout), got %+v", r.Problems)
	}
	p := r.Problems[shadows[0]]
	if p.Severity != SevInfo || !strings.Contains(p.Msg, "board's own") {
		t.Errorf("own-checkout shadow must be info with the scope-less-reads phrasing: %+v", p)
	}
}

// The 2026-07-16 company-PC hole: furrow installed, the board even on disk,
// but no [[board]] in the user config — every use is a bare exit 2 that names
// nothing. Doctor must name it and hand over the concrete fix.
func TestDoctorNoBoardsIsTheDiagnosis(t *testing.T) {
	writeGlobalConfig(t, "# no boards\n")

	r := mustDoctor(t, t.TempDir())
	if r.Healthy {
		t.Fatal("a machine with no usable [[board]] must be unhealthy")
	}
	hits := findProblems(r, "no-boards")
	if len(hits) != 1 {
		t.Fatalf("want a no-boards finding, got %+v", r.Problems)
	}
	p := r.Problems[hits[0]]
	if p.Severity != "warn" || !strings.Contains(p.Msg, "furrow config init") || !strings.Contains(p.Msg, "scopes") {
		t.Errorf("the finding must carry the concrete fix (config init + a [[board]] with scopes): %+v", p)
	}

	// Entirely-missing config file: same finding, and the message says so.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	r = mustDoctor(t, t.TempDir())
	hits = findProblems(r, "no-boards")
	if len(hits) != 1 || !strings.Contains(r.Problems[hits[0]].Msg, "does not exist") {
		t.Errorf("a missing config file must be named in the finding: %+v", r.Problems)
	}
}

func TestDoctorBoardAndScopeExistence(t *testing.T) {
	root := t.TempDir()
	missingBoard := filepath.Join(root, "nowhere", DirName)
	liveScope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(liveScope, "projects"))
	deadScope := filepath.Join(root, "gone")
	writeGlobalConfig(t,
		boardEntry(board, "auto", liveScope, deadScope)+
			boardEntry(missingBoard, "auto", liveScope))

	r := mustDoctor(t, "")
	if r.Healthy {
		t.Fatal("missing board/scope must be unhealthy")
	}
	if n := findProblems(r, "board-missing"); len(n) != 1 || r.Problems[n[0]].ID != missingBoard {
		t.Errorf("want board-missing on %s, got %+v", missingBoard, r.Problems)
	}
	if n := findProblems(r, "scope-missing"); len(n) != 1 || r.Problems[n[0]].ID != deadScope {
		t.Errorf("want scope-missing on %s, got %+v", deadScope, r.Problems)
	}
	// The dead board keeps its git column honest: nothing was probed.
	if r.Boards[1].Git.State != GitUnprobed {
		t.Errorf("a board not on disk must stay %q, got %q", GitUnprobed, r.Boards[1].Git.State)
	}
	// Errors sort before warns before infos.
	if r.Problems[0].Code != "board-missing" {
		t.Errorf("errors must sort first, got %+v", r.Problems)
	}
}

func TestDoctorSchemaOutdatedWarns(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	// An older layout: the version gate makes it read-only for this binary.
	if err := os.WriteFile(filepath.Join(board, "meta.json"), []byte("{\n  \"schema_version\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, boardEntry(board, "auto", scope))

	r := mustDoctor(t, "")
	if n := findProblems(r, "schema-outdated"); len(n) != 1 || !strings.Contains(r.Problems[n[0]].Msg, "furrow upgrade") {
		t.Errorf("an outdated board must warn toward `furrow upgrade`: %+v", r.Problems)
	}
	if r.Healthy {
		t.Error("schema-outdated is a warn, so the machine is not healthy")
	}
}

func TestDoctorAssertedDirMustResolve(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	inside := filepath.Join(scope, "repo1")
	outside := filepath.Join(root, "elsewhere")
	for _, d := range []string{inside, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeGlobalConfig(t, boardEntry(board, "auto", scope))

	// cwd OUTSIDE every scope is informational — never a problem on its own.
	r := mustDoctor(t, outside)
	if len(findProblems(r, "dir-unresolved")) != 0 {
		t.Errorf("an unresolved cwd must not be a problem: %+v", r.Problems)
	}
	if r.Resolutions[0].Resolved {
		t.Errorf("cwd probe must still report unresolved: %+v", r.Resolutions[0])
	}

	// The same dir ASSERTED is the problem, with the fix in hand.
	r = mustDoctor(t, inside, inside, outside)
	n := findProblems(r, "dir-unresolved")
	if len(n) != 1 || r.Problems[n[0]].ID != outside {
		t.Fatalf("want dir-unresolved on %s, got %+v", outside, r.Problems)
	}
	if !strings.Contains(r.Problems[n[0]].Msg, "scopes") {
		t.Errorf("the finding must point at adding a scope: %+v", r.Problems[n[0]])
	}
	if len(r.Resolutions) != 3 { // cwd + 2 asserted
		t.Fatalf("resolutions = %+v, want cwd + both asserted dirs", r.Resolutions)
	}
	if !r.Resolutions[1].Asserted || !r.Resolutions[1].Resolved {
		t.Errorf("the in-scope asserted dir must resolve: %+v", r.Resolutions[1])
	}
}

func TestDoctorShadowOptOutCheckout(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	// A sibling checkout with its OWN repo-local board: nearest wins there.
	mustInitBoard(t, filepath.Join(scope, "loner"))
	writeGlobalConfig(t, boardEntry(board, "auto", scope))

	r := mustDoctor(t, "")
	var optOut bool
	for _, i := range findProblems(r, "scope-shadowed") {
		p := r.Problems[i]
		if p.ID == filepath.Join(scope, "loner") {
			optOut = true
			if p.Severity != SevInfo || !strings.Contains(p.Msg, "nearest wins") {
				t.Errorf("opt-out shadow must be info with the nearest-wins phrasing: %+v", p)
			}
		}
	}
	if !optOut {
		t.Fatalf("the loner checkout's local .furrow must surface: %+v", r.Problems)
	}
	if !r.Healthy {
		t.Errorf("shadows are info — they must not redden the machine: %+v", r.Problems)
	}
}

func TestDoctorEnvOverrides(t *testing.T) {
	writeGlobalConfig(t, "")

	// A broken override is the loudest possible misconfiguration: every furrow
	// command on the machine fails.
	t.Setenv(EnvDir, filepath.Join(t.TempDir(), "nope"))
	r := mustDoctor(t, "")
	if n := findProblems(r, "env-override-broken"); len(n) != 1 || r.Problems[n[0]].ID != EnvDir {
		t.Fatalf("want env-override-broken on %s, got %+v", EnvDir, r.Problems)
	}
	if r.Healthy {
		t.Error("a broken env override must be unhealthy")
	}

	// A working override is deliberate setup: INFO, so it never reddens, but it
	// must be said — it shadows every configured board.
	board := mustInitBoard(t, t.TempDir())
	t.Setenv(EnvDir, board)
	r = mustDoctor(t, "")
	if n := findProblems(r, "env-override"); len(n) != 1 || r.Problems[n[0]].Severity != SevInfo {
		t.Fatalf("want one env-override info, got %+v", r.Problems)
	}
	if len(findProblems(r, "no-boards")) != 1 {
		t.Errorf("FURROW_DIR does not substitute for board config — no-boards still warns: %+v", r.Problems)
	}
}

// FURROW_BOARD substitutes for a configured [[board]] (it IS a board), so
// no-boards must NOT fire — unlike FURROW_DIR, which bypasses boards entirely.
func TestDoctorEnvBoardSuppressesNoBoards(t *testing.T) {
	writeGlobalConfig(t, "")
	board := mustInitBoard(t, filepath.Join(t.TempDir(), "projects"))
	t.Setenv(EnvBoard, board)

	r := mustDoctor(t, "")
	if len(findProblems(r, "no-boards")) != 0 {
		t.Errorf("FURROW_BOARD supplies the board — no-boards must not fire: %+v", r.Problems)
	}
	if n := findProblems(r, "env-override"); len(n) != 1 {
		t.Errorf("the override itself must still be visible: %+v", r.Problems)
	}
}

func TestDoctorGlobalConfigUnreadable(t *testing.T) {
	writeGlobalConfig(t, "not [ toml")

	r := mustDoctor(t, "")
	if n := findProblems(r, "global-config-unreadable"); len(n) != 1 || r.Problems[n[0]].Severity != "error" {
		t.Fatalf("want a global-config-unreadable error, got %+v", r.Problems)
	}
	if r.Healthy {
		t.Error("an unparseable config must be unhealthy")
	}
}

// The git column: a board clone that is behind its upstream (as of its last
// fetch) warns toward `furrow sync`; unpushed local commits warn the same way.
func TestDoctorGitAheadBehind(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)

	// cloneA moves the board and pushes; cloneB fetches WITHOUT rebasing, then
	// commits its own change without pushing — ahead 1, behind 1.
	a := openBoard(t, cloneA)
	if _, err := a.Add("from A", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", "-A")
	runGitT(t, git, cloneA, "commit", "-q", "-m", "a")
	runGitT(t, git, cloneA, "push", "-q")

	b := openBoard(t, cloneB)
	if _, err := b.Add("from B", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneB, "add", "-A")
	runGitT(t, git, cloneB, "commit", "-q", "-m", "b")
	runGitT(t, git, cloneB, "fetch", "-q")

	boardB := filepath.Join(cloneB, DirName)
	writeGlobalConfig(t, boardEntry(boardB, "auto", filepath.Dir(cloneB)))

	r := mustDoctor(t, "")
	if len(r.Boards) != 1 {
		t.Fatalf("boards = %+v", r.Boards)
	}
	g := r.Boards[0].Git
	if g.State != GitOK || g.Ahead != 1 || g.Behind != 1 {
		t.Fatalf("git = %+v, want {ok 1 1}", g)
	}
	if len(findProblems(r, "board-behind")) != 1 || len(findProblems(r, "board-ahead")) != 1 {
		t.Errorf("want board-behind and board-ahead warns, got %+v", r.Problems)
	}
	for _, i := range findProblems(r, "board-behind") {
		if !strings.Contains(r.Problems[i].Msg, "furrow sync") {
			t.Errorf("the fix is `furrow sync`: %+v", r.Problems[i])
		}
	}
}
