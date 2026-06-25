package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// fixedClock is a deterministic Clock for tests.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

func newApp() *App {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk)
}

func TestAddAssignsFrozenIDAndSparsePriority(t *testing.T) {
	a := newApp()
	t1, err := a.Add("first", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if t1.ID != "t-0001" || t1.Status != "inbox" || t1.Priority != 100 {
		t.Errorf("first task wrong: %+v", t1)
	}
	t2, _ := a.Add("second", AddOpts{})
	if t2.ID != "t-0002" || t2.Priority != 110 { // same lane -> +step
		t.Errorf("second task should get id t-0002 priority 110: %+v", t2)
	}
	if t2.Body != "bodies/t-0002.md" {
		t.Errorf("body path wrong: %q", t2.Body)
	}
	// body file seeded from the title
	if body, _ := a.Store.LoadBody("t-0002"); body != "# second\n" {
		t.Errorf("body should seed a heading, got %q", body)
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

	next, err := a.Next(0)
	if err != nil {
		t.Fatal(err)
	}
	// only "base" is actionable: blocked waits on an open dep, parked is terminal.
	if len(next) != 1 || next[0].ID != base.ID {
		t.Errorf("next should be [base], got %+v", next)
	}

	// finishing base unblocks the dependent task.
	a.Done(base.ID)
	next, _ = a.Next(0)
	if len(next) != 1 || next[0].Title != "blocked" {
		t.Errorf("after base done, next should be [blocked], got %+v", next)
	}
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
