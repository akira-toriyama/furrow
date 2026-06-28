package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

var idRe = regexp.MustCompile(`^t-[0-9a-z]{5}$`)

// fixedClock is a deterministic Clock for tests.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

func newApp() *App {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk)
}

func TestAddAssignsRandomIDAndSparsePriority(t *testing.T) {
	a := newApp()
	t1, err := a.Add("first", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !idRe.MatchString(t1.ID) || t1.Status != "inbox" || t1.Priority != 100 {
		t.Errorf("first task wrong: %+v", t1)
	}
	t2, _ := a.Add("second", AddOpts{})
	if t2.ID == t1.ID || t2.Priority != 110 { // distinct id, same lane -> +step
		t.Errorf("second task should get a distinct id + priority 110: %+v", t2)
	}
	if t2.Body != "bodies/"+t2.ID+".md" {
		t.Errorf("body path should match the id: %q", t2.Body)
	}
	// body file seeded from the title
	if body, _ := a.Store.LoadBody(t2.ID); body != "# second\n" {
		t.Errorf("body should seed a heading, got %q", body)
	}
}

func TestAddGeneratesUniqueRandomIDs(t *testing.T) {
	a := newApp() // random (unseeded) memstore
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tk, err := a.Add("task", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if !idRe.MatchString(tk.ID) {
			t.Fatalf("id %q does not match the random pattern", tk.ID)
		}
		if seen[tk.ID] {
			t.Fatalf("Add produced a duplicate id: %q (uniqueID retry should prevent this)", tk.ID)
		}
		seen[tk.ID] = true
	}
	// the whole index is internally consistent (no duplicate-id lint error).
	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		if strings.Contains(p.Msg, "duplicate id") {
			t.Errorf("unexpected duplicate-id problem: %+v", p)
		}
	}
}

