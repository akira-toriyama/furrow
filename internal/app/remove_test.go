package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func wantReferenced(t *testing.T, err error) References {
	t.Helper()
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindReferenced || fe.Code != core.CodeValidation {
		t.Fatalf("want kind referenced exit 2, got %v", err)
	}
	d, ok := fe.Details.(map[string]any)
	if !ok {
		t.Fatalf("details = %T, want a map carrying references", fe.Details)
	}
	refs, ok := d["references"].(References)
	if !ok {
		t.Fatalf("details.references = %T", d["references"])
	}
	return refs
}

// A preview writes nothing; --yes removes shard, body, and the task's assets.
func TestRemoveTasksPreviewThenApply(t *testing.T) {
	a := newApp()
	x, _ := a.Add("gone", AddOpts{})
	name, err := a.Store.SaveAsset(x.ID, "shot.png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := a.RemoveTasks([]string{x.ID, x.ID}, RemoveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.Tasks) != 1 || rep.Tasks[0].ID != x.ID || !rep.References.Empty() {
		t.Fatalf("preview = %+v", rep)
	}
	if _, _, err := a.Get(x.ID); err != nil {
		t.Fatalf("a preview must not remove: %v", err)
	}
	rep, err = a.RemoveTasks([]string{x.ID}, RemoveOpts{Apply: true})
	if err != nil || rep.DryRun {
		t.Fatalf("apply: %+v %v", rep, err)
	}
	if _, _, err := a.Get(x.ID); core.AsError(err) == nil || core.AsError(err).Kind != core.KindNotFound {
		t.Fatalf("task still readable after rm: %v", err)
	}
	if a.Store.BodyExists(x.ID) {
		t.Error("body survived rm")
	}
	if _, err := a.Store.LoadAsset(name); err == nil {
		t.Error("asset survived rm")
	}
}

// A target another task's deps or a body's [[link]] still names is refused,
// kind referenced, every reference in details — and nothing is written.
func TestRemoveTasksRefusesWhileReferenced(t *testing.T) {
	a := newApp()
	x, _ := a.Add("target", AddOpts{})
	y, _ := a.Add("waits", AddOpts{Deps: []string{x.ID}})
	z, _ := a.Add("mentions", AddOpts{Body: "see [[" + x.ID + "]]"})
	_, err := a.RemoveTasks([]string{x.ID}, RemoveOpts{Apply: true})
	refs := wantReferenced(t, err)
	if len(refs.Deps) != 1 || refs.Deps[0] != (DepEdge{From: y.ID, To: x.ID}) {
		t.Errorf("deps = %+v", refs.Deps)
	}
	if len(refs.Links) != 1 || refs.Links[0] != (LinkRef{Body: z.ID, To: x.ID}) {
		t.Errorf("links = %+v", refs.Links)
	}
	if refs.Members == nil || refs.EpicDeps == nil {
		t.Error("reference slices must be [] not null")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("message names no escape: %s", err)
	}
	if _, _, err := a.Get(x.ID); err != nil {
		t.Fatalf("a refusal must not remove: %v", err)
	}
	// The preview refuses the same way: a referenced target is an error, not a
	// warning a --yes could then walk past.
	if _, err := a.RemoveTasks([]string{x.ID}, RemoveOpts{}); core.AsError(err) == nil || core.AsError(err).Kind != core.KindReferenced {
		t.Errorf("preview did not refuse: %v", err)
	}
}

