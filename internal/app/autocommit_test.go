package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// These are real-git tests for post-mutation autocommit (AutoCommitFlush). They
// build a STANDALONE board — a git repo whose direct child is the .furrow — the
// layout the feature targets, then drive the public App API and assert what
// actually landed in git. gittest.Isolate (TestMain) neutralizes the developer's
// git config, so commits never flake on a missing identity or a stray gpgsign.

// setupACBoard builds a standalone board: `git init` at dir, `Init` its .furrow,
// and commit the empty board so later bodies are TRACKED (the interesting case
// for autocommit's untracked-vs-tracked body rule). Returns the git binary and
// the board's enclosing dir.
func setupACBoard(t *testing.T) (git, dir string) {
	t.Helper()
	git = gitOrSkip(t)
	dir = t.TempDir()
	runGitT(t, git, dir, "init", "-q", "-b", "main")
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, dir, "add", "-A")
	runGitT(t, git, dir, "commit", "-q", "-m", "board")
	return git, dir
}

// openAC opens the board with autocommit forced on (as a user-config [[board]]
// autocommit=true would). It is opened FRESH per logical command so bodiesTouched
// reflects only that command, exactly as separate furrow processes would.
func openAC(t *testing.T, dir string) *App {
	t.Helper()
	a := openBoard(t, dir)
	a.AutoCommit = true
	return a
}

func headCount(t *testing.T, git, dir string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(runGitT(t, git, dir, "rev-list", "--count", "HEAD")))
	if err != nil {
		t.Fatalf("rev-list --count HEAD: %v", err)
	}
	return n
}

// commitFiles lists the paths HEAD's commit touched (added, modified, OR
// deleted). --no-renames is load-bearing: archive moves a shard/body to
// identical content under archive/, which git would otherwise rename-detect and
// collapse to just the destination — hiding the hot-side deletion the archive
// test is specifically checking landed in the same commit.
func commitFiles(t *testing.T, git, dir string) []string {
	t.Helper()
	var fs []string
	for _, l := range strings.Split(strings.TrimSpace(runGitT(t, git, dir, "show", "--name-only", "--no-renames", "--format=", "HEAD")), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			fs = append(fs, l)
		}
	}
	return fs
}

func statusPorcelain(t *testing.T, git, dir string) string {
	t.Helper()
	return strings.TrimSpace(runGitT(t, git, dir, "status", "--porcelain"))
}