func TestAddManyGeneratesUniqueIDs(t *testing.T) {
	a := newApp() // random (unseeded) memstore
	specs := make([]AddSpec, 300)
	for i := range specs {
		specs[i] = AddSpec{Title: "batch", AddOpts: AddOpts{Status: "ready"}}
	}
	created, err := a.AddMany(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != len(specs) {
		t.Fatalf("AddMany created %d tasks, want %d", len(created), len(specs))
	}
	// every id in one batch must be distinct (uniqueID checks the accumulating index).
	seen := map[string]bool{}
	for _, tk := range created {
		if !idRe.MatchString(tk.ID) {
			t.Errorf("id %q does not match the random pattern", tk.ID)
		}
		if seen[tk.ID] {
			t.Fatalf("AddMany produced a duplicate id within one batch: %q", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestAddRejectsUnknownLaneAndEmptyTitle(t *testing.T) {
	a := newApp()
	if _, err := a.Add("x", AddOpts{Status: "ghost"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown lane should be a validation error, got %v", err)
	}
	if _, err := a.Add("   ", AddOpts{}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty title should be a validation error, got %v", err)
	}
}

func TestAddDepAndRemoveDep(t *testing.T) {
	a := newApp()
	ta, _ := a.Add("a", AddOpts{})
	tb, _ := a.Add("b", AddOpts{})
	tc, _ := a.Add("c", AddOpts{})

	// b depends on a; c depends on b.
	if _, err := a.AddDep(tb.ID, ta.ID); err != nil {
		t.Fatalf("AddDep b->a: %v", err)
	}
	if _, err := a.AddDep(tc.ID, tb.ID); err != nil {
		t.Fatalf("AddDep c->b: %v", err)
	}

	// Re-adding is idempotent (no duplicate dep).
	t2, err := a.AddDep(tb.ID, ta.ID)
	if err != nil {
		t.Fatalf("idempotent AddDep: %v", err)
	}
	if len(t2.Deps) != 1 || t2.Deps[0] != ta.ID {
		t.Errorf("re-adding a dep must not duplicate it, got %v", t2.Deps)
	}

	// Self-dependency is rejected.
	if _, err := a.AddDep(ta.ID, ta.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("self-dep should be a validation error, got %v", err)
	}
	// Unknown dependency is rejected.
	if _, err := a.AddDep(tb.ID, "t-9999"); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown dep should be a validation error, got %v", err)
	}
	// Cycle is rejected: a->c would close the c->b->a chain.
	if _, err := a.AddDep(ta.ID, tc.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("cycle-creating dep should be a validation error, got %v", err)
	}

	// Remove a real dep.
	rt, err := a.RemoveDep(tb.ID, ta.ID)
	if err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}
	if len(rt.Deps) != 0 {
		t.Errorf("dep should be removed, got %v", rt.Deps)
	}
	// Removing a non-existent dep is a validation error (not a silent no-op).
	if _, err := a.RemoveDep(tb.ID, ta.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("removing a non-dependency should be a validation error, got %v", err)
	}
	// Unknown task id is NotFound.
	if _, err := a.AddDep("t-9999", ta.ID); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("AddDep on unknown id should be NotFound, got %v", err)
	}
}

func TestDoneStampsClosedAndMoveClears(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("ship it", AddOpts{Status: "ready"})
	done, err := a.Done(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" || done.Closed == nil {
		t.Errorf("done should set lane=done + Closed: %+v", done)
	}
	// moving back out of done clears Closed.
	back, _ := a.Move(tk.ID, "ready")
	if back.Closed != nil {
		t.Errorf("moving out of done should clear Closed, got %v", back.Closed)
	}
	// moving to icebox (terminal but not done) does NOT stamp Closed.
	ice, _ := a.Move(tk.ID, "icebox")
	if ice.Closed != nil {
		t.Errorf("icebox should not stamp Closed: %v", ice.Closed)
	}
}

func TestNextSkipsBlockedAndTerminal(t *testing.T) {
	a := newApp()
	base, _ := a.Add("base", AddOpts{Status: "ready"})
	a.Add("blocked", AddOpts{Status: "ready", Deps: []string{base.ID}})
	a.Add("parked", AddOpts{Status: "icebox"})

	next, err := a.Next("", 0)
	if err != nil {
		t.Fatal(err)
	}
	// only "base" is actionable: blocked waits on an open dep, parked is terminal.
	if len(next) != 1 || next[0].ID != base.ID {
		t.Errorf("next should be [base], got %+v", next)
	}

	// finishing base unblocks the dependent task.
	a.Done(base.ID)
	next, _ = a.Next("", 0)
	if len(next) != 1 || next[0].Title != "blocked" {
		t.Errorf("after base done, next should be [blocked], got %+v", next)
	}
}

func TestNextOnlyConsidersNextLanes(t *testing.T) {
	a := newApp() // default next-lanes = ready + in-progress
	a.Add("intake", AddOpts{Status: "inbox"})
	a.Add("planned", AddOpts{Status: "backlog"})
	r, _ := a.Add("ready one", AddOpts{Status: "ready"})
	a.Add("doing", AddOpts{Status: "in-progress"})

	next, err := a.Next("", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tk := range next {
		got[tk.Status] = true
	}
	if got["inbox"] || got["backlog"] {
		t.Errorf("next must exclude inbox/backlog by default, got %+v", titlesOf(next))
	}
	if !got["ready"] || !got["in-progress"] {
		t.Errorf("next must include ready + in-progress, got %+v", titlesOf(next))
	}
	if len(next) != 2 {
		t.Errorf("expected 2 actionable tasks, got %d", len(next))
	}
	_ = r
}

func TestNextFiltersByLabel(t *testing.T) {
	a := newApp() // default next-lanes = ready + in-progress
	a.Add("furrow task", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	a.Add("facet task", AddOpts{Status: "ready", Labels: []string{"facet"}})
	a.Add("both", AddOpts{Status: "in-progress", Labels: []string{"furrow", "facet"}})

	if all, _ := a.Next("", 0); len(all) != 3 {
		t.Fatalf("no label filter should return all 3 actionable, got %d", len(all))
	}

	furrowOnly, err := a.Next("furrow", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(furrowOnly) != 2 {
		t.Fatalf("--label furrow should return 2 (furrow task + both), got %+v", titlesOf(furrowOnly))
	}
	for _, tk := range furrowOnly {
		if !contains(tk.Labels, "furrow") {
			t.Errorf("filtered task %q lacks the furrow label", tk.Title)
		}
	}

	if none, _ := a.Next("nope", 0); len(none) != 0 {
		t.Errorf("unknown label should return no tasks, got %d", len(none))
	}
}

func titlesOf(ts []core.Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Status+":"+t.Title)
	}
	return out
}

func TestCheckTogglesChecklist(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("with steps", AddOpts{})
	a.AddCheck(tk.ID, "step one")
	a.AddCheck(tk.ID, "step two")
	got, _ := a.Check(tk.ID, 1, true)
	if len(got.Checklist) != 2 || !got.Checklist[1].Done || got.Checklist[0].Done {
		t.Errorf("check should toggle only item 1: %+v", got.Checklist)
	}
}

func TestLabelsRequiredEnforced(t *testing.T) {
	cfg := config.Default()
	cfg.LabelsRequired = true
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	a := NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})

	// add without a label -> validation error.
	if _, err := a.Add("no label", AddOpts{Status: "ready"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add without a label should be a validation error when required, got %v", err)
	}
	// add with a label -> ok.
	if _, err := a.Add("with label", AddOpts{Status: "ready", Labels: []string{"furrow"}}); err != nil {
		t.Errorf("add with a label should succeed, got %v", err)
	}

	// lint flags a label-less task (injected directly, with a body so the only
	// problem is the missing label).
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-0099", Title: "ghost", Status: "ready", Body: core.BodyPath("t-0099")})
	a.Store.Save(idx)
	a.Store.SaveBody("t-0099", "# ghost\n")
	ps, _ := a.Lint()
	var found bool
	for _, p := range ps {
		if p.ID == "t-0099" && p.Severity == core.SevError && contains2(p.Msg, "label") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 as label-less, got %+v", ps)
	}

	// default config (not required) accepts a label-less add.
	cfg2 := config.Default()
	a2 := NewWithStore(memstore.New(cfg2.IDPrefix, cfg2.IDWidth), cfg2, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})
	if _, err := a2.Add("fine", AddOpts{Status: "ready"}); err != nil {
		t.Errorf("label-less add should succeed when not required, got %v", err)
	}
}

func contains2(s, sub string) bool { return strings.Contains(s, sub) }

func TestCheckOutOfRangeIsValidationError(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("steps", AddOpts{})
	a.AddCheck(tk.ID, "only item") // index 0 valid; 1+ and negatives invalid
	if _, err := a.Check(tk.ID, 5, true); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("out-of-range index should be a validation error, got %v", err)
	}
	if _, err := a.Check(tk.ID, -1, true); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("negative index should be a validation error, got %v", err)
	}
	// a missing id is still NotFound, not validation.
	if _, err := a.Check("t-9999", 0, true); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("missing id should be not-found, got %v", err)
	}
	// in-range still works.
	if _, err := a.Check(tk.ID, 0, true); err != nil {
		t.Errorf("in-range check should succeed, got %v", err)
	}
}

