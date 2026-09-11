package app

import (
	"context"
	"strings"
	"testing"
)

// A close that advances a series CREATES a task, so a shared board must carry
// both halves of it: the shard and the prose. The body rule is the subtle one —
// CLAUDE.md says a merely-MODIFIED body is published only when named with -b,
// so a generated body has to be new (untracked), which always commits. If it
// ever became a modification instead, a plain sync would publish a task whose
// prose exists on exactly one machine.
func TestSyncPublishesAGeneratedSuccessorWithItsBody(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)

	a := openBoard(t, cloneA)
	a.Loc = jst
	task, err := a.Add("水やり", AddOpts{Due: "2026-03-01", Repeat: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	// Close on A: the successor is born here, body and all.
	closer := openBoard(t, cloneA)
	closer.Loc = jst
	closed, rep, err := closer.moveOne(task.ID, closer.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || rep.Created == nil {
		t.Fatalf("no successor: %+v", rep)
	}
	pub, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pub.PendingBodies) != 0 {
		t.Errorf("pending_bodies = %v — a generated body was left behind by a plain sync", pub.PendingBodies)
	}
	if !pub.Complete {
		t.Errorf("sync reported complete=false: %+v", pub)
	}

	// B pulls: it must see the successor AND be able to read its prose.
	b := openBoard(t, cloneB)
	if _, err := b.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	got := openBoard(t, cloneB)
	succ, _, err := got.Get(*rep.Created)
	if err != nil {
		t.Fatalf("the successor never reached the other clone: %v", err)
	}
	if succ.Repeat == "" || succ.RepeatAnchor == nil {
		t.Errorf("the successor arrived without its rule: %q / %v", succ.Repeat, succ.RepeatAnchor)
	}
	body, err := got.Store.LoadBody(succ.ID)
	if err != nil {
		t.Fatalf("the successor's body never reached the other clone: %v", err)
	}
	if !strings.Contains(body, "[["+closed.ID+"]]") {
		t.Errorf("the published body lost its back-link:\n%s", body)
	}
}
