package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// These are the real-git two-clone e2e tests for `furrow sync` (the style of
// fsstore/conflict_test.go): a bare origin, two working clones A and B, and the
// public App API driving the boards.

func gitOrSkip(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	return git
}

func runGitT(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// setupClones builds origin (bare) + clone A (board initialized and pushed) +
// clone B (cloned after, so it already has the board).
func setupClones(t *testing.T) (git, cloneA, cloneB string) {
	t.Helper()
	git = gitOrSkip(t)
	origin := t.TempDir()
	runGitT(t, git, origin, "init", "-q", "--bare", "-b", "main")

	cloneA = filepath.Join(t.TempDir(), "a")
	runGitT(t, git, filepath.Dir(cloneA), "clone", "-q", origin, cloneA)
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@e"}} {
		runGitT(t, git, cloneA, "config", kv[0], kv[1])
	}
	if _, err := Init(cloneA); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", "-A")
	runGitT(t, git, cloneA, "commit", "-q", "-m", "board")
	runGitT(t, git, cloneA, "push", "-q", "-u", "origin", "main")

	cloneB = filepath.Join(t.TempDir(), "b")
	runGitT(t, git, filepath.Dir(cloneB), "clone", "-q", origin, cloneB)
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@e"}} {
		runGitT(t, git, cloneB, "config", kv[0], kv[1])
	}
	return git, cloneA, cloneB
}