func TestArchiveCommitsBeforeDeletingBodies(t *testing.T) {
	// End-to-end archive against a real fsstore: aged done task moves to
	// .furrow/archive/, hot body is gone, archive body+index present, hot lint clean.
	dir := t.TempDir()
	ia, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	// fix the clock far in the future so the task is "old".
	ia.Clock = &fixedClock{t: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

	// create a done task closed long ago by injecting via the store.
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-0001", Title: "old done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Body: core.BodyPath("t-0001")})
	ia.Store.Save(idx)
	ia.Store.SaveBody("t-0001", "# old done\n")

	moved, err := ia.Archive(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].ID != "t-0001" {
		t.Fatalf("expected to archive t-0001, got %+v", moved)
	}
	// hot store: task gone from index AND body deleted.
	hot, _ := ia.Store.Load()
	if hot.Has("t-0001") {
		t.Error("archived task should be removed from the hot index")
	}
	if ia.Store.BodyExists("t-0001") {
		t.Error("archived task's hot body should be deleted")
	}
	// archive store: index + body present.
	arcIdxPath := filepath.Join(ia.Dir, "archive", "index.json")
	if _, err := os.Stat(arcIdxPath); err != nil {
		t.Errorf("archive index.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ia.Dir, "archive", "bodies", "t-0001.md")); err != nil {
		t.Errorf("archive body should exist: %v", err)
	}
	// hot store is consistent.
	ps, _ := ia.Lint()
	if core.HasErrors(ps) {
		t.Errorf("hot store should lint clean after archive, got %+v", ps)
	}
}

