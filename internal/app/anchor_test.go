package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// anchorFixture is the reschedule drill in miniature on a JST board: a box
// with a day, a follower in it and one in another box, a fixed date with no
// pointer, and a done follower. now is a weekday well before every due.
func anchorFixture(t *testing.T) (a *App, clk *fixedClock, epic string, follower, cross, fixed, done *core.Task) {
	t.Helper()
	a, clk = newAppWith(at(time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)), zone(jst))
	epic = mustEpic(t, a, "会場", EpicAddOpts{Repos: []string{"o/r"}, Anchor: "2026-11-21"})
	other := mustEpic(t, a, "献立", EpicAddOpts{Repos: []string{"o/r"}})
	follower = mustAdd(t, a, "申込書を出す", AddOpts{Epic: epic, Due: "2026-11-07T13:20", Anchor: epic})
	cross = mustAdd(t, a, "試食会", AddOpts{Epic: other, Due: "2026-11-14", Anchor: epic})
	fixed = mustAdd(t, a, "入金期限", AddOpts{Epic: epic, Due: "2026-10-12"})
	done = mustAdd(t, a, "済", AddOpts{Epic: epic, Due: "2026-10-01", Anchor: epic})
	if _, _, err := a.moveMany([]string{done.ID}, a.Cfg.DoneLane, ""); err != nil {
		t.Fatal(err)
	}
	return
}

func dueOf(t *testing.T, a *App, id string) time.Time {
	t.Helper()
	tk, _, err := a.Get(id)
	if err != nil || tk.Due == nil {
		t.Fatalf("task %s has no due (%v)", id, err)
	}
	return *tk.Due
}

// A first set moves nothing; a move shifts every open follower by the day
// delta with its wall clock kept, across boxes, and stamps only them; the
// fixed date and the done follower stay; the same day again is a no-op that
// leaves `updated` alone; the plan the preview computes is the plan the write
// applies.
func TestEpicSetAnchorMove(t *testing.T) {
	a, clk, epic, follower, cross, fixed, done := anchorFixture(t)
	before := map[string]time.Time{}
	for _, tk := range []*core.Task{follower, cross, fixed, done} {
		before[tk.ID] = dueOf(t, a, tk.ID)
	}
	clk.t = clk.t.Add(time.Hour)

	planned, err := a.PlanAnchor(epic, "2026-11-28")
	if err != nil {
		t.Fatal(err)
	}
	_, after, plan, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-11-28")})
	if err != nil {
		t.Fatal(err)
	}
	if after.Anchor != "2026-11-28" {
		t.Errorf("epic anchor = %q", after.Anchor)
	}
	if !reflect.DeepEqual(planned, plan) {
		t.Errorf("preview plan %+v != applied plan %+v", planned, plan)
	}
	if plan.From != "2026-11-21" || plan.To != "2026-11-28" || plan.Days != 7 || len(plan.Moves) != 2 || !reflect.DeepEqual(plan.Kept, []string{done.ID}) {
		t.Fatalf("plan = %+v", plan)
	}
	for _, m := range plan.Moves {
		if m.From.Location() != time.UTC || m.To.Location() != time.UTC {
			t.Errorf("plan stamps must be UTC: %+v", m)
		}
	}
	if got, want := dueOf(t, a, follower.ID), time.Date(2026, 11, 14, 13, 20, 0, 0, jst); !got.Equal(want) {
		t.Errorf("follower due = %s, want %s (wall clock kept)", got.In(jst), want)
	}
	if got, want := dueOf(t, a, cross.ID), time.Date(2026, 11, 21, 23, 59, 59, 0, jst); !got.Equal(want) {
		t.Errorf("cross-box follower due = %s, want %s", got.In(jst), want)
	}
	for _, tk := range []*core.Task{fixed, done} {
		if got := dueOf(t, a, tk.ID); !got.Equal(before[tk.ID]) {
			t.Errorf("%s moved: %s -> %s", tk.Title, before[tk.ID], got)
		}
	}
	moved, _, _ := a.Get(follower.ID)
	kept, _, _ := a.Get(fixed.ID)
	if !moved.Updated.Equal(clk.t) || kept.Updated.Equal(clk.t) {
		t.Errorf("updated: moved %s (want %s), fixed %s (want untouched)", moved.Updated, clk.t, kept.Updated)
	}

	clk.t = clk.t.Add(time.Hour)
	b2, a2, plan2, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-11-28")})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2.Moves) != 0 || !b2.Updated.Equal(a2.Updated) || !dueOf(t, a, follower.ID).Equal(moved.Due.UTC()) {
		t.Errorf("a same-day set must be a no-op: plan %+v, updated %s -> %s", plan2, b2.Updated, a2.Updated)
	}

	// Backwards, and the preview's arithmetic with it.
	_, _, plan3, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-11-20")})
	if err != nil || plan3.Days != -8 {
		t.Fatalf("a move back: %+v, %v", plan3, err)
	}
	if got, want := dueOf(t, a, follower.ID), time.Date(2026, 11, 6, 13, 20, 0, 0, jst); !got.Equal(want) {
		t.Errorf("follower due after -8d = %s, want %s", got.In(jst), want)
	}
}

