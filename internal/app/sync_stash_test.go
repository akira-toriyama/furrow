package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// seedTrackedFile commits a file OUTSIDE .furrow in clone A and pushes it, then
// pulls it into clone B — the setup for an autostash: a file sync will never
// commit, but git must stash to rebase over it.
func seedTrackedFile(t *testing.T, git, cloneA, cloneB, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cloneA, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "add", name)
	runGitT(t, git, cloneA, "commit", "-q", "-m", "seed "+name)
	runGitT(t, git, cloneA, "push", "-q")
	runGitT(t, git, cloneB, "pull", "-q", "--rebase")
}

// The defect this whole path exists for. `git rebase --autostash` re-applies the
// stash at the END of a SUCCESSFUL rebase; when that apply conflicts with what was
// just pulled, git keeps the changes in the stash, warns on stderr, and EXITS 0.
// Nothing in an exit code, and nothing in a rebase-in-progress probe, can see it —
// the operator's dirty files are simply gone from the working tree.
//
// (Note the failure is NOT on the abort path, where the tree is restored to
// exactly the state the stash was taken from and the re-apply cannot conflict.)
func TestSyncReportsStrandedAutostashOnCleanRebase(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)
	seedTrackedFile(t, git, cloneA, cloneB, "notes.md", "base\n")

	// A moves notes.md upstream.
	if err := os.WriteFile(filepath.Join(cloneA, "notes.md"), []byte("from A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "commit", "-q", "-am", "A edits notes")
	runGitT(t, git, cloneA, "push", "-q")

	// B has its own uncommitted edit to the same file (sync never commits it — it
	// is outside .furrow) plus a board change to sync.
	if err := os.WriteFile(filepath.Join(cloneB, "notes.md"), []byte("from B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := openBoard(t, cloneB)
	if _, err := b.Add("from B", AddOpts{}); err != nil {
		t.Fatal(err)
	}

	p, err := b.Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatalf("sync must NOT report success while the operator's changes sit in a stash (progress %+v)", p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.ID != "sync-stash-stranded" {
		t.Fatalf("want id sync-stash-stranded, got %v", err)
	}
	if fe.Code != core.CodeInternal {
		t.Errorf("sync-stash-stranded is exit 3 (retryable once the stash is popped), got code %d", fe.Code)
	}
	if !p.Pulled {
		t.Errorf("the rebase DID succeed — progress must say so: %+v", p)
	}
	if p.Pushed {
		t.Errorf("nothing may be pushed while the working tree is missing the operator's edits: %+v", p)
	}
	if len(p.PendingStash) != 1 {
		t.Fatalf("want exactly one stranded autostash entry, got %+v", p.PendingStash)
	}
	st := p.PendingStash[0]
	if st.Commit == "" {
		t.Error("the stash commit oid is the stable handle (stash@{N} shifts) — it must be reported")
	}
	if !containsStr(st.Paths, "notes.md") {
		t.Errorf("the stash paths answer \"did it eat my edit?\" — want notes.md, got %v", st.Paths)
	}
	// The machine-actionable half: an agent branches on details, never on prose.
	d, ok := fe.Details.(map[string]any)
	if !ok || d["pending_stash"] == nil {
		t.Errorf("details must carry pending_stash, got %#v", fe.Details)
	}
	// And git really did keep it: the entry is still there to be popped.
	if out := runGitT(t, git, cloneB, "stash", "list"); !strings.Contains(out, "autostash") {
		t.Errorf("the autostash entry must still exist for `git stash pop`: %q", out)
	}
}

// The state a stranded autostash leaves behind: git merged what it could into the
// working tree and left unmerged entries in the index. Every later git that touches
// the index now fails — with git's own opaque wording, which mentions neither the
// stash nor the fix. The next sync must not just relay that.
func TestSyncPreflightExplainsUnmergedIndex(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)
	seedTrackedFile(t, git, cloneA, cloneB, "notes.md", "base\n")

	if err := os.WriteFile(filepath.Join(cloneA, "notes.md"), []byte("from A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneA, "commit", "-q", "-am", "A edits notes")
	runGitT(t, git, cloneA, "push", "-q")

	if err := os.WriteFile(filepath.Join(cloneB, "notes.md"), []byte("from B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := openBoard(t, cloneB)
	if _, err := b.Add("from B", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	// The first sync strands the stash and leaves notes.md unmerged.
	if _, err := b.Sync(context.Background(), SyncOpts{}); err == nil {
		t.Fatal("expected the stranding sync to fail")
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	fe := core.AsError(err)
	if fe == nil || fe.ID != "sync-unmerged" {
		t.Fatalf("the follow-up sync must name the state it is in, not relay git's \"unmerged\"; got %v", err)
	}
	if fe.Code != core.CodeValidation {
		t.Errorf("resolving the markers is an edit, not a re-run — exit 2, got code %d", fe.Code)
	}
	if len(p.PendingStash) != 1 {
		t.Errorf("the stash holding the other half must be named again, got %+v", p.PendingStash)
	}
	d, ok := fe.Details.(map[string]any)
	if !ok || d["paths"] == nil || d["pending_stash"] == nil {
		t.Errorf("details must carry both the unmerged paths and the stash, got %#v", fe.Details)
	}
}

// A pre-existing autostash (stranded by an EARLIER sync, or another machine) is
// reported on every sync — it sits there silently until someone pops it — but it
// does not fail a sync that did nothing wrong.
func TestSyncReportsPreExistingAutostashWithoutFailing(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)
	seedTrackedFile(t, git, cloneA, cloneB, "notes.md", "base\n")

	// Plant the leftover the way git does: store a stash entry under its own
	// "autostash" reflog subject.
	if err := os.WriteFile(filepath.Join(cloneB, "notes.md"), []byte("stranded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(runGitT(t, git, cloneB, "stash", "create"))
	runGitT(t, git, cloneB, "checkout", "--", "notes.md")
	runGitT(t, git, cloneB, "stash", "store", "-m", "autostash", oid)

	b := openBoard(t, cloneB)
	if _, err := b.Add("unrelated work", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("a leftover from an earlier sync must not fail this one: %v (progress %+v)", err, p)
	}
	if !p.Pushed {
		t.Errorf("the sync itself was fine and must complete: %+v", p)
	}
	if len(p.PendingStash) != 1 || !containsStr(p.PendingStash[0].Paths, "notes.md") {
		t.Errorf("a leftover autostash must stay VISIBLE on every sync until it is popped, got %+v", p.PendingStash)
	}
}

// An operator's own `git stash` is theirs — sync must not nag about it. Only the
// entries git stored on furrow's behalf (subject "autostash") are furrow's to report.
func TestSyncIgnoresOperatorsOwnStash(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)
	seedTrackedFile(t, git, cloneA, cloneB, "notes.md", "base\n")

	if err := os.WriteFile(filepath.Join(cloneB, "notes.md"), []byte("my wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, git, cloneB, "stash", "push", "-q", "-m", "my own wip")

	b := openBoard(t, cloneB)
	if _, err := b.Add("unrelated work", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := b.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(p.PendingStash) != 0 {
		t.Errorf("an operator's own stash is not furrow's business: %+v", p.PendingStash)
	}
}

// The prevention half: a body still carrying conflict markers is never
// auto-committed. This is the exact wreckage a stranded autostash leaves behind
// (git's failed re-apply writes the merge into the working tree), and the next sync
// used to publish it — a commit cannot be un-published.
func TestSyncRefusesToCommitBodyWithConflictMarkers(t *testing.T) {
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	task, err := a.Add("a task whose body got half-merged", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Commit the clean body first, so the marked-up version is a MODIFIED body
	// (the -b path) rather than a new one.
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	body := "# a task\n\n<<<<<<< Updated upstream\n- [x] done\n=======\n- [ ] mid-sentence\n>>>>>>> Stashed changes\n"
	if err := a.Store.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}

	p, err := a.Sync(context.Background(), SyncOpts{Bodies: []string{task.ID}})
	fe := core.AsError(err)
	if fe == nil || fe.ID != "body-conflict-marker" {
		t.Fatalf("want id body-conflict-marker, got %v (progress %+v)", err, p)
	}
	if fe.Code != core.CodeValidation {
		t.Errorf("the fix is an edit, not a re-run — exit 2, got code %d", fe.Code)
	}
	if p.Committed {
		t.Error("the guard runs BEFORE the commit: a refused sync must have changed nothing")
	}
	// The refusal must leave the body exactly where it was — dirty, and the
	// operator's to fix.
	if out := runGitT(t, git, cloneA, "status", "--porcelain"); !strings.Contains(out, core.BodyPath(task.ID)) {
		t.Errorf("the marked-up body must still be uncommitted: %q", out)
	}
	d, ok := fe.Details.(map[string]any)
	if !ok || d["bodies"] == nil {
		t.Errorf("details must name the offending bodies, got %#v", fe.Details)
	}
}

// The same guard on the OTHER path a body reaches a commit: a brand-new (untracked)
// body is auto-committed without any opt-in, so it must be checked too.
func TestSyncRefusesToCommitNewBodyWithConflictMarkers(t *testing.T) {
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	task, err := a.Add("never synced", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody(task.ID, "<<<<<<< Updated upstream\nx\n=======\ny\n>>>>>>> Stashed changes\n"); err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync(context.Background(), SyncOpts{})
	if fe := core.AsError(err); fe == nil || fe.ID != "body-conflict-marker" {
		t.Fatalf("an untracked body is committed with no opt-in, so it must be guarded too; got %v (progress %+v)", err, p)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