func TestArchivableSelection(t *testing.T) {
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	idx := &core.Index{Tasks: []core.Task{
		{ID: "t-1", Status: "done", Closed: &old},
		{ID: "t-2", Status: "done", Closed: &recent},
		{ID: "t-3", Status: "ready"},
		{ID: "t-4", Status: "icebox"}, // terminal but no Closed -> not archivable
	}}
	cutoff := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	ids := Archivable(idx, "done", cutoff)
	if len(ids) != 1 || ids[0] != "t-1" {
		t.Errorf("Archivable = %v, want [t-1]", ids)
	}
}

func TestLintFlagsMissingBody(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("has body", AddOpts{})
	// inject a task with no body file directly into the store.
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-0099", Title: "ghost", Status: "ready", Body: core.BodyPath("t-0099")})
	a.Store.Save(idx)

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range ps {
		if p.ID == "t-0099" && p.Severity == core.SevError {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 (no body), got %+v", ps)
	}
	_ = tk
}

func TestRelabelAddsRemovesIdempotently(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("x", AddOpts{Labels: []string{"chord", "shared"}})

	// Add one label, remove another in a single call.
	got, err := a.Relabel(tk.ID, []string{"sill"}, []string{"chord"})
	if err != nil {
		t.Fatalf("Relabel: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("labels = %q, want %q", join, "shared,sill")
	}

	// Adding an existing label is a no-op (no duplicate).
	got, err = a.Relabel(tk.ID, []string{"sill"}, nil)
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("idempotent add changed labels: %q", join)
	}

	// Removing an absent label is a no-op (not an error).
	got, err = a.Relabel(tk.ID, nil, []string{"nope"})
	if err != nil {
		t.Fatalf("removing an absent label should be a no-op, got %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("absent remove changed labels: %q", join)
	}

	// No flags at all is a bad-usage validation error (never a silent no-op).
	if _, err := a.Relabel(tk.ID, nil, nil); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("relabel with no add/remove should be a validation error, got %v", err)
	}

	// Unknown id is NotFound.
	if _, err := a.Relabel("t-9999", []string{"x"}, nil); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("relabel on unknown id should be NotFound, got %v", err)
	}
}

func TestRelabelRespectsLabelsRequired(t *testing.T) {
	cfg := config.Default()
	cfg.LabelsRequired = true
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	a := NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})
	tk, err := a.Add("x", AddOpts{Labels: []string{"only"}})
	if err != nil {
		t.Fatal(err)
	}

	// Removing the last label is rejected when labels are required.
	if _, err := a.Relabel(tk.ID, nil, []string{"only"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("removing the last required label should be a validation error, got %v", err)
	}

	// Swapping (add a new one while removing the old) is fine: the result is non-empty.
	got, err := a.Relabel(tk.ID, []string{"new"}, []string{"only"})
	if err != nil {
		t.Fatalf("swap relabel: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "new" {
		t.Errorf("labels = %q, want %q", join, "new")
	}
}
