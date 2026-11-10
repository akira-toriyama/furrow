package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
	"github.com/akira-toriyama/furrow/v10/internal/gitrepo"
	"github.com/akira-toriyama/furrow/v10/internal/gittest"
)

// A sync on a detached HEAD used to commit first and fail in the pull, with
// committed=true: checking a branch out then left that commit — the board's
// writes — behind. The pre-flight refuses before the commit, so the write
// stays in the working tree for the branch to pick up.
func TestSyncRefusesADetachedHeadBeforeCommitting(t *testing.T) {
	t.Parallel()
	git, cloneA, _ := setupClones(t)
	gittest.RunGit(t, git, cloneA, "checkout", "-q", "--detach")
	a := openBoard(t, cloneA)
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync(context.Background(), SyncOpts{})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncDetachedHead || fe.Code != core.CodeValidation {
		t.Fatalf("want sync-detached-head exit 2, got %+v (progress %+v)", err, p)
	}
	if p.Committed || p.Complete {
		t.Errorf("progress = %+v; want nothing committed and complete=false", p)
	}
	if strings.TrimSpace(gittest.RunGit(t, git, cloneA, "status", "--porcelain")) == "" {
		t.Error("the refused sync must leave the write in the working tree, not in an orphaned commit")
	}
}

// A branch with no upstream has nowhere to pull from or push to: the sync
// refuses before committing (exit 2, its own kind) instead of committing and
// relaying git's "no upstream configured" as git-failed exit 3.
func TestSyncRefusesABranchWithoutUpstreamBeforeCommitting(t *testing.T) {
	t.Parallel()
	git := gittest.GitOrSkip(t)
	dir := t.TempDir()
	gittest.RunGit(t, git, dir, "init", "-q", "-b", "main")
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@e"}} {
		gittest.RunGit(t, git, dir, "config", kv[0], kv[1])
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, dir, "add", "-A")
	gittest.RunGit(t, git, dir, "commit", "-q", "-m", "board")
	a := openBoard(t, dir)
	if _, err := a.Add("x", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync(context.Background(), SyncOpts{})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncNoUpstream || fe.Code != core.CodeValidation {
		t.Fatalf("want sync-no-upstream exit 2, got %+v (progress %+v)", err, p)
	}
	if p.Committed || p.Pulled || p.Pushed || p.Complete {
		t.Errorf("progress = %+v; want nothing committed", p)
	}
	if !strings.Contains(fe.Msg, "push -u") {
		t.Errorf("the message must name the remedy: %s", fe.Msg)
	}
}

// A store that IS the git toplevel — a board repo cloned as a repo's .furrow —
// has a pathspec of "." and git prints its paths bare, so the "<spec>/" prefix
// rule called every shard foreign and sync committed nothing.
func TestSyncCommitsAStoreThatIsTheGitToplevel(t *testing.T) {
	t.Parallel()
	git := gittest.GitOrSkip(t)
	origin := t.TempDir()
	gittest.RunGit(t, git, origin, "init", "-q", "--bare", "-b", "main")
	home := t.TempDir()
	store := filepath.Join(home, DirName)
	gittest.RunGit(t, git, home, "clone", "-q", origin, store)
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@e"}} {
		gittest.RunGit(t, git, store, "config", kv[0], kv[1])
	}
	if _, err := InitAt(store); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, store, "add", "-A")
	gittest.RunGit(t, git, store, "commit", "-q", "-m", "board")
	gittest.RunGit(t, git, store, "push", "-q", "-u", "origin", "main")

	a := openBoard(t, home)
	if a.Dir != store {
		t.Fatalf("opened %s, want the toplevel store %s", a.Dir, store)
	}
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync: %v (progress %+v)", err, p)
	}
	if !p.Committed || !p.Pushed || len(p.ForeignFiles) != 0 || !p.Complete {
		t.Errorf("progress = %+v; want the shard and its body committed and pushed, nothing foreign", p)
	}
	if out := gittest.RunGit(t, git, store, "ls-tree", "--name-only", "-r", "origin/main"); !strings.Contains(out, "tasks/"+x.ID+".json") || !strings.Contains(out, "bodies/"+x.ID+".md") {
		t.Errorf("the remote lacks the task:\n%s", out)
	}
}

