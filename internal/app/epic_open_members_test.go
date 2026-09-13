package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// newDatedApp is newApp with a board calendar declared, so a repeating member
// can be created without tripping the shared-board timezone rule.
func newDatedApp() *App {
	cfg := config.Default()
	cfg.DueTimezone = jst
	st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
	return NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)})
}

// TestEpicOpenMembersCountsWhatEpicClosedWarnsAbout pins the disclosure's set to
// core.EpicProblems' epic-closed set: non-terminal members only, with the
// recurring ones distinguishable. A terminal member counted here would make
// `epic done` report work lint is deliberately silent about.
func TestEpicOpenMembersCountsWhatEpicClosedWarnsAbout(t *testing.T) {
	a := newDatedApp()
	eid := mustEpic(t, a, "box", EpicAddOpts{})
	chore := mustAddRepeating(t, a, "daily chore", "2026-06-26", "daily", AddOpts{Epic: eid})
	plain := mustAdd(t, a, "plain member", AddOpts{Epic: eid, Status: "ready"})
	parked := mustAdd(t, a, "parked member", AddOpts{Epic: eid, Status: "icebox"})
	shut := mustAdd(t, a, "finished member", AddOpts{Epic: eid})
	if _, err := a.Done(shut.ID); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, a, "another box's member", AddOpts{Epic: mustEpic(t, a, "other box", EpicAddOpts{})})

	if _, _, err := a.EpicDone(eid); err != nil {
		t.Fatalf("epic done: %v", err)
	}
	got := a.EpicOpenMembers(eid)

	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if len(got) != 2 {
		t.Fatalf("open members = %v, want exactly the two non-terminal ones (%s, %s); %s is parked and %s is done",
			ids, chore.ID, plain.ID, parked.ID, shut.ID)
	}
	byID := map[string]EpicOpenMember{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if m, ok := byID[chore.ID]; !ok || m.Repeat != "FREQ=DAILY" {
		t.Errorf("repeating member = %+v (present=%v), want its rule carried — it is the one the disclosure calls out", m, ok)
	}
	if m, ok := byID[plain.ID]; !ok || m.Repeat != "" || m.Status != "ready" {
		t.Errorf("plain member = %+v (present=%v), want repeat \"\" and its lane", m, ok)
	}

	// The lint code this disclosure exists to pre-announce must see the same set.
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		t.Fatal(err)
	}
	warned := 0
	for _, p := range core.EpicProblems(idx, epics, a.Cfg.Terminal, nil) {
		if p.Code == "epic-closed" {
			warned++
		}
	}
	if warned != len(got) {
		t.Errorf("epic-closed warns about %d task(s), the disclosure reports %d — the two must count the same set", warned, len(got))
	}
}

// TestEpicOpenMembersOnABoxWithNothingOpen pins the empty answer as [] rather
// than nil: nil is reserved for "could not be read", which the CLI reports as
// such instead of printing a false all-clear.
func TestEpicOpenMembersOnABoxWithNothingOpen(t *testing.T) {
	a := newApp()
	eid := mustEpic(t, a, "box", EpicAddOpts{})
	if got := a.EpicOpenMembers(eid); got == nil || len(got) != 0 {
		t.Errorf("open members = %#v, want an empty non-nil slice", got)
	}
}

func TestRepeatingMembersSelectsTheLiveSeries(t *testing.T) {
	in := []EpicOpenMember{{ID: "t-a"}, {ID: "t-b", Repeat: "FREQ=DAILY"}, {ID: "t-c"}}
	got := RepeatingMembers(in)
	if len(got) != 1 || got[0].ID != "t-b" {
		t.Errorf("RepeatingMembers = %+v, want only t-b", got)
	}
	if got := RepeatingMembers(nil); got == nil {
		t.Errorf("RepeatingMembers(nil) = nil, want [] — the caller counts it, never nil-checks it")
	}
}