// A mutating command on an opted-in board produces exactly one commit carrying
// the shard AND the new (untracked) body, and leaves the board clean.
func TestAutoCommitAddOneCommit(t *testing.T) {
	git, dir := setupACBoard(t)
	before := headCount(t, git, dir)

	a := openAC(t, dir)
	task, err := a.Add("hello", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	res := a.AutoCommitFlush(context.Background(), "add", []string{task.ID})
	if !res.Committed {
		t.Fatalf("want Committed, got %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	if got := headCount(t, git, dir) - before; got != 1 {
		t.Errorf("want exactly 1 new commit, got %d", got)
	}
	if s := statusPorcelain(t, git, dir); s != "" {
		t.Errorf("board must be clean after autocommit:\n%s", s)
	}
	files := commitFiles(t, git, dir)
	for _, want := range []string{".furrow/tasks/" + task.ID + ".json", ".furrow/bodies/" + task.ID + ".md"} {
		if !slices.Contains(files, want) {
			t.Errorf("commit files = %v, want it to contain %s", files, want)
		}
	}
	if subj := strings.TrimSpace(runGitT(t, git, dir, "log", "-1", "--format=%s")); !strings.Contains(subj, "furrow add "+task.ID) {
		t.Errorf("subject = %q, want it to name `furrow add %s`", subj, task.ID)
	}
}

// A board that did NOT opt in never commits: the mutation lands on disk and is
// left uncommitted (byte-identical to furrow's pre-autocommit behavior).
func TestAutoCommitDisabledNeverCommits(t *testing.T) {
	git, dir := setupACBoard(t)
	before := headCount(t, git, dir)

	a := openBoard(t, dir) // AutoCommit stays false (a local board reads no user-config opt-in)
	if _, err := a.Add("hello", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	res := a.AutoCommitFlush(context.Background(), "add", nil)
	if res.Attempted || res.Committed {
		t.Errorf("a board without autocommit must not attempt/commit: %+v", res)
	}
	if headCount(t, git, dir) != before {
		t.Errorf("no commit expected when autocommit is off")
	}
	if statusPorcelain(t, git, dir) == "" {
		t.Errorf("expected the uncommitted add to show as dirty")
	}
}

// Option 3: a `note` on an existing (tracked) task commits its OWN body prose —
// the whole point of the feature on a standalone board.
func TestAutoCommitNoteCommitsOwnBody(t *testing.T) {
	git, dir := setupACBoard(t)

	a := openAC(t, dir)
	task, err := a.Add("hello", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.AutoCommitFlush(context.Background(), "add", []string{task.ID}) // body now tracked

	a2 := openAC(t, dir) // fresh process: appends to the tracked body
	if _, err := a2.AddNote(task.ID, "progress marker XYZ"); err != nil {
		t.Fatal(err)
	}
	res := a2.AutoCommitFlush(context.Background(), "note", []string{task.ID})
	if !res.Committed {
		t.Fatalf("note must autocommit its own body edit: %+v", res)
	}
	if body := runGitT(t, git, dir, "show", "HEAD:.furrow/bodies/"+task.ID+".md"); !strings.Contains(body, "progress marker XYZ") {
		t.Errorf("committed body must contain the note prose; got:\n%s", body)
	}
	if s := statusPorcelain(t, git, dir); s != "" {
		t.Errorf("board must be clean after note autocommit:\n%s", s)
	}
}

// A co-located operator's untouched tracked-dirty body is NEVER swept in: this
// session's shard commit leaves the other session's WIP prose pending.
func TestAutoCommitLeavesOtherSessionBody(t *testing.T) {
	git, dir := setupACBoard(t)

	a := openAC(t, dir)
	task, err := a.Add("hello", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.AutoCommitFlush(context.Background(), "add", []string{task.ID})

	// Another session hand-edits the body (tracked+dirty), outside this process.
	bodyPath := filepath.Join(dir, ".furrow", "bodies", task.ID+".md")
	orig, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodyPath, append(orig, []byte("\nWIP from another session\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	// This session only moves the task (touches the shard, not the body).
	a2 := openAC(t, dir)
	if _, err := a2.Move(task.ID, "backlog"); err != nil {
		t.Fatal(err)
	}
	res := a2.AutoCommitFlush(context.Background(), "move", []string{task.ID})
	if !res.Committed {
		t.Fatalf("the shard move should commit: %+v", res)
	}
	if !slices.Contains(res.PendingBodies, task.ID) {
		t.Errorf("the other session's dirty body must be reported pending, got %+v", res.PendingBodies)
	}
	if committed := runGitT(t, git, dir, "show", "HEAD:.furrow/bodies/"+task.ID+".md"); strings.Contains(committed, "WIP from another session") {
		t.Errorf("autocommit must not sweep another session's WIP body into the commit")
	}
	if !strings.Contains(statusPorcelain(t, git, dir), "bodies/"+task.ID+".md") {
		t.Errorf("the other session's WIP body should remain uncommitted/dirty")
	}
}

// archive moves a task across two stores (hot -> archive/) with deletions on one
// side and additions on the other; the whole move must land in ONE commit so the
// backup is never a half-move.
func TestAutoCommitArchiveSingleCommit(t *testing.T) {
	git, dir := setupACBoard(t)

	a := openAC(t, dir)
	task, err := a.Add("done soon", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.AutoCommitFlush(context.Background(), "add", []string{task.ID})

	a2 := openAC(t, dir)
	if _, err := a2.Done(task.ID); err != nil {
		t.Fatal(err)
	}
	a2.AutoCommitFlush(context.Background(), "done", []string{task.ID})

	before := headCount(t, git, dir)
	a3 := openAC(t, dir)
	if _, err := a3.ArchiveIDs([]string{task.ID}, false); err != nil {
		t.Fatal(err)
	}
	res := a3.AutoCommitFlush(context.Background(), "archive", []string{task.ID})
	if !res.Committed {
		t.Fatalf("archive should autocommit: %+v", res)
	}
	if got := headCount(t, git, dir) - before; got != 1 {
		t.Errorf("archive must be exactly one commit, got %d", got)
	}
	if s := statusPorcelain(t, git, dir); s != "" {
		t.Errorf("board must be clean after archive autocommit:\n%s", s)
	}
	files := commitFiles(t, git, dir)
	for _, want := range []string{
		".furrow/tasks/" + task.ID + ".json",         // deleted from the hot store
		".furrow/bodies/" + task.ID + ".md",          // deleted from the hot store
		".furrow/archive/tasks/" + task.ID + ".json", // added to the archive store
		".furrow/archive/bodies/" + task.ID + ".md",  // added to the archive store
	} {
		if !slices.Contains(files, want) {
			t.Errorf("archive commit missing %s; files=%v", want, files)
		}
	}
}

// Best-effort: a board outside any git repository never errors — it warns and
// leaves the mutation on disk (a non-zero exit would make an agent retry and
// double-apply).
func TestAutoCommitNonGitWarnsNoError(t *testing.T) {
	gitOrSkip(t)
	dir := t.TempDir() // no `git init`
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	a := openBoard(t, dir)
	a.AutoCommit = true
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	res := a.AutoCommitFlush(context.Background(), "add", nil)
	if res.Committed {
		t.Errorf("a non-git board must not commit")
	}
	if len(res.Warnings) == 0 {
		t.Errorf("a non-git board must warn, got no warnings")
	}
}

// Ownership guard: when the board is nested inside an UNRELATED enclosing repo
// (the standalone recipe's classic slip — forgetting `git init` in the board's
// own dir), autocommit refuses rather than dropping board commits into that repo.
func TestAutoCommitOwnershipGuardSkipsEnclosingRepo(t *testing.T) {
	git := gitOrSkip(t)
	dir := t.TempDir()
	runGitT(t, git, dir, "init", "-q", "-b", "main") // enclosing repo at dir
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(sub); err != nil { // board at dir/sub/.furrow; no git init in sub
		t.Fatal(err)
	}
	a := openBoard(t, sub)
	a.AutoCommit = true
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	res := a.AutoCommitFlush(context.Background(), "add", nil)
	if res.Committed {
		t.Errorf("a board nested in an enclosing repo must be skipped, not committed")
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "unrelated") {
		t.Errorf("expected an ownership warning naming the unrelated repo, got %v", res.Warnings)
	}
	if s := statusPorcelain(t, git, dir); !strings.Contains(s, "sub/") {
		t.Errorf("the board files should be left UNcommitted in the enclosing repo, status:\n%s", s)
	}
}

// A clean tree (e.g. a `set` that changed no bytes) is a silent no-op: no
// commit, no warning.
func TestAutoCommitCleanTreeSilent(t *testing.T) {
	git, dir := setupACBoard(t)
	before := headCount(t, git, dir)

	a := openAC(t, dir)
	res := a.AutoCommitFlush(context.Background(), "set", nil) // nothing was mutated
	if res.Committed {
		t.Errorf("a clean tree must not create a commit")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("a clean tree must be silent, got warnings: %v", res.Warnings)
	}
	if headCount(t, git, dir) != before {
		t.Errorf("no new commit expected on a clean tree")
	}
}

// A body carrying conflict markers is refused (best-effort: warn + skip that one
// body, still commit the rest) — a published commit cannot be un-published.
func TestAutoCommitSkipsConflictMarkerBody(t *testing.T) {
	git, dir := setupACBoard(t)

	a := openAC(t, dir)
	task, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.AutoCommitFlush(context.Background(), "add", []string{task.ID})

	a2 := openAC(t, dir)
	marker := "<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> other"
	if _, err := a2.AddNote(task.ID, marker); err != nil {
		t.Fatal(err)
	}
	res := a2.AutoCommitFlush(context.Background(), "note", []string{task.ID})
	if !strings.Contains(strings.Join(res.Warnings, " "), "conflict marker") {
		t.Errorf("expected a conflict-marker warning, got %v", res.Warnings)
	}
	if !strings.Contains(statusPorcelain(t, git, dir), "bodies/"+task.ID+".md") {
		t.Errorf("a marker-carrying body must be left uncommitted")
	}
	if committed := runGitT(t, git, dir, "show", "HEAD:.furrow/bodies/"+task.ID+".md"); strings.Contains(committed, "<<<<<<<") {
		t.Errorf("a conflict-marker body must never be committed")
	}
}

// The full config -> resolveGlobalBoard -> App wiring: a user-config [[board]]
// with autocommit=true lands on App.AutoCommit; an omitted key defaults false.
func TestAutoCommitResolvesFromUserConfig(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, "[[board]]\npath = \""+board+"\"\nscopes = [\""+scope+"\"]\nrepo = \"\"\nautocommit = true\n")
	work := filepath.Join(scope, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "user-config" {
		t.Fatalf("Source = %q, want user-config", a.Source)
	}
	if !a.AutoCommit {
		t.Errorf("a.AutoCommit = false, want true from [[board]] autocommit=true")
	}
}

func TestAutoCommitDefaultsFalseFromUserConfig(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	board := mustInitBoard(t, filepath.Join(scope, "projects"))
	writeGlobalConfig(t, "[[board]]\npath = \""+board+"\"\nscopes = [\""+scope+"\"]\nrepo = \"\"\n") // no autocommit key
	work := filepath.Join(scope, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	if a.AutoCommit {
		t.Errorf("a.AutoCommit = true, want false when autocommit is omitted")
	}
}
