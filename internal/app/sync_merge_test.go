package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/akira-toriyama/furrow/v9/internal/gitrepo"
	"github.com/akira-toriyama/furrow/v9/internal/gittest"
)

// The t-v1fg repro, end to end against real git: the task-status bot closes a
// task (status / closed / updated) and, a second later, a session ticks the
// same task's checklist (checklist / updated). Every write advances `updated`,
// so the two shards conflict textually on that one line and the session's
// sync used to abort — stalling every later push from that checkout. Sync
// must merge the two at the field grain and push.

// mergeClones seeds one shared task with a checklist on A, pushed and pulled
// into B, and returns the two boards under fixed clocks one second apart.
func mergeClones(t *testing.T) (git, cloneA, cloneB, id string, t1, t2 time.Time) {
	t.Helper()
	git, cloneA, cloneB = setupClones(t)
	t1 = time.Date(2026, 10, 5, 4, 47, 41, 0, time.UTC)
	t2 = t1.Add(time.Second)

	a := openBoard(t, cloneA)
	shared, err := a.Add("shared", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddCheck(shared.ID, "step"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	return git, cloneA, cloneB, shared.ID, t1, t2
}

func boardAt(t *testing.T, dir string, now time.Time) *App {
	t.Helper()
	a := openBoard(t, dir)
	a.Clock = &fixedClock{t: now}
	return a
}

func shardBytes(t *testing.T, dir, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, DirName, "tasks", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSyncMergesShardsWrittenInDifferentFields(t *testing.T) {
	_, cloneA, cloneB, id, t1, t2 := mergeClones(t)

	// The bot closes the task on A and pushes; the session ticks the checklist
	// on B one second later and syncs into the collision.
	if _, err := boardAt(t, cloneA, t1).Done(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Check(id, 0, true); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		var details any
		if fe := core.AsError(err); fe != nil {
			details = fe.Details
		}
		t.Fatalf("disjoint shard writes must merge, not conflict: %v (details %v)", err, details)
	}
	shardPath := ".furrow/tasks/" + id + ".json"
	if !p.Pushed || p.Conflict || !p.Complete {
		t.Errorf("progress = %+v; want pushed=true conflict=false complete=true", p)
	}
	if len(p.MergedShards) != 1 || p.MergedShards[0] != shardPath {
		t.Errorf("merged_shards = %v; want [%s]", p.MergedShards, shardPath)
	}
	// The pull brought the bot's close in — the incoming report must say so.
	var closed bool
	for _, c := range p.Incoming {
		if c.ID == id && c.Kind == incomingClosed {
			closed = true
		}
	}
	if !closed {
		t.Errorf("incoming = %+v; want %s reported closed", p.Incoming, id)
	}

	tk, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "done" || tk.Closed == nil || !tk.Closed.Equal(t1) {
		t.Errorf("the bot's close was lost: status=%q closed=%v", tk.Status, tk.Closed)
	}
	if len(tk.Checklist) != 1 || !tk.Checklist[0].Done {
		t.Errorf("the session's tick was lost: %+v", tk.Checklist)
	}
	if !tk.Updated.Equal(t2) {
		t.Errorf("updated = %v; want the later stamp %v", tk.Updated, t2)
	}

	// What landed on disk is a canonical shard: re-marshalling it is a no-op.
	onDisk := shardBytes(t, cloneB, id)
	again, err := core.MarshalTask(tk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, again) {
		t.Errorf("merged shard is not in canonical form:\n%s", onDisk)
	}
	if bytes.Contains(onDisk, []byte("<<<<<<<")) {
		t.Errorf("merged shard carries conflict markers:\n%s", onDisk)
	}

	// A pulls the merge and the two clones agree byte for byte.
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shardBytes(t, cloneA, id), onDisk) {
		t.Errorf("clones diverge after the merge:\nA: %s\nB: %s", shardBytes(t, cloneA, id), onDisk)
	}
}

// Several unpushed local commits touching the same shard stop the rebase once
// per commit; the resolver finishes every stop and reports the shard once.
func TestSyncMergesAcrossSeveralLocalCommits(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)

	if _, err := boardAt(t, cloneA, t1).Done(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	// B: two separate commits, each advancing `updated` on the same shard.
	if _, err := boardAt(t, cloneB, t2).Check(id, 0, true); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, cloneB, "add", "-A")
	gittest.RunGit(t, git, cloneB, "commit", "-q", "-m", "tick")
	three := 3
	if _, err := boardAt(t, cloneB, t2.Add(time.Second)).SetValue(id, &three); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, cloneB, "add", "-A")
	gittest.RunGit(t, git, cloneB, "commit", "-q", "-m", "value")

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("two local commits on one shard must both merge: %v", err)
	}
	if !p.Pushed || p.Committed || len(p.MergedShards) != 1 {
		t.Errorf("progress = %+v; want pushed=true committed=false and the shard merged once", p)
	}
	tk, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "done" || !tk.Checklist[0].Done || tk.Value == nil || *tk.Value != 3 {
		t.Errorf("a side was lost across the stops: %+v", tk)
	}
	if !tk.Updated.Equal(t2.Add(time.Second)) {
		t.Errorf("updated = %v; want the latest stamp", tk.Updated)
	}
	if strings.TrimSpace(gittest.RunGit(t, git, cloneB, "status", "--porcelain")) != "" {
		t.Errorf("board must be clean after the merged rebase:\n%s", gittest.RunGit(t, git, cloneB, "status", "--porcelain"))
	}
}

