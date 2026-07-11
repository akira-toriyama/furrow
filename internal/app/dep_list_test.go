package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestDepListBothDirections(t *testing.T) {
	a := newApp()
	base, _ := a.Add("base task", AddOpts{})
	mid, _ := a.Add("middle task", AddOpts{Deps: []string{base.ID}}) // mid depends on base
	top, _ := a.Add("top task", AddOpts{Deps: []string{mid.ID}})     // top depends on mid
	_ = top

	res, err := a.DepList(mid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != mid.ID || res.Title != "middle task" {
		t.Errorf("subject wrong: %+v", res)
	}
	// depends_on: mid waits on base, resolved to id+title+status.
	if len(res.DependsOn) != 1 || res.DependsOn[0].ID != base.ID ||
		res.DependsOn[0].Title != "base task" || res.DependsOn[0].Status != "inbox" {
		t.Errorf("depends_on should resolve base, got %+v", res.DependsOn)
	}
	// blocks (reverse): top waits on mid.
	if len(res.Blocks) != 1 || res.Blocks[0].ID != top.ID || res.Blocks[0].Title != "top task" {
		t.Errorf("blocks should resolve top, got %+v", res.Blocks)
	}
}

func TestDepListEmptyNeighborhoodIsCleanObject(t *testing.T) {
	a := newApp()
	lone, _ := a.Add("lonely", AddOpts{})
	res, err := a.DepList(lone.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Non-nil empty slices so the JSON is [] not null.
	if res.DependsOn == nil || res.Blocks == nil {
		t.Fatalf("empty edges must be non-nil slices, got %+v", res)
	}
	if len(res.DependsOn) != 0 || len(res.Blocks) != 0 {
		t.Fatalf("a lone task should have no edges, got %+v", res)
	}
}

func TestDepListNotFound(t *testing.T) {
	a := newApp()
	a.Add("something", AddOpts{})
	if _, err := a.DepList("t-zzzzz"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeNotFound {
		t.Fatalf("an unknown id should be NotFound (exit 1), got %v", err)
	}
}

func TestDepListDanglingDepResolvesToIDOnly(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("has a dangling dep", AddOpts{})
	// Inject a dangling dep below the app layer (AddDeps would reject a missing
	// id); this is the shape lint reports as dangling-dep.
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, i := idx.Find(tk.ID)
	if i < 0 {
		t.Fatal("seed task vanished")
	}
	idx.Tasks[i].Deps = []string{"t-ghost"}
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	res, err := a.DepList(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DependsOn) != 1 || res.DependsOn[0].ID != "t-ghost" ||
		res.DependsOn[0].Title != "" || res.DependsOn[0].Status != "" {
		t.Errorf("a dangling dep should resolve to the id alone, got %+v", res.DependsOn)
	}
}
