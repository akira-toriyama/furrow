package app

import (
	"context"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func findIncoming(changes []IncomingChange, id string) *IncomingChange {
	for i := range changes {
		if changes[i].ID == id {
			return &changes[i]
		}
	}
	return nil
}

// A sync that pulls another machine's board writes must SAY what came in —
// pulled:true alone was the whole report before (t-fzff). Real-git e2e (the
// setupClones harness): the report reads the pre-pull..post-pull tree diff,
// which no memstore can fake.
func TestSyncReportsIncomingChanges(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)

	a := openBoard(t, cloneA)
	t1, err := a.Add("will close", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := a.Add("will move", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	fromLane := t2.Status
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	b := openBoard(t, cloneB)
	p, err := b.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("B sync: %v (progress %+v)", err, p)
	}
	for _, id := range []string{t1.ID, t2.ID} {
		ch := findIncoming(p.Incoming, id)
		if ch == nil || ch.Kind != "created" {
			t.Errorf("incoming for %s = %+v, want kind created (all: %+v)", id, ch, p.Incoming)
		}
	}
	if ch := findIncoming(p.Incoming, t1.ID); ch != nil && ch.Title != "will close" {
		t.Errorf("incoming title = %q, want the shard's title", ch.Title)
	}

	// B closes one and moves the other; A's next sync must classify both —
	// and must NOT report A's own concurrent write as incoming.
	if _, err := b.Done(t1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Move(t2.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	a = openBoard(t, cloneA)
	local, err := a.Add("A's own write", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	p, err = a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("A sync: %v (progress %+v)", err, p)
	}
	if ch := findIncoming(p.Incoming, t1.ID); ch == nil || ch.Kind != "closed" {
		t.Errorf("incoming for %s = %+v, want kind closed (all: %+v)", t1.ID, ch, p.Incoming)
	}
	if ch := findIncoming(p.Incoming, t2.ID); ch == nil || ch.Kind != "moved" || ch.From != fromLane || ch.To != "ready" {
		t.Errorf("incoming for %s = %+v, want moved %s→ready (all: %+v)", t2.ID, ch, fromLane, p.Incoming)
	}
	if ch := findIncoming(p.Incoming, local.ID); ch != nil {
		t.Errorf("A's own auto-committed write reported as incoming: %+v", ch)
	}

	p, err = openBoard(t, cloneA).Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Incoming) != 0 {
		t.Errorf("no-change sync reported incoming: %+v", p.Incoming)
	}
}

// The modification classifier's kind priority: a done both closes and moves,
// and "closed" must win; a lane change outranks an epic change; anything else
// is "updated". Pure table test — the git plumbing is covered by the e2e above.
func TestClassifyIncomingEdit(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name           string
		before, after  core.Task
		kind, from, to string
	}{
		{"done closes and moves; closed wins", core.Task{Status: "in-progress"}, core.Task{Status: "done", Closed: &now}, "closed", "", ""},
		{"reopened", core.Task{Status: "done", Closed: &now}, core.Task{Status: "backlog"}, "reopened", "", ""},
		{"lane change", core.Task{Status: "backlog"}, core.Task{Status: "ready"}, "moved", "backlog", "ready"},
		{"lane outranks epic", core.Task{Status: "backlog", Epic: "e-1"}, core.Task{Status: "ready", Epic: "e-2"}, "moved", "backlog", "ready"},
		{"epic change", core.Task{Status: "backlog", Epic: "e-1"}, core.Task{Status: "backlog", Epic: "e-2"}, "refiled", "e-1", "e-2"},
		{"filed from unfiled", core.Task{Status: "backlog"}, core.Task{Status: "backlog", Epic: "e-2"}, "refiled", "", "e-2"},
		{"metadata only", core.Task{Status: "backlog", Priority: 10}, core.Task{Status: "backlog", Priority: 20}, "updated", "", ""},
	}
	for _, tc := range cases {
		kind, from, to := classifyIncomingEdit(&tc.before, &tc.after)
		if kind != tc.kind || from != tc.from || to != tc.to {
			t.Errorf("%s: = (%q, %q, %q), want (%q, %q, %q)", tc.name, kind, from, to, tc.kind, tc.from, tc.to)
		}
	}
}
