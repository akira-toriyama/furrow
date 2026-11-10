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
// committed, naming the bodies -b can name.
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
// hit this was. A body no task or epic owns (lint's orphan-body) is named by
// its file the same way, modified or new. The task body nobody named stays
// pending.
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
	bodies := filepath.Join(cloneA, DirName, "bodies")
	if err := os.WriteFile(filepath.Join(bodies, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	const edited = "# edited by hand\n"
	for _, id := range []string{e.ID, x.ID, "notes", "draft"} {
		if err := os.WriteFile(filepath.Join(bodies, id+".md"), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	named := []string{e.ID, "notes", "draft"}
	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: named})
	if err != nil {
		t.Fatalf("-b %v = %v (progress %+v); want every named body published", named, err, p)
	}
	if !slices.Equal(p.CommittedBodies, []string{"draft", e.ID, "notes"}) || !slices.Equal(p.PendingBodies, []string{x.ID}) || !p.Pushed {
		t.Errorf("progress = %+v; want %v committed and pushed, %s left pending", p, named, x.ID)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range named {
		if got, err := os.ReadFile(filepath.Join(cloneB, DirName, "bodies", id+".md")); err != nil || string(got) != edited {
			t.Errorf("the other clone reads %s as %q (%v); want the edit", id, got, err)
		}
	}
}

// The guard refuses only an id that names neither a task nor a body file. An
// id it accepts must not end the check before a later typo, and a body is
// named by its exact file stem: an epic's title, an id prefix or another case
// would commit nothing and leave the meant body pending, the silent ignore the
// guard exists to stop. The candidates are the dirty bodies a sync would leave
// behind, so a new body, committed anyway, is not one.
func TestSyncBodyRefusesOnlyAnIDThatNamesNothing(t *testing.T) {
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
	bodies := filepath.Join(cloneA, DirName, "bodies")
	if err := os.WriteFile(filepath.Join(bodies, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"notes.md": "# notes\n\nedited by hand\n", "draft.md": "# draft\n"} {
		if err := os.WriteFile(filepath.Join(bodies, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prefix, upper := e.ID[:len(e.ID)-1], strings.ToUpper(e.ID)
	ids := []string{x.ID, e.ID, "notes", "box", prefix, upper}
	p, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: ids})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindValidation || fe.Subject != "box" || !strings.Contains(fe.Msg, prefix+", "+upper) {
		t.Fatalf("-b %v = %+v; want a validation refusal naming box, %s and %s", ids, err, prefix, upper)
	}
	if !slices.Equal(fe.Candidates, []string{"notes"}) {
		t.Errorf("candidates = %v; want the one body left pending, notes", fe.Candidates)
	}
	if p.Committed || p.Pushed {
		t.Errorf("progress = %+v; want nothing committed", p)
	}
}

// Whether a named body is dirty does not decide the guard. A sync that
// committed it and then lost the push (retryable) is recovered by re-running
// the same command, and by then the body is clean: a guard that took a body
// no record owns only while dirty refused that retry as a validation error and
// left the commit unpushed.
func TestSyncBodyRetryAfterALostPushNamesTheSameBodies(t *testing.T) {
	t.Parallel()
	git, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(cloneA, DirName, "bodies", "notes.md")
	if err := os.WriteFile(notes, []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{notes, filepath.Join(cloneA, DirName, "bodies", e.ID+".md")} {
		if err := os.WriteFile(f, []byte("# edited by hand\n"), 0o644); err != nil {
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

	opts := SyncOpts{Bodies: []string{e.ID, "notes"}}
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