// The pure half of the toplevel rule: with spec "." the bare paths classify
// exactly as ".furrow/..." paths do under spec ".furrow".
func TestPartitionSyncAndShardKindOfAcceptAToplevelStore(t *testing.T) {
	changes := []gitrepo.Change{{Path: "tasks/t-1.json"}, {Path: "bodies/t-1.md"}, {Path: "meta.json"}, {Path: "bodies/.swp"}}
	commit, committed, pending, foreign := partitionSync(".", changes, SyncOpts{})
	if strings.Join(commit, ",") != "tasks/t-1.json,meta.json" || len(committed) != 0 || strings.Join(pending, ",") != "t-1" || strings.Join(foreign, ",") != "bodies/.swp" {
		t.Errorf("partitionSync(\".\") = commit %v committed %v pending %v foreign %v", commit, committed, pending, foreign)
	}
	if kind, ok := shardKindOf(".", "archive/epics/e-1.json"); !ok || kind != core.ShardEpic {
		t.Errorf("shardKindOf(\".\", archive/epics/e-1.json) = %v %v; want the epic kind", kind, ok)
	}
	if _, ok := shardKindOf(".", "bodies/t-1.md"); ok {
		t.Error("a body is never a shard")
	}
}

// A -b id that names no task was silently ignored: the sync pushed, the body
// the operator meant stayed pending. It is refused before anything is
// committed, naming the modified bodies as candidates.
func TestSyncRefusesABodyIDThatNamesNoTask(t *testing.T) {
	t.Parallel()
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(cloneA, DirName, "bodies", x.ID+".md")
	if err := os.WriteFile(bodyPath, []byte("# x\n\nedited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: []string{"t-nope"}})
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation || fe.Kind != core.KindValidation {
		t.Fatalf("want a validation refusal, got %+v (progress %+v)", err, p)
	}
	if !slices.Equal(fe.Candidates, []string{x.ID}) || fe.Subject != "t-nope" {
		t.Errorf("candidates = %v subject = %q; want the one pending body %s named for t-nope", fe.Candidates, fe.Subject, x.ID)
	}
	if p.Committed || p.Pushed {
		t.Errorf("progress = %+v; want nothing committed", p)
	}
	if strings.TrimSpace(gittest.RunGit(t, git, cloneA, "status", "--porcelain")) == "" {
		t.Error("the refused sync must leave the edit in the working tree")
	}
}

// An epic keeps its prose in bodies/ beside the tasks' and is hand-edited the
// same way, so -b names it the same way: refusing its id left --all-bodies,
// which sweeps a co-located operator's WIP, as the only way to publish it
// (t-8k4t). The epic is closed, as the retired repo's box whose record first
// hit this was. The task body nobody named stays pending.
func TestSyncBodyNamesAnEpic(t *testing.T) {
	t.Parallel()
	_, cloneA, cloneB := setupClones(t)
	a := openBoard(t, cloneA)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicDone(e.ID); err != nil {
		t.Fatal(err)
	}
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	const edited = "# edited by hand\n"
	for _, id := range []string{e.ID, x.ID} {
		if err := os.WriteFile(filepath.Join(cloneA, DirName, "bodies", id+".md"), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: []string{e.ID}})
	if err != nil {
		t.Fatalf("-b %s = %v (progress %+v); want the epic body published", e.ID, err, p)
	}
	if !slices.Equal(p.CommittedBodies, []string{e.ID}) || !slices.Equal(p.PendingBodies, []string{x.ID}) || !p.Pushed {
		t.Errorf("progress = %+v; want %s committed and pushed, %s left pending", p, e.ID, x.ID)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(cloneB, DirName, "bodies", e.ID+".md")); err != nil || string(got) != edited {
		t.Errorf("the other clone reads %q (%v); want the edited epic body", got, err)
	}
}

// The guard takes a task or epic id exactly, whatever the state of its body
// file, missing included. An id it accepts must not end the check before a
// later typo, and a title, an id prefix or another case is refused:
// partitionSync matches -b against bodies/<id>.md by name, so a resolved ref
// would commit nothing and leave the meant body pending, the silent ignore the
// guard exists to stop. A body no record owns is refused too, new or modified,
// though a modified one is among the candidates: those are the modified
// bodies, so a new one is not.
func TestSyncBodyRefusesAnIDThatNamesNoTaskOrEpic(t *testing.T) {
	t.Parallel()
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	y, err := a.Add("y", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	bodies := filepath.Join(cloneA, DirName, "bodies")
	if err := os.WriteFile(filepath.Join(bodies, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{x.ID + ".md": "# x\n\nedited by hand\n", "notes.md": "# notes\n\nedited by hand\n", "draft.md": "# draft\n"} {
		if err := os.WriteFile(filepath.Join(bodies, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(bodies, y.ID+".md")); err != nil {
		t.Fatal(err)
	}

	refused := []string{"box", e.ID[:len(e.ID)-1], strings.ToUpper(e.ID), y.ID[:len(y.ID)-1], strings.ToUpper(y.ID), "notes", "draft"}
	ids := append([]string{y.ID, e.ID}, refused...)
	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: ids})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindValidation || fe.Subject != "box" || !strings.Contains(fe.Msg, strings.Join(refused, ", ")+" — ") {
		t.Fatalf("-b %v = %+v; want a validation refusal naming exactly %v", ids, err, refused)
	}
	if !slices.Equal(fe.Candidates, []string{"notes", x.ID}) {
		t.Errorf("candidates = %v; want the modified bodies, notes and %s", fe.Candidates, x.ID)
	}
	if p.Committed || p.Pushed {
		t.Errorf("progress = %+v; want nothing committed", p)
	}
}

// A task or epic id is accepted whatever its body's git state. A sync that
// committed the bodies and then lost the push (retryable) is recovered by
// re-running the same command, and by then the bodies are clean: a guard that
// keyed on the dirty set refused that retry as a validation error and left the
// commit unpushed. The rejecting pre-receive hook reads to sync as a lost push
// race (sync-push-rejected).
func TestSyncBodyRetryAfterALostPushNamesTheSameBodies(t *testing.T) {
	t.Parallel()
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{e.ID, x.ID} {
		if err := os.WriteFile(filepath.Join(cloneA, DirName, "bodies", id+".md"), []byte("# edited by hand\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	origin := strings.TrimSpace(gittest.RunGit(t, git, cloneA, "remote", "get-url", "origin"))
	marker := filepath.Join(t.TempDir(), "reject")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-receive"), []byte("#!/bin/sh\n[ -f '"+marker+"' ] && exit 1\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, origin, "config", "core.hooksPath", hooks)

	opts := SyncOpts{Bodies: []string{e.ID, x.ID}}
	p, err := openBoard(t, cloneA).Sync(context.Background(), opts)
	if fe := core.AsError(err); fe == nil || !fe.Retryable || !p.Committed || p.Pushed {
		t.Fatalf("first sync = %+v (progress %+v); want a retryable failure after the commit", err, p)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if p, err = openBoard(t, cloneA).Sync(context.Background(), opts); err != nil || !p.Pushed {
		t.Fatalf("the same sync re-run = %v (progress %+v); want the commit pushed", err, p)
	}
}

// A failed epic read is reported as itself. Swallowed, it turned a correct
// epic id into the -b refusal ("names no task or epic"), with that same id
// among the candidates.
func TestSyncBodyReportsAnUnreadableEpicShard(t *testing.T) {
	t.Parallel()
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := a.EpicAdd("bad", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneA, DirName, "bodies", e.ID+".md"), []byte("# edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneA, DirName, core.EpicPath(bad.ID)), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: []string{e.ID}})
	if fe := core.AsError(err); fe == nil || fe.Subject == e.ID || len(fe.Candidates) > 0 || strings.Contains(fe.Msg, "-b names") {
		t.Fatalf("-b %s over an unreadable epic shard = %+v; want the read error", e.ID, err)
	}
	if p.Committed {
		t.Errorf("progress = %+v; want nothing committed", p)
	}
}

// A body refused for conflict markers is left uncommitted exactly as a pending
// body is, so it is reported in pending_bodies — and a refused sync is never
// complete.
func TestSyncReportsAMarkedBodyAsPendingAndIncomplete(t *testing.T) {
	t.Parallel()
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(cloneA, DirName, "bodies", x.ID+".md")
	if err := os.WriteFile(bodyPath, []byte("# x\n\n<<<<<<< ours\nmine\n=======\ntheirs\n>>>>>>> theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: []string{x.ID}})
	if fe := core.AsError(err); fe == nil || fe.Kind != core.KindBodyConflictMarker {
		t.Fatalf("want body-conflict-marker, got %+v", err)
	}
	if !slices.Equal(p.PendingBodies, []string{x.ID}) || p.Complete {
		t.Errorf("progress = %+v; want the marked body pending and complete=false", p)
	}
}

// An unarchive is a shard that reappears under tasks/ while its archive/ copy
// goes: "created" sent the reader after a task they had already closed.
func TestSyncIncomingNamesAnUnarchive(t *testing.T) {
	t.Parallel()
	_, cloneA, cloneB := setupClones(t)
	a := openBoard(t, cloneA)
	x, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).ArchiveIDs([]string{x.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Incoming) != 1 || p.Incoming[0].Kind != incomingArchived {
		t.Fatalf("incoming after the archive = %+v; want one archived", p.Incoming)
	}
	if _, err := openBoard(t, cloneA).Unarchive([]string{x.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	p, err = openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Incoming) != 1 || p.Incoming[0].Kind != incomingUnarchived || p.Incoming[0].ID != x.ID {
		t.Errorf("incoming after the unarchive = %+v; want one unarchived %s", p.Incoming, x.ID)
	}
}

// The summary's id lists are omitted when empty — README's contract for the
// revisit key — never emitted as null.
func TestRevisitSummaryOmitsEmptyLists(t *testing.T) {
	data, err := json.Marshal(RevisitSummary{Stale: []string{"t-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "dep_done") || !strings.Contains(string(data), `"stale":["t-1"]`) {
		t.Errorf("summary JSON = %s; want dep_done omitted and stale kept", data)
	}
	if data, _ := json.Marshal(RevisitSummary{}); string(data) != "{}" {
		t.Errorf("an empty summary = %s; want {}", data)
	}
}

// The whole flow under a hook's environment: a sync run from another repo's
// git hook (GIT_DIR and friends pointing there) must still commit and push
// the BOARD's repo, and the other clone must see the write.
func TestSyncUnderAnotherReposHookEnvironmentPublishesTheBoard(t *testing.T) {
	git, cloneA, cloneB := setupClones(t)
	other := t.TempDir()
	gittest.RunGit(t, git, other, "init", "-q", "-b", "main")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	a := openBoard(t, cloneA)
	x, err := a.Add("from the hook", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil || !p.Pushed {
		t.Fatalf("sync under the hook's GIT_DIR: err=%v progress=%+v", err, p)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBoard(t, cloneB).Get(x.ID); err != nil {
		t.Errorf("the write never reached the board's remote: %v", err)
	}
}