// A first set on a box with followers already pointing at it (a cleared and
// re-set day) declares the dues as they are; a clear discloses the followers
// and lint names them; a hand-edited follower the move cannot shift refuses
// the whole move up front with every id in details.
func TestEpicSetAnchorClearAndRefusals(t *testing.T) {
	a, _, epic, follower, cross, _, done := anchorFixture(t)
	dueBefore := dueOf(t, a, follower.ID)
	_, after, plan, err := a.EpicSet(epic, EpicSetOpts{ClearAnchor: true})
	if err != nil {
		t.Fatal(err)
	}
	if after.Anchor != "" || plan.To != "" || plan.From != "2026-11-21" || !reflect.DeepEqual(plan.Followers, []string{follower.ID, cross.ID}) {
		t.Errorf("clear plan = %+v, epic anchor %q", plan, after.Anchor)
	}
	ps, _ := a.Lint()
	if got := problemsWithCode(ps, "anchor-unset"); len(got) != 2 {
		t.Errorf("anchor-unset findings = %+v (the done follower is exempt)", got)
	}
	if _, _, _, err := a.Set(cross.ID, SetOpts{Anchor: ptr(epic)}); err == nil {
		t.Error("a task cannot follow a box that has no day")
	}
	_, _, plan, err = a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-12-05")})
	if err != nil || plan.From != "" || len(plan.Moves) != 0 {
		t.Fatalf("re-setting the day is a first set: %+v, %v", plan, err)
	}
	if !dueOf(t, a, follower.ID).Equal(dueBefore) {
		t.Error("a first set moved a follower")
	}
	if ps, _ = a.Lint(); len(problemsWithCode(ps, "anchor-unset")) != 0 {
		t.Error("anchor-unset should clear once the box has a day again")
	}

	// A hand-edited follower: a rule planted beside the pointer.
	handEditRepeat(t, a, cross.ID, "FREQ=WEEKLY", ptr(dueOf(t, a, cross.ID)))
	ps, _ = a.Lint()
	if got := problemsWithCode(ps, "anchor-on-repeat"); len(got) != 1 || got[0].ID != cross.ID {
		t.Errorf("anchor-on-repeat = %+v", got)
	}
	_, _, _, err = a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-12-12")})
	ce, ok := err.(*core.Error)
	if !ok || ce.Code != core.CodeValidation || ce.Subject != epic {
		t.Fatalf("a move over a repeating follower must refuse: %v", err)
	}
	if d, _ := ce.Details.(map[string]any); !reflect.DeepEqual(d["repeating"], []string{cross.ID}) {
		t.Errorf("details.repeating = %v", ce.Details)
	}
	e, _, _ := a.Store.LoadEpic(epic)
	if e.Anchor != "2026-12-05" || !dueOf(t, a, follower.ID).Equal(dueBefore) {
		t.Errorf("a refused move wrote something: anchor %q, follower due %s", e.Anchor, dueOf(t, a, follower.ID))
	}
	_ = done
	for _, bad := range []string{"", "2026-13-45", "next week"} {
		if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr(bad)}); err == nil {
			t.Errorf("EpicSet --anchor %q must be refused", bad)
		}
	}
	if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: ptr("2026-12-12"), ClearAnchor: true}); err == nil {
		t.Error("--anchor beside --clear-anchor must be refused")
	}
	if _, err := a.EpicAdd("bad", EpicAddOpts{Anchor: "2026-02-30"}); err == nil {
		t.Error("epic add with a bad day must be refused")
	}
}