// --force severs: the dep edge goes, the live link becomes the bare id (a
// code-quoted example survives verbatim), and neither owner's `updated`
// moves — bookkeeping, not progress.
func TestRemoveTasksForceSeversWithoutStamping(t *testing.T) {
	a := newApp()
	clk := a.Clock.(*fixedClock)
	x, _ := a.Add("target", AddOpts{})
	y, _ := a.Add("waits", AddOpts{Deps: []string{x.ID}})
	z, _ := a.Add("mentions", AddOpts{Body: "see [[" + x.ID + "]] and `[[" + x.ID + "]]`"})
	clk.t = clk.t.Add(time.Hour)
	rep, err := a.RemoveTasks([]string{x.ID}, RemoveOpts{Force: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.References.Deps) != 1 || len(rep.References.Links) != 1 {
		t.Errorf("report = %+v", rep.References)
	}
	y2, _, _ := a.Get(y.ID)
	if len(y2.Deps) != 0 {
		t.Errorf("dep edge survived: %v", y2.Deps)
	}
	if !y2.Updated.Equal(y.Updated) {
		t.Errorf("severing stamped updated: %s -> %s", y.Updated, y2.Updated)
	}
	z2, body, _ := a.Get(z.ID)
	if want := "see " + x.ID + " and `[[" + x.ID + "]]`"; !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
	if !z2.Updated.Equal(z.Updated) {
		t.Errorf("de-linking stamped updated: %s -> %s", z.Updated, z2.Updated)
	}
	if _, _, err := a.Get(x.ID); err == nil {
		t.Error("target survived --force rm")
	}
}

// References among the targets themselves never count: a chain removes in one
// call, and a miss removes nothing (details.missing).
func TestRemoveTasksChainAndAllOrNothing(t *testing.T) {
	a := newApp()
	x, _ := a.Add("first", AddOpts{})
	y, _ := a.Add("second", AddOpts{Deps: []string{x.ID}, Body: "after [[" + x.ID + "]]"})
	_, err := a.RemoveTasks([]string{x.ID, "t-nope1"}, RemoveOpts{Apply: true})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindNotFound {
		t.Fatalf("want not-found, got %v", err)
	}
	if d, _ := fe.Details.(map[string]any); d == nil || len(d["missing"].([]string)) != 1 {
		t.Errorf("details = %v", fe.Details)
	}
	if _, _, err := a.Get(x.ID); err != nil {
		t.Fatalf("a miss removed the found target: %v", err)
	}
	rep, err := a.RemoveTasks([]string{y.ID, x.ID}, RemoveOpts{Apply: true})
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if !rep.References.Empty() || len(rep.Tasks) != 2 {
		t.Errorf("chain report = %+v", rep)
	}
	idx, _ := a.load()
	if len(idx.Tasks) != 0 {
		t.Errorf("tasks left: %d", len(idx.Tasks))
	}
}

// A box's references are its members, the boxes that wait on it, and the
// [[e-…]] links in other bodies; --force unfiles, drops the edge, de-links,
// and deletes shard + body.
func TestRemoveEpicRefusesThenForceSevers(t *testing.T) {
	a := newApp()
	box, _ := a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/r"}})
	later, _ := a.EpicAdd("later", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(later.ID, []string{box.ID}); err != nil {
		t.Fatal(err)
	}
	m, _ := a.Add("member", AddOpts{Epic: box.ID})
	z, _ := a.Add("mentions", AddOpts{Body: "part of [[" + box.ID + "]]"})

	_, err := a.RemoveEpic(box.ID, RemoveOpts{Apply: true})
	refs := wantReferenced(t, err)
	if len(refs.Members) != 1 || refs.Members[0] != (MemberRef{Task: m.ID, Epic: box.ID}) {
		t.Errorf("members = %+v", refs.Members)
	}
	if len(refs.EpicDeps) != 1 || refs.EpicDeps[0] != (DepEdge{From: later.ID, To: box.ID}) {
		t.Errorf("epic deps = %+v", refs.EpicDeps)
	}
	if len(refs.Links) != 1 || refs.Links[0] != (LinkRef{Body: z.ID, To: box.ID}) {
		t.Errorf("links = %+v", refs.Links)
	}
	if _, ok, _ := a.Store.LoadEpic(box.ID); !ok {
		t.Fatal("a refusal removed the box")
	}

	rep, err := a.RemoveEpic("box", RemoveOpts{Force: true, Apply: true}) // by title
	if err != nil {
		t.Fatal(err)
	}
	if rep.Epic.ID != box.ID || rep.DryRun {
		t.Errorf("report = %+v", rep)
	}
	if _, ok, _ := a.Store.LoadEpic(box.ID); ok {
		t.Error("epic shard survived")
	}
	if a.Store.BodyExists(box.ID) {
		t.Error("epic body survived")
	}
	m2, _, _ := a.Get(m.ID)
	if m2.Epic != "" {
		t.Errorf("member still filed under %q", m2.Epic)
	}
	later2, _, _ := a.Store.LoadEpic(later.ID)
	if len(later2.Deps) != 0 {
		t.Errorf("epic dep survived: %v", later2.Deps)
	}
	_, body, _ := a.Get(z.ID)
	if !strings.Contains(body, "part of "+box.ID) || strings.Contains(body, "[[") {
		t.Errorf("body = %q", body)
	}
	if _, err := a.RemoveEpic(box.ID, RemoveOpts{}); core.AsError(err) == nil || core.AsError(err).Kind != core.KindEpicNotFound {
		t.Errorf("a removed box must be epic-not-found: %v", err)
	}
}

// rm is guarded like every write: on the target, and under --force on every
// entity it edits — even when the target itself sits in a free repo.
func TestSessionGuardCoversRemove(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	var x, free, waits *core.Task
	var box *core.Epic
	offGuard(a, func() {
		x, _ = a.Add("occupied", AddOpts{Repos: []string{"o/glyph"}})
		free, _ = a.Add("free", AddOpts{Repos: []string{"o/furrow"}})
		waits, _ = a.Add("waits", AddOpts{Repos: []string{"o/glyph"}, Deps: []string{free.ID}})
		box, _ = a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/glyph"}})
	})
	_, err := a.RemoveTasks([]string{x.ID}, RemoveOpts{Apply: true})
	wantSessionBusy(t, err, x.ID)
	_, err = a.RemoveTasks([]string{free.ID}, RemoveOpts{Force: true, Apply: true})
	wantSessionBusy(t, err, waits.ID)
	_, err = a.RemoveEpic(box.ID, RemoveOpts{Apply: true})
	wantSessionBusy(t, err, box.ID)
	if _, _, err := a.Get(free.ID); err != nil {
		t.Fatalf("a refused --force removed the target: %v", err)
	}
	if _, err := a.RemoveTasks([]string{free.ID}, RemoveOpts{Apply: true}); core.AsError(err) == nil || core.AsError(err).Kind != core.KindReferenced {
		t.Errorf("without --force the reference refusal comes first: %v", err)
	}
}