func openBoard(t *testing.T, dir string) *App {
	t.Helper()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The happy path: adds on both machines converge through sync with no
// conflict — the payoff of per-task shards.
func TestSyncTwoClonesConverge(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)

	a := openBoard(t, cloneA)
	taskA, err := a.Add("from A", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync("")
	if err != nil {
		t.Fatalf("A sync: %v (progress %+v)", err, p)
	}
	if !p.Committed || !p.Pulled || !p.Pushed || p.Conflict {
		t.Errorf("A progress = %+v; want committed+pulled+pushed, no conflict", p)
	}

	b := openBoard(t, cloneB)
	taskB, err := b.Add("from B", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sync(""); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	// B now has both tasks.
	if _, _, err := openBoard(t, cloneB).Get(taskA.ID); err != nil {
		t.Errorf("B must see A's task after sync: %v", err)
	}

	// A pulls B's task with a no-change sync (nothing to commit or push).
	p, err = openBoard(t, cloneA).Sync("")
	if err != nil {
		t.Fatalf("A second sync: %v", err)
	}
	if p.Committed {
		t.Errorf("nothing changed on A; committed must be false, got %+v", p)
	}
	if _, _, err := openBoard(t, cloneA).Get(taskB.ID); err != nil {
		t.Errorf("A must see B's task after sync: %v", err)
	}
}

// The failure contract: both sides edit the SAME shard; the loser's sync hits a
// rebase conflict, aborts automatically (no conflict markers on the board, the
// local sync commit survives), and reports sync-conflict + the paths.
func TestSyncConflictAbortsAndReportsPaths(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)

	// A seeds one shared task and pushes it.
	a := openBoard(t, cloneA)
	shared, err := a.Add("shared", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(""); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(""); err != nil { // B pulls it
		t.Fatal(err)
	}

	// Both sides now retitle the same task, divergently.
	if _, err := openBoard(t, cloneA).SetTitle(shared.ID, "A wins"); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(""); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).SetTitle(shared.ID, "B wins"); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync("")
	if err == nil {
		t.Fatal("B sync must fail on the conflicting shard")
	}
	if !p.Committed || p.Pulled || p.Pushed || !p.Conflict {
		t.Errorf("progress = %+v; want committed=true pulled=false pushed=false conflict=true", p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.ID != "sync-conflict" || fe.Code != core.CodeInternal {
		t.Fatalf("want sync-conflict internal error, got %+v", err)
	}
	details, ok := fe.Details.(map[string]any)
	if !ok {
		t.Fatalf("details missing: %+v", fe)
	}
	paths, _ := details["paths"].([]string)
	shardPath := ".furrow/tasks/" + shared.ID + ".json"
	found := false
	for _, p := range paths {
		if p == shardPath {
			found = true
		}
	}
	if !found {
		t.Errorf("details.paths = %v; must contain %s", paths, shardPath)
	}

	// The board is restored: no rebase in progress, no conflict markers — the
	// store loads, and B's local commit (its title) survived.
	if strings.TrimSpace(runGitT(t, git, cloneB, "status", "--porcelain")) != "" {
		t.Errorf("board must be clean after auto-abort:\n%s", runGitT(t, git, cloneB, "status", "--porcelain"))
	}
	tk, _, err := openBoard(t, cloneB).Get(shared.ID)
	if err != nil {
		t.Fatalf("board must still load after abort: %v", err)
	}
	if tk.Title != "B wins" {
		t.Errorf("local commit must survive the abort; title = %q", tk.Title)
	}
}

// Pre-flight: outside a git repo, sync is a validation error (exit 2) and the
// progress object still comes back (all false).
func TestSyncOutsideGitIsValidation(t *testing.T) {
	gitOrSkip(t)
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	a := openBoard(t, dir)
	p, err := a.Sync("")
	if err == nil {
		t.Fatal("sync outside git must fail")
	}
	if got := core.ExitCode(err); got != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", got, core.CodeValidation)
	}
	if p == nil || p.Committed || p.Pulled || p.Pushed || p.Conflict {
		t.Errorf("progress must be the all-false object, got %+v", p)
	}
}

// Pre-flight: a repo already mid-merge is refused before sync touches anything.
func TestSyncRefusesMidMerge(t *testing.T) {
	git, cloneA, _ := setupClones(t)

	// Manufacture an unresolved merge in clone A on a plain file.
	runGitT(t, git, cloneA, "checkout", "-q", "-b", "x")
	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", "-A")
	runGitT(t, git, cloneA, "commit", "-qm", "x")
	runGitT(t, git, cloneA, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", "-A")
	runGitT(t, git, cloneA, "commit", "-qm", "y")
	cmd := exec.Command(git, "merge", "x")
	cmd.Dir = cloneA
	_ = cmd.Run() // conflicts; MERGE_HEAD left behind

	_, err := openBoard(t, cloneA).Sync("")
	if err == nil {
		t.Fatal("sync mid-merge must be refused")
	}
	if got := core.ExitCode(err); got != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", got, core.CodeValidation)
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should name the in-progress operation: %v", err)
	}
}

// startStuckRebase leaves dir with a real, non-clearing rebase in progress (an
// add/add conflict git stopped on), so MidOperation reports "rebase" — the
// concurrent-writer signature, here made permanent so the retry budget runs out.
func startStuckRebase(t *testing.T, git, dir string) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitT(t, git, dir, "checkout", "-q", "-b", "topic")
	write("c.txt", "topic\n")
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-qm", "topic change")
	runGitT(t, git, dir, "checkout", "-q", "main")
	write("c.txt", "main\n")
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-qm", "main change")
	runGitT(t, git, dir, "checkout", "-q", "topic")
	cmd := exec.Command(git, "rebase", "main") // add/add conflict — git stops mid-rebase
	cmd.Dir = dir
	_ = cmd.Run()
}

// A rebase in progress is transient (a concurrent writer momentarily rebasing),
// so sync retries it out; if it never clears, the residual failure is retryable
// (exit 3, id sync-busy) — NOT a validation error (exit 2 = do not retry).
func TestSyncRebaseBusyIsRetryableNotValidation(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	startStuckRebase(t, git, cloneA)

	a := openBoard(t, cloneA)
	a.sleep = func(time.Duration) {} // ride out the retry budget instantly
	p, err := a.Sync("")
	if err == nil {
		t.Fatal("sync on a never-clearing rebase must fail after the retry budget")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("exit = %d, want %d (retryable, not validation)", got, core.CodeInternal)
	}
	fe := core.AsError(err)
	if fe == nil || fe.ID != "sync-busy" {
		t.Fatalf("want id sync-busy, got %+v", err)
	}
	if p == nil || p.Committed || p.Pulled || p.Pushed || p.Conflict {
		t.Errorf("progress must be the all-false object, got %+v", p)
	}
}

// --message overrides the default auto-commit message.
func TestSyncMessageOverride(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(":card_file_box: chore(board): custom words"); err != nil {
		t.Fatal(err)
	}
	subject := strings.TrimSpace(runGitT(t, git, cloneA, "log", "-1", "--format=%s"))
	if subject != ":card_file_box: chore(board): custom words" {
		t.Errorf("subject = %q", subject)
	}
}