// The task side: the pointer needs a due (this write's counts), refuses a
// rule, resolves the box strictly with the day-shaped hint, goes with the due
// on --clear-due, and the batch names every offender before writing anything.
func TestSetAnchorInvariants(t *testing.T) {
	a, _, epic, follower, _, fixed, _ := anchorFixture(t)
	undated := mustAdd(t, a, "undated", AddOpts{Epic: epic})
	weekly := mustAddRepeating(t, a, "weekly", "2026-10-05", "weekly", AddOpts{Epic: epic})

	_, _, _, err := a.Set(undated.ID, SetOpts{Anchor: ptr(epic)})
	ce, ok := err.(*core.Error)
	if !ok || ce.Code != core.CodeValidation || ce.Subject != undated.ID {
		t.Fatalf("undated: %v", err)
	}
	if d, _ := ce.Details.(map[string]any); !reflect.DeepEqual(d["undated"], []string{undated.ID}) {
		t.Errorf("details.undated = %v", ce.Details)
	}
	if got, _, _, err := a.Set(undated.ID, SetOpts{Anchor: ptr(epic), Due: ptr("2026-11-20")}); err != nil || got.Anchor != epic {
		t.Errorf("--due and --anchor in one write: %v, %+v", err, got)
	}
	_, _, _, err = a.Set(weekly.ID, SetOpts{Anchor: ptr(epic)})
	if ce, ok = err.(*core.Error); !ok || ce.Code != core.CodeValidation {
		t.Fatalf("repeating: %v", err)
	}
	if d, _ := ce.Details.(map[string]any); !reflect.DeepEqual(d["repeating"], []string{weekly.ID}) {
		t.Errorf("details.repeating = %v", ce.Details)
	}
	if got, _, _, err := a.Set(weekly.ID, SetOpts{Anchor: ptr(epic), ClearRepeat: true}); err != nil || got.Anchor != epic || got.Repeat != "" {
		t.Errorf("--clear-repeat and --anchor in one write: %v, %+v", err, got)
	}
	if _, _, _, err := a.Set(fixed.ID, SetOpts{Anchor: ptr(epic), Repeat: ptr("weekly")}); err == nil {
		t.Error("--anchor beside --repeat must be refused")
	}
	if _, _, _, err := a.Set(fixed.ID, SetOpts{Anchor: ptr(epic), ClearDue: true}); err == nil {
		t.Error("--anchor beside --clear-due must be refused")
	}
	if _, _, _, err := a.Set(fixed.ID, SetOpts{Repeat: ptr("weekly")}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.Set(fixed.ID, SetOpts{Anchor: ptr(epic)}); err == nil {
		t.Error("a task that already repeats cannot take a pointer")
	}
	_, _, _, err = a.Set(fixed.ID, SetOpts{Anchor: ptr("2026-11-28"), ClearRepeat: true})
	if err == nil || !strings.Contains(err.Error(), "furrow epic set <epic> --anchor 2026-11-28") {
		t.Errorf("a day where the box goes: %v", err)
	}
	if _, _, _, err := a.Set(fixed.ID, SetOpts{Anchor: ptr("no-such-box"), ClearRepeat: true}); err == nil {
		t.Error("an unknown box must be refused")
	}

	cleared, _, _, err := a.Set(follower.ID, SetOpts{ClearDue: true})
	if err != nil || cleared.Anchor != "" || cleared.Due != nil {
		t.Errorf("--clear-due must take the pointer with it: %v, %+v", err, cleared)
	}
	if got, _, _, err := a.Set(undated.ID, SetOpts{ClearAnchor: true}); err != nil || got.Anchor != "" || got.Due == nil {
		t.Errorf("--clear-anchor keeps the due: %v, %+v", err, got)
	}

	// All-or-nothing across the batch.
	bare := mustAdd(t, a, "bare", AddOpts{Epic: epic})
	_, _, err = a.SetMany([]string{undated.ID, bare.ID}, SetOpts{Anchor: ptr(epic)})
	if ce, ok = err.(*core.Error); !ok || ce.Subject != "" {
		t.Fatalf("batch with an undated id: %v", err)
	}
	if again, _, _ := a.Get(undated.ID); again.Anchor != "" {
		t.Error("the batch wrote the dated task despite the refusal")
	}

	// Add: the same two invariants, before anything is written.
	if _, err := a.Add("no due", AddOpts{Epic: epic, Anchor: epic}); err == nil {
		t.Error("add --anchor without --due must be refused")
	}
	if _, err := a.Add("rule", AddOpts{Epic: epic, Anchor: epic, Due: "2026-11-01", Repeat: "weekly"}); err == nil {
		t.Error("add --anchor beside --repeat must be refused")
	}
	if n := len(idsOf(mustList(t, a, QueryOpts{Query: "anchor:" + epic}))); n != 3 {
		t.Errorf("anchor: selects the three pointers (cross, done, undated-now-dated), got %d", n)
	}
	if got := idsOf(mustList(t, a, QueryOpts{Query: "has:due no:anchor"})); !contains(got, fixed.ID) {
		t.Errorf("has:due no:anchor should list the fixed date, got %v", got)
	}
}

func mustList(t *testing.T, a *App, o QueryOpts) []core.Task {
	t.Helper()
	o.Repo = ""
	tasks, err := a.List(o)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

// epic rm counts followers as references and --force unpoints them, dues kept.
func TestRemoveEpicSeversFollowers(t *testing.T) {
	a, _, epic, follower, cross, fixed, _ := anchorFixture(t)
	_, err := a.RemoveEpic(epic, RemoveOpts{Apply: true})
	ce, ok := err.(*core.Error)
	if !ok || ce.Kind != core.KindReferenced {
		t.Fatalf("rm with followers: %v", err)
	}
	rep, err := a.RemoveEpic(epic, RemoveOpts{Apply: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.References.Followers) != 3 {
		t.Errorf("followers = %+v", rep.References.Followers)
	}
	for _, tk := range []*core.Task{follower, cross, fixed} {
		got, _, _ := a.Get(tk.ID)
		if got.Anchor != "" || got.Due == nil {
			t.Errorf("%s after --force: anchor %q, due %v", tk.Title, got.Anchor, got.Due)
		}
	}
}

// show resolves the pointer to the box, its title and its day.
func TestShowResolvesAnchor(t *testing.T) {
	a, _, epic, follower, _, _, _ := anchorFixture(t)
	entries, _, err := a.ShowBatch([]string{follower.ID}, false)
	if err != nil || len(entries) != 1 || entries[0].Task == nil {
		t.Fatalf("show: %v", err)
	}
	ref := entries[0].Task.AnchorRef
	if ref == nil || ref.ID != epic || ref.Title != "会場" || ref.Date != "2026-11-21" {
		t.Errorf("AnchorRef = %+v", ref)
	}
}
