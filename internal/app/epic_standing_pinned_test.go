package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// setFlag flips the v7 standing/pinned declarations via the same EpicSet path
// the CLI drives.
func setFlag(t *testing.T, a *App, id string, standing, pinned *bool) {
	t.Helper()
	if _, _, err := a.EpicSet(id, EpicSetOpts{Standing: standing, Pinned: pinned}); err != nil {
		t.Fatalf("epic set %s: %v", id, err)
	}
}

func boolPtr(b bool) *bool { return &b }

// The pass-through contract, in all three configurations that matter:
// a DIFFERENT box active, NOTHING active, and an explicit -e bypass.
func TestNextPinnedPassesThroughActiveScope(t *testing.T) {
	a := newApp()
	focus := mustEpic(t, a, "the focus box", EpicAddOpts{Repos: []string{"o/r"}})
	mandate := mustEpic(t, a, "mandate box", EpicAddOpts{Repos: []string{"o/r"}})
	setFlag(t, a, mandate, boolPtr(true), boolPtr(true))

	focusTask := mustAddReady(t, a, "focus work", focus)
	order := mustAddReady(t, a, "an instruction", mandate)

	// Nothing active: the pinned band still shows, the rest stays deliberately
	// empty (never the unfiled pile).
	unfiled := mustAddReady(t, a, "unfiled stray", "")
	got := mustNext(t, a)
	if len(got) != 1 || got[0].ID != order {
		t.Fatalf("with nothing active only the pinned band may show: got %v", taskIDs(got))
	}

	// A different box active: pinned tasks LEAD, focus follows, unfiled rescues.
	mustActivate(t, a, focus)
	got = mustNext(t, a)
	if len(got) != 3 || got[0].ID != order || got[1].ID != focusTask || got[2].ID != unfiled {
		t.Fatalf("want [pinned, focus, unfiled], got %v", taskIDs(got))
	}

	// -n1 hands you the pinned channel, not whatever sorts first.
	limited, err := a.Next(QueryOpts{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != order {
		t.Fatalf("-n1 must hand the pinned channel first: got %v", taskIDs(limited))
	}

	// An explicit -e bypasses the scope, pinned band included.
	scoped, err := a.Next(QueryOpts{Epic: focus})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ID != focusTask {
		t.Fatalf("-e must read one box only: got %v", taskIDs(scoped))
	}
}

// A closed pinned box injects nothing, and a box both pinned and active shows
// its tasks once (in the pinned band).
func TestNextPinnedEdgeCases(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "pinned and active", EpicAddOpts{Repos: []string{"o/r"}})
	setFlag(t, a, box, nil, boolPtr(true))
	work := mustAddReady(t, a, "the work", box)
	mustActivate(t, a, box)

	got := mustNext(t, a)
	if len(got) != 1 || got[0].ID != work {
		t.Fatalf("pinned+active must show the task exactly once: got %v", taskIDs(got))
	}

	if _, _, err := a.EpicDone(box); err != nil {
		t.Fatal(err)
	}
	got = mustNext(t, a)
	if len(got) != 0 {
		t.Fatalf("a closed pinned box must inject nothing: got %v", taskIDs(got))
	}
}

// standing silences the lifecycle nags: all_done, dep_done, and stuck.
func TestStandingSilencesFinishNags(t *testing.T) {
	a := newApp()
	inbox := mustEpic(t, a, "mandate inbox", EpicAddOpts{Repos: []string{"o/r"}})
	setFlag(t, a, inbox, boolPtr(true), nil)

	// A drained inbox: its one member is done — open 0 is HEALTHY here.
	tsk := mustAddReady(t, a, "consumed instruction", inbox)
	if _, err := a.Done(tsk); err != nil {
		t.Fatal(err)
	}
	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicAllDone) != 0 {
		t.Errorf("a standing box must not be nagged to close: %v", sum.EpicAllDone)
	}

	// dep_done: a standing box waiting on a closed box is NOT nudged to open.
	dep := mustEpic(t, a, "gone box", EpicAddOpts{Repos: []string{"o/r2"}})
	if _, _, err := a.EpicAddDeps(inbox, []string{dep}); err != nil {
		t.Fatal(err)
	}
	mustEpicDone(t, a, dep)
	sum, err = a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicDepDone) != 0 {
		t.Errorf("a standing box has no turn to open: %v", sum.EpicDepDone)
	}

	// stuck is silenced too: deposits sitting non-actionable (an inbox full of
	// untriaged backlog) is a standing channel's other healthy resting state.
	blocked := mustAddReady(t, a, "waits forever", inbox)
	gate := mustAdd(t, a, "the gate", AddOpts{Epic: inbox})
	if _, err := a.AddDep(blocked, gate.ID); err != nil {
		t.Fatal(err)
	}
	sum, err = a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicStuck) != 0 {
		t.Errorf("a standing box must not report stuck: %v", sum.EpicStuck)
	}
}

// Brief lists the pinned channels beside the focus, deduped against active.
func TestBriefListsPinnedChannels(t *testing.T) {
	a := newApp()
	focus := mustEpic(t, a, "the focus", EpicAddOpts{Repos: []string{"o/r"}})
	mandate := mustEpic(t, a, "mandate box", EpicAddOpts{Repos: []string{"o/other"}})
	setFlag(t, a, mandate, boolPtr(true), boolPtr(true))
	mustActivate(t, a, focus)
	mustAddReady(t, a, "an instruction", mandate)

	b, err := a.Brief(QueryOpts{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Pinned) != 1 || b.Pinned[0].Epic.ID != mandate {
		t.Fatalf("brief must list the pinned channel: %+v", b.Pinned)
	}

	// A box both pinned and active appears once, as the focus.
	setFlag(t, a, focus, nil, boolPtr(true))
	b, err = a.Brief(QueryOpts{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range b.Pinned {
		if it.Epic.ID == focus {
			t.Errorf("an active box must not repeat in pinned: %+v", b.Pinned)
		}
	}
}

func mustAddReady(t *testing.T, a *App, title, epic string) string {
	t.Helper()
	task := mustAdd(t, a, title, AddOpts{Epic: epic})
	if _, err := a.Move(task.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func mustNext(t *testing.T, a *App) []core.Task {
	t.Helper()
	tasks, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func taskIDs(ts []core.Task) []string {
	out := make([]string, 0, len(ts))
	for _, tk := range ts {
		out = append(out, tk.ID)
	}
	return out
}