// Both sides made the SAME change, the local one stamped earlier: the merge
// equals the upstream, so the replayed commit is empty and must be skipped,
// not refused by `rebase --continue`.
func TestSyncSkipsLocalCommitTheUpstreamAlreadyCarries(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	three := 3
	if _, err := boardAt(t, cloneA, t2).SetValue(id, &three); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t1).SetValue(id, &three); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("a local commit the upstream subsumes must be skipped, not fail: %v", err)
	}
	if !p.Pushed || p.Conflict {
		t.Errorf("progress = %+v; want pushed=true conflict=false", p)
	}
	tk, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Updated.Equal(t2) {
		t.Errorf("updated = %v; want the upstream's later stamp %v", tk.Updated, t2)
	}
	// Nothing was left to publish: B sits exactly on the upstream.
	if out := strings.TrimSpace(gittest.RunGit(t, git, cloneB, "rev-list", "--count", "@{u}..HEAD")); out != "0" {
		t.Errorf("B is %s commit(s) ahead of upstream after the skip; want 0", out)
	}
}

// Both sides closed the task (the PR footer's apply and a session's `done`):
// the merge keeps the first close and the sync goes through.
func TestSyncMergesDoubleClose(t *testing.T) {
	_, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	if _, err := boardAt(t, cloneA, t1).Done(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Done(id); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		var details any
		if fe := core.AsError(err); fe != nil {
			details = fe.Details
		}
		t.Fatalf("a double close must merge: %v (details %v)", err, details)
	}
	if !p.Pushed || len(p.MergedShards) != 1 {
		t.Errorf("progress = %+v; want pushed with one merged shard", p)
	}
	tk, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != "done" || tk.Closed == nil || !tk.Closed.Equal(t1) {
		t.Errorf("status=%q closed=%v; want done, closed at the FIRST close %v", tk.Status, tk.Closed, t1)
	}
}

