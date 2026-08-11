package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
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
	p, err := a.Sync(context.Background(), SyncOpts{})
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
	if _, err := b.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatalf("B sync: %v", err)
	}
	// B now has both tasks.
	if _, _, err := openBoard(t, cloneB).Get(taskA.ID); err != nil {
		t.Errorf("B must see A's task after sync: %v", err)
	}

	// A pulls B's task with a no-change sync (nothing to commit or push).
	p, err = openBoard(t, cloneA).Sync(context.Background(), SyncOpts{})
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

// The committed flag survives the pull rewriting the sync commit: when the
// remote is ahead, the pull --rebase picks our fresh auto-commit onto the
// remote head — a NEW sha — and the report must still say committed=true (the
// question it answers is "did this sync create a commit", not "is that exact
// sha still HEAD"). Pinned because a field observation (t-08gb) blamed exactly
// this path for a committed=false misreport.
func TestSyncCommittedTrueWhenPullRewritesTheCommit(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)

	// A pushes first, so B's sync will find the remote ahead of its base.
	a := openBoard(t, cloneA)
	if _, err := a.Add("from A", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	// B adds new shards, then syncs: auto-commit → pull rebases that commit
	// onto A's head (rewriting its sha) → push.
	b := openBoard(t, cloneB)
	if _, err := b.Add("from B", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("B sync: %v (progress %+v)", err, p)
	}
	if !p.Committed || !p.Pulled || !p.Pushed || p.Conflict {
		t.Errorf("B progress = %+v; want committed+pulled+pushed even though the rebase rewrote the commit", p)
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
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil { // B pulls it
		t.Fatal(err)
	}

	// Both sides now retitle the same task, divergently.
	if _, err := openBoard(t, cloneA).SetTitle(shared.ID, "A wins"); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).SetTitle(shared.ID, "B wins"); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatal("B sync must fail on the conflicting shard")
	}
	if !p.Committed || p.Pulled || p.Pushed || !p.Conflict {
		t.Errorf("progress = %+v; want committed=true pulled=false pushed=false conflict=true", p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncConflict || fe.Code != core.CodeInternal {
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
	p, err := a.Sync(context.Background(), SyncOpts{})
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

	_, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{})
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
	p, err := a.Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatal("sync on a never-clearing rebase must fail after the retry budget")
	}
	if got := core.ExitCode(err); got != int(core.CodeInternal) {
		t.Errorf("exit = %d, want %d (retryable, not validation)", got, core.CodeInternal)
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncBusy {
		t.Fatalf("want id sync-busy, got %+v", err)
	}
	if p == nil || p.Committed || p.Pulled || p.Pushed || p.Conflict {
		t.Errorf("progress must be the all-false object, got %+v", p)
	}
}

// The class-split: a co-located operator's merely-modified body is NOT swept
// into another session's sync (it is left dirty and surfaced in PendingBodies),
// while machine-written shards and brand-new bodies still flow, and -b names the
// explicit opt-in. This is the fix for the shared-board WIP-sweep accident.
func TestSyncScopesBodiesToPreventForeignSweep(t *testing.T) {
	git, cloneA, _ := setupClones(t)

	a := openBoard(t, cloneA)
	t1, err := a.Add("task one", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil { // t1 shard + its new body committed
		t.Fatalf("initial sync: %v", err)
	}

	// A co-located operator is mid-edit on t1's body (now modified + tracked)…
	bodyPath := filepath.Join(cloneA, ".furrow", "bodies", t1.ID+".md")
	if err := os.WriteFile(bodyPath, []byte("# task one\n\nWIP progress note, not ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// …while this session adds its own task (new shard + new body).
	t2, err := a.Add("task two", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}

	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !p.Committed {
		t.Errorf("t2's shard must be committed: %+v", p)
	}
	// t1's modified body is left uncommitted and reported…
	bodySpec := ".furrow/bodies/" + t1.ID + ".md"
	if strings.TrimSpace(runGitT(t, git, cloneA, "status", "--porcelain", "--", bodySpec)) == "" {
		t.Errorf("foreign body %s must stay dirty (uncommitted), but the tree is clean for it", bodySpec)
	}
	if !slices.Contains(p.PendingBodies, t1.ID) {
		t.Errorf("PendingBodies = %v; want it to contain %s", p.PendingBodies, t1.ID)
	}
	// …while t2's brand-new body rode along automatically, and t1's did not.
	if !slices.Contains(p.CommittedBodies, t2.ID) {
		t.Errorf("CommittedBodies = %v; want it to contain the new body %s", p.CommittedBodies, t2.ID)
	}
	if slices.Contains(p.CommittedBodies, t1.ID) {
		t.Errorf("t1's foreign edit must not be committed: %v", p.CommittedBodies)
	}
	// t-5f43: the sync pushed but is NOT complete (a body is pending). The SAME
	// stdout summary that claims the push must name that, and --json exposes it as
	// complete=false — the incompleteness can't hide on a separate stream.
	if p.Complete {
		t.Errorf("Complete must be false while a body is pending: %+v", p)
	}
	if !strings.Contains(p.SyncSummary(), "pending_bodies=1") {
		t.Errorf("SyncSummary must name the pending count on the success line, got %q", p.SyncSummary())
	}

	// Explicit opt-in (-b) commits the named body and clears the pending nudge.
	p2, err := a.Sync(context.Background(), SyncOpts{Bodies: []string{t1.ID}})
	if err != nil {
		t.Fatalf("opt-in sync: %v", err)
	}
	if !slices.Contains(p2.CommittedBodies, t1.ID) || len(p2.PendingBodies) != 0 {
		t.Errorf("opt-in sync: committed=%v pending=%v; want t1 committed, none pending", p2.CommittedBodies, p2.PendingBodies)
	}
	if got := strings.TrimSpace(runGitT(t, git, cloneA, "status", "--porcelain", "--", bodySpec)); got != "" {
		t.Errorf("t1 body must be clean after the opt-in sync, status: %q", got)
	}
	// Nothing pending now → complete, and the summary drops the pending clause.
	if !p2.Complete {
		t.Errorf("Complete must be true once nothing is pending: %+v", p2)
	}
	if strings.Contains(p2.SyncSummary(), "pending_bodies") {
		t.Errorf("a complete sync's summary must not mention pending_bodies, got %q", p2.SyncSummary())
	}
}

// A plain sync (no --message) commits under the gitmoji-grammar default,
// `:card_file_box:(board) sync via furrow`. The literal is pinned here on
// purpose: the previous default carried the retired Conventional
// `chore(board):` token, which glyph lint rejects — a latent exit-3 for every
// sync the moment a commit-msg hook lands on a board repo.
func TestSyncDefaultMessageGrammar(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	subject := strings.TrimSpace(runGitT(t, git, cloneA, "log", "-1", "--format=%s"))
	if subject != ":card_file_box:(board) sync via furrow" {
		t.Errorf("subject = %q, want %q", subject, ":card_file_box:(board) sync via furrow")
	}
}

// --message overrides the default auto-commit message.
func TestSyncMessageOverride(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{Message: ":card_file_box:(board) custom words"}); err != nil {
		t.Fatal(err)
	}
	subject := strings.TrimSpace(runGitT(t, git, cloneA, "log", "-1", "--format=%s"))
	if subject != ":card_file_box:(board) custom words" {
		t.Errorf("subject = %q", subject)
	}
}

// A context cancelled before/mid-sync (a Ctrl-C / SIGTERM) surfaces as one clean
// "sync-interrupted" error — NOT the misleading "not a git repository" that a
// cancelled rev-parse in Open would otherwise be classified as, nor a raw
// "git fetch: (no output)" from a killed subprocess. The progress object still
// reports how far the sync got.
func TestSyncInterruptedByCancelledContext(t *testing.T) {
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	if _, err := a.Add("work", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: every git subprocess dies immediately

	p, err := a.Sync(ctx, SyncOpts{})
	if err == nil {
		t.Fatal("a cancelled sync must return an error")
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncInterrupted {
		t.Fatalf("err = %v, want *core.Error id \"sync-interrupted\"", err)
	}
	if p.Pushed {
		t.Errorf("progress must not report pushed on an interrupted sync: %+v", p)
	}
}

// The pre-allowlist partition rule was "everything that is not a body is
// machine-written", which happily published an editor's swap file, a backup ~,
// and a crashed atomicWrite's .tmp-* — and a commit cannot be un-published.
// The allowlist leaves them in the working tree and disclosures ride in
// foreign_files + the summary line (t-4dgc pinned all three shapes).
func TestSyncSkipsForeignFilesInStore(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	t1, err := a.Add("task one", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	junk := []string{
		".furrow/bodies/." + t1.ID + ".md.swp",
		".furrow/bodies/" + t1.ID + ".md~",
		".furrow/tasks/.tmp-999999",
	}
	for _, f := range junk {
		if err := os.WriteFile(filepath.Join(cloneA, f), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A real mutation alongside, so the sync has something legitimate to commit.
	t2, err := a.Add("task two", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}

	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !p.Committed || !p.Pushed {
		t.Fatalf("the legitimate shard must still commit and push: %+v", p)
	}
	if !slices.Contains(p.CommittedBodies, t2.ID) {
		t.Errorf("t2's new body must ride along: %v", p.CommittedBodies)
	}
	// The junk is disclosed, sorted, and NOT in the commit or the index.
	want := append([]string(nil), junk...)
	sort.Strings(want)
	if !slices.Equal(p.ForeignFiles, want) {
		t.Errorf("ForeignFiles = %v; want %v", p.ForeignFiles, want)
	}
	committed := runGitT(t, git, cloneA, "show", "--name-only", "--format=", "HEAD")
	tracked := runGitT(t, git, cloneA, "ls-files")
	for _, f := range junk {
		if strings.Contains(committed, filepath.Base(f)) {
			t.Errorf("%s must not be in the sync commit:\n%s", f, committed)
		}
		if strings.Contains(tracked, filepath.Base(f)) {
			t.Errorf("%s must not be tracked at all:\n%s", f, tracked)
		}
		if strings.TrimSpace(runGitT(t, git, cloneA, "status", "--porcelain", "--", f)) == "" {
			t.Errorf("%s must remain dirty in the working tree (not swept, not deleted)", f)
		}
	}
	// Foreign junk is not board content: the sync is still complete, but the
	// summary line names the skip on the same line that claims success.
	if !p.Complete {
		t.Errorf("Complete must stay true — foreign files are not board content: %+v", p)
	}
	if !strings.Contains(p.SyncSummary(), "foreign_files=3") {
		t.Errorf("summary must name the skip, got %q", p.SyncSummary())
	}
}

// startConflictedCherryPick leaves dir mid-cherry-pick on a conflicted plain
// file: CHERRY_PICK_HEAD exists and f.txt carries markers. The three-stage
// failure this pins (t-e381): MidOperation used to probe only rebase/merge, so
// sync (1) misdiagnosed the state as sync-unmerged with a misleading stash
// remedy, (2) after a `git add` fell through to a raw exit-3
// "cannot do a partial commit during a cherry-pick" — having staged the BOARD
// files, which (3) `git cherry-pick --continue` then absorbed into the
// operator's foreign commit.
func startConflictedCherryPick(t *testing.T, git, dir string) {
	t.Helper()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitT(t, git, dir, "checkout", "-q", "-b", "side")
	write("f.txt", "side\n")
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-qm", "side")
	runGitT(t, git, dir, "checkout", "-q", "main")
	write("f.txt", "main\n")
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-qm", "main")
	cmd := exec.Command(git, "cherry-pick", "side")
	cmd.Dir = dir
	_ = cmd.Run() // conflicts; CHERRY_PICK_HEAD left behind
}

func TestSyncRefusesMidCherryPickAndStagesNothing(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	startConflictedCherryPick(t, git, cloneA)
	// A board mutation the sync would want to commit (new, untracked files —
	// created mid-operation, exactly the agent-walks-in scenario).
	a := openBoard(t, cloneA)
	if _, err := a.Add("task one", AddOpts{}); err != nil {
		t.Fatal(err)
	}

	// Stage 1: conflicted cherry-pick. Refused as the classified op-in-progress
	// (exit 2), naming the operation and its exact way out — never the
	// stash-shaped sync-unmerged wording, never a raw git relay.
	_, err := a.Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatal("sync mid-cherry-pick must be refused")
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncOpInProgress {
		t.Fatalf("kind = %v, want %s (err: %v)", fe, core.KindSyncOpInProgress, err)
	}
	if got := core.ExitCode(err); got != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", got, core.CodeValidation)
	}
	for _, want := range []string{"cherry-pick", "--continue", "--abort"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}

	// Stage 2: the operator resolves and `git add`s the conflicted file — index
	// clean, CHERRY_PICK_HEAD still present. Still the same classified refusal
	// (this exact state used to reach `git commit` and stage the board).
	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", "f.txt")
	_, err = a.Sync(context.Background(), SyncOpts{})
	fe = core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncOpInProgress {
		t.Fatalf("post-add kind = %v, want %s (err: %v)", fe, core.KindSyncOpInProgress, err)
	}
	// The refusal must have staged NOTHING: only the operator's own f.txt.
	if staged := strings.TrimSpace(runGitT(t, git, cloneA, "diff", "--cached", "--name-only")); staged != "f.txt" {
		t.Errorf("staged = %q, want only f.txt — board files must never enter the foreign operation", staged)
	}

	// Stage 3: the operator backs out; sync now proceeds and commits the board.
	runGitT(t, git, cloneA, "cherry-pick", "--abort")
	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync after abort: %v", err)
	}
	if !p.Committed || !p.Pushed {
		t.Errorf("board must commit and push once the operation is gone: %+v", p)
	}
	// And the board commit is furrow's own, not a cherry-pick absorption.
	if sub := strings.TrimSpace(runGitT(t, git, cloneA, "log", "-1", "--format=%s")); sub != DefaultSyncMessage {
		t.Errorf("HEAD subject = %q, want the sync default %q", sub, DefaultSyncMessage)
	}
}

// The unattended path has the same pre-flight: a mutation during someone's
// cherry-pick must not stage the board into it — autocommit warns and skips.
func TestAutoCommitSkipsMidOperation(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	startConflictedCherryPick(t, git, cloneA)

	a := openBoard(t, cloneA)
	a.AutoCommit = true
	if _, err := a.Add("task one", AddOpts{}); err != nil {
		t.Fatal(err) // the mutation itself must survive
	}
	res := a.AutoCommitFlush(context.Background(), "add", nil)
	if !res.Attempted || res.Committed {
		t.Fatalf("flush must attempt and skip: %+v", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "cherry-pick") {
		t.Errorf("the skip must be disclosed naming the operation: %v", res.Warnings)
	}
	// f.txt legitimately sits in the index (the conflict's unmerged entries);
	// what must NOT be there is anything of the board's.
	if staged := runGitT(t, git, cloneA, "diff", "--cached", "--name-only"); strings.Contains(staged, ".furrow") {
		t.Errorf("autocommit staged board files during a foreign operation:\n%s", staged)
	}
}