// A real disagreement — the same field set differently on both sides — still
// aborts, restores the board, and now names the field in details.fields; a
// mergeable shard in the same stop is NOT half-applied.
func TestSyncConflictNamesTheDisagreeingFields(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	other, err := boardAt(t, cloneA, t1).Add("other", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	// A: retitle `shared`, close `other`. B: retitle `shared` differently, tick
	// `other`'s... value. `shared` is a real conflict; `other` would merge.
	a := boardAt(t, cloneA, t1)
	if _, err := a.SetTitle(id, "A wins"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	b := boardAt(t, cloneB, t2)
	if _, err := b.SetTitle(id, "B wins"); err != nil {
		t.Fatal(err)
	}
	two := 2
	if _, err := b.SetValue(other.ID, &two); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatal("a same-field disagreement must still fail the sync")
	}
	if !p.Conflict || p.Pulled || p.Pushed || len(p.MergedShards) != 0 || p.Complete {
		t.Errorf("progress = %+v; want conflict=true pulled=false pushed=false complete=false and no merged shards", p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncConflict {
		t.Fatalf("want sync-conflict, got %+v", err)
	}
	details, _ := fe.Details.(map[string]any)
	sharedPath := ".furrow/tasks/" + id + ".json"
	if paths, _ := details["paths"].([]string); len(paths) != 1 || paths[0] != sharedPath {
		t.Errorf("details.paths = %v; want only the unmergeable %s (the mergeable shard is not a conflict)", paths, sharedPath)
	}
	fields, _ := details["fields"].(map[string][]string)
	if got := strings.Join(fields[sharedPath], ","); got != "title" {
		t.Errorf("details.fields[%s] = %v; want [title]", sharedPath, fields[sharedPath])
	}
	if !strings.Contains(fe.Msg, "title") {
		t.Errorf("the message must name the field: %s", fe.Msg)
	}

	// The abort restored BOTH shards to the local commit: nothing half-merged.
	if strings.TrimSpace(gittest.RunGit(t, git, cloneB, "status", "--porcelain")) != "" {
		t.Errorf("board must be clean after the abort:\n%s", gittest.RunGit(t, git, cloneB, "status", "--porcelain"))
	}
	bb := openBoard(t, cloneB)
	if tk, _, err := bb.Get(id); err != nil || tk.Title != "B wins" {
		t.Errorf("local title must survive the abort: %v %v", tk, err)
	}
	if tk, _, err := bb.Get(other.ID); err != nil || tk.Status == "done" {
		t.Errorf("the mergeable shard must not be half-applied by an aborted sync: %+v %v", tk, err)
	}

	// The remedy, followed to the letter: B takes the other side's title and
	// re-runs. The rebase replays B's FIRST commit ("B wins") on its own, so
	// without the tip-aware settling that pick conflicted forever and every
	// retry grew the unpushed stack (t-kge6). Now the dispute is settled from
	// the tip, the pick merges, and the rest of B's edits ride along.
	if _, err := boardAt(t, cloneB, t2.Add(time.Minute)).SetTitle(id, "A wins"); err != nil {
		t.Fatal(err)
	}
	p, err = openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("following the remedy must merge and push: %v", err)
	}
	if !p.Pushed || p.Conflict {
		t.Errorf("progress after the remedy = %+v; want pushed, no conflict", p)
	}
	if strings.TrimSpace(gittest.RunGit(t, git, cloneB, "log", "--oneline", "@{u}..HEAD")) != "" {
		t.Errorf("unpushed commits remain after the remedy:\n%s", gittest.RunGit(t, git, cloneB, "log", "--oneline", "@{u}..HEAD"))
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"A": cloneA, "B": cloneB} {
		board := openBoard(t, dir)
		if tk, _, err := board.Get(id); err != nil || tk.Title != "A wins" {
			t.Errorf("%s: title = %v %v; want the value both sides agreed on", name, tk, err)
		}
		if tk, _, err := board.Get(other.ID); err != nil || tk.Status != "done" || tk.Value == nil || *tk.Value != 2 {
			t.Errorf("%s: the mergeable shard must carry A's close AND B's value: %+v %v", name, tk, err)
		}
	}
}

func TestShardKindOf(t *testing.T) {
	cases := []struct {
		path string
		kind core.ShardKind
		ok   bool
	}{
		{".furrow/tasks/t-k3m9p.json", core.ShardTask, true},
		{".furrow/archive/tasks/t-k3m9p.json", core.ShardTask, true},
		{".furrow/epics/e-k3m9.json", core.ShardEpic, true},
		{".furrow/repos/akira-toriyama__furrow.json", core.ShardRepo, true},
		{".furrow/meta.json", 0, false},
		{".furrow/archive/meta.json", 0, false},
		{".furrow/bodies/t-k3m9p.md", 0, false},
		{".furrow/tasks/t-k3m9p.md", 0, false},
		{".furrow/tasks/sub/t-k3m9p.json", 0, false},
		{"other/.furrow/tasks/t-k3m9p.json", 0, false},
		{".furrow/config.toml", 0, false},
	}
	for _, c := range cases {
		kind, ok := shardKindOf(".furrow", c.path)
		if ok != c.ok || (ok && kind != c.kind) {
			t.Errorf("shardKindOf(%q) = %v,%v; want %v,%v", c.path, kind, ok, c.kind, c.ok)
		}
	}
}

// A double close of a REPEATING task is two series (each close minted its own
// successor), so it must stay a conflict — review of #451 found the first cut
// merging it and pushing both successors.
func TestSyncDoubleCloseOfRepeatingTaskStaysAConflict(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)
	t1 := time.Date(2026, 10, 5, 4, 47, 41, 0, time.UTC)
	a := boardAt(t, cloneA, t1)
	chore, err := a.Add("weekly chore", AddOpts{Due: "2026-10-06", Repeat: "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneA, t1).Done(chore.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t1.Add(time.Second)).Done(chore.ID); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err == nil || !p.Conflict || p.Pushed {
		t.Fatalf("a double close of a repeating task must conflict, got err=%v progress=%+v", err, p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncConflict {
		t.Fatalf("want sync-conflict, got %+v", err)
	}
	details, _ := fe.Details.(map[string]any)
	fields, _ := details["fields"].(map[string][]string)
	if got := fields[".furrow/tasks/"+chore.ID+".json"]; len(got) == 0 || got[0] != "closed" {
		t.Errorf("details.fields = %v; want closed named on the repeating task", fields)
	}
}

// The first stop of the rebase is not a conflict at all (a hook refuses the
// very first pick): the report is git's own failure, not "conflicts", the
// board is restored, and conflict stays false.
func TestSyncFirstStopWithoutConflictReportsTheGitFailure(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	three := 3
	if _, err := boardAt(t, cloneA, t1).SetValue(id, &three); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Check(id, 0, true); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, cloneB, "add", "-A")
	gittest.RunGit(t, git, cloneB, "commit", "-q", "-m", "tick")
	hooks := filepath.Join(cloneB, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "prepare-commit-msg"), []byte("#!/bin/sh\necho 'policy: refused' >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // a test hook must be executable
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err == nil {
		t.Fatal("a rebase the hook refuses must fail the sync")
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindGitFailed || !strings.Contains(fe.Msg, "policy: refused") {
		t.Errorf("want git-failed carrying the hook's words, got %+v", err)
	}
	if p.Conflict || p.Pulled || p.Pushed {
		t.Errorf("progress = %+v; want conflict=false pulled=false pushed=false", p)
	}
	if strings.TrimSpace(gittest.RunGit(t, git, cloneB, "status", "--porcelain")) != "" {
		t.Errorf("board must be clean after the abort:\n%s", gittest.RunGit(t, git, cloneB, "status", "--porcelain"))
	}
	if _, statErr := os.Stat(filepath.Join(cloneB, ".git", "rebase-merge")); statErr == nil {
		t.Error("the rebase must have been aborted")
	}
}

// An operator's commit.cleanup=verbatim must not leak git's conflict template
// into the continued commit's message.
func TestSyncContinueStripsTheCommitTemplate(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	gittest.RunGit(t, git, cloneB, "config", "commit.cleanup", "verbatim")
	if _, err := boardAt(t, cloneA, t1).Done(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Check(id, 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	msg := gittest.RunGit(t, git, cloneB, "log", "-1", "--format=%B")
	for _, l := range strings.Split(msg, "\n") {
		if strings.HasPrefix(l, "#") {
			t.Errorf("the continued commit kept a template line: %q\n%s", l, msg)
		}
	}
	if !strings.HasPrefix(msg, DefaultSyncMessage) {
		t.Errorf("the continued commit must keep the sync subject:\n%s", msg)
	}
}

// A cancelled context must not make a half-rebased board look finished: the
// resolver names the cancellation so sync aborts the rebase.
func TestResolveShardConflictsNamesACancelledContext(t *testing.T) {
	git, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	if _, err := boardAt(t, cloneA, t1).Done(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Check(id, 0, true); err != nil {
		t.Fatal(err)
	}
	gittest.RunGit(t, git, cloneB, "add", "-A")
	gittest.RunGit(t, git, cloneB, "commit", "-q", "-m", "tick")
	gittest.RunGit(t, git, cloneB, "fetch", "-q")
	cmd := exec.Command(git, "rebase", "@{u}")
	cmd.Dir = cloneB
	if err := cmd.Run(); err == nil {
		t.Fatal("the rebase must stop on the shard")
	}
	r, err := gitrepo.Open(context.Background(), cloneB)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	merged, unresolved, merr := openBoard(t, cloneB).resolveShardConflicts(ctx, r, ".furrow", nil)
	if merr == nil || len(merged) != 0 || len(unresolved) != 0 {
		t.Errorf("resolver under a cancelled ctx = (%v, %v, %v); want the cancellation and nothing merged", merged, unresolved, merr)
	}
	if !r.RebaseInProgress(context.Background()) {
		t.Error("the rebase must still be in progress for the caller to abort")
	}
	if err := r.AbortRebase(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Two clones each make the FIRST review of the same repo — one human, one
// agent — so both create the record, each with the other stamp at null. The
// merge must carry both: refusing it left both clones unable to sync, since
// `review` can only press "now" and neither could take the other's value.
func TestSyncMergesBothSidesFirstReviewOfARepo(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)
	t1 := time.Date(2026, 10, 5, 4, 47, 41, 0, time.UTC)
	if _, err := boardAt(t, cloneA, t1).ReviewRepo("me/z", false); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t1.Add(time.Second)).ReviewRepo("me/z", true); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("two first reviews of one repo must merge: %v", err)
	}
	if !p.Pushed || len(p.MergedShards) != 1 || p.MergedShards[0] != ".furrow/repos/me__z.json" {
		t.Errorf("progress = %+v; want the repo record pushed as a merged shard", p)
	}
	rec, ok, err := openBoard(t, cloneB).Store.LoadRepo("me/z")
	if err != nil || !ok {
		t.Fatalf("record after merge: ok=%v err=%v", ok, err)
	}
	if rec.LastReviewed == nil || !rec.LastReviewed.Equal(t1) || rec.LastAgentReviewed == nil || !rec.LastAgentReviewed.Equal(t1.Add(time.Second)) {
		t.Errorf("merged record = %+v; want both stamps", rec)
	}
}

// Both clones review the same task: a review changes no content, so two of
// them are one review — the later — and never a conflict.
func TestSyncMergesConcurrentReviewsOfATask(t *testing.T) {
	_, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	if _, err := boardAt(t, cloneA, t1).ReviewTask(id); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).ReviewTask(id); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("two reviews of one task must merge: %v", err)
	}
	if !p.Pushed || len(p.MergedShards) != 1 {
		t.Errorf("progress = %+v; want the task pushed as a merged shard", p)
	}
	got, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reviewed == nil || !got.Reviewed.Equal(t2) {
		t.Errorf("reviewed = %v; want the later review %s", got.Reviewed, t2)
	}
}

// One clone binds a repeat rule to a task the other clone closes in the same
// window. Field by field that is a done task carrying a rule — no successor
// minted, the series dead with only lint's repeat-on-closed to say so — so it
// must stop the sync as a `closed` conflict, exactly as a close racing an edit
// of an already repeating task does.
func TestSyncMakingATaskRepeatVersusClosingItIsAConflict(t *testing.T) {
	_, cloneA, cloneB, id, t1, t2 := mergeClones(t)
	due, daily := "2026-10-06", "daily"
	if _, _, _, err := boardAt(t, cloneA, t1).Set(id, SetOpts{Due: &due, Repeat: &daily}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := boardAt(t, cloneB, t2).Done(id); err != nil {
		t.Fatal(err)
	}
	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{})
	if err == nil || !p.Conflict || p.Pushed {
		t.Fatalf("a close racing a new repeat rule must conflict, got err=%v progress=%+v", err, p)
	}
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSyncConflict {
		t.Fatalf("want sync-conflict, got %+v", err)
	}
	details, _ := fe.Details.(map[string]any)
	fields, _ := details["fields"].(map[string][]string)
	if got := fields[".furrow/tasks/"+id+".json"]; len(got) == 0 || got[0] != "closed" {
		t.Errorf("details.fields = %v; want closed named on the task", fields)
	}
	// The board is restored: B still holds its own close, unmerged.
	got, _, err := openBoard(t, cloneB).Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Closed == nil || got.Repeat != "" {
		t.Errorf("after the refused sync B's task = closed %v repeat %q; want its own close kept", got.Closed, got.Repeat)
	}
}
