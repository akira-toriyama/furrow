package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ShowBatch is the read behind `show <id>...` taking either entity: membership
// routes each ref, input order is preserved across the two stores, and a ref
// naming neither is a MISS (data) rather than the box resolver's exit 2 — a
// resolution error would throw away the entries that were found.
func TestShowBatchRoutesAcrossEntities(t *testing.T) {
	a := newApp()
	tk, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	epic := mustEpic(t, a, "a box", EpicAddOpts{Repos: []string{"o/r"}})

	entries, missing, err := a.ShowBatch([]string{epic, "t-nope0", tk.ID, epic}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries (the duplicate collapses), got %d", len(entries))
	}
	if entries[0].Epic == nil || entries[0].Epic.Epic.ID != epic {
		t.Errorf("entry 0 should be the box %s, got %+v", epic, entries[0])
	}
	if entries[0].Task != nil {
		t.Error("a box entry must not also carry a task")
	}
	if entries[1].Task == nil || entries[1].Task.Task.ID != tk.ID {
		t.Errorf("entry 1 should be the task %s, got %+v", tk.ID, entries[1])
	}
	if len(missing) != 1 || missing[0] != "t-nope0" {
		t.Errorf("missing = %v, want [t-nope0]", missing)
	}

	// The box's members ride along, exactly as `epic show` returns them.
	if _, _, err := a.Set(tk.ID, SetOpts{Epic: strptr(epic)}); err != nil {
		t.Fatal(err)
	}
	entries, _, err = a.ShowBatch([]string{epic}, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(entries[0].Epic.Tasks); n != 1 {
		t.Errorf("the box should carry its 1 member, got %d", n)
	}
}

// Dedup is by the RESOLVED id, not the ref: a box answers to its id, a unique
// id prefix and a unique title substring, so a ref-keyed set would render it
// once per spelling and inflate `show`'s "N of M ids not found" arithmetic.
func TestShowBatchDedupesResolvedEntities(t *testing.T) {
	a := newApp()
	epic := mustEpic(t, a, "the alpha box", EpicAddOpts{Repos: []string{"o/r"}})

	entries, missing, err := a.ShowBatch([]string{epic, epic[:4], "alpha", "t-nope0", "t-nope0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		ids := make([]string, len(entries))
		for i := range entries {
			ids[i] = entries[i].Epic.Epic.ID
		}
		t.Fatalf("three refs for one box should collapse to 1 entry, got %d: %v", len(entries), ids)
	}
	// A repeated MISS still collapses too — that dedup is ref-level by nature.
	if len(missing) != 1 {
		t.Errorf("missing = %v, want one entry", missing)
	}
}

// --no-body means one thing for both entities.
func TestShowBatchNoBodyDropsBothProse(t *testing.T) {
	a := newApp()
	tk, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	epic := mustEpic(t, a, "a box", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicNote(epic, "box progress"); err != nil {
		t.Fatal(err)
	}

	withBody, _, err := a.ShowBatch([]string{tk.ID, epic}, true)
	if err != nil {
		t.Fatal(err)
	}
	if withBody[0].Task.Body == "" || withBody[1].Epic.Body == "" {
		t.Fatalf("precondition: both bodies should be present, got %q / %q",
			withBody[0].Task.Body, withBody[1].Epic.Body)
	}
	lean, _, err := a.ShowBatch([]string{tk.ID, epic}, false)
	if err != nil {
		t.Fatal(err)
	}
	if lean[0].Task.Body != "" {
		t.Errorf("--no-body left a task body: %q", lean[0].Task.Body)
	}
	if lean[1].Epic.Body != "" {
		t.Errorf("--no-body left a box body: %q", lean[1].Epic.Body)
	}
}

// EditPath routes the same way. memstore is not file-backed, so the happy path
// ends in the documented exit-3 (see editpath_test.go's characterization) —
// which is itself the proof that resolution got PAST the task index and found
// the box. An unknown box keeps the epic resolver's exit 2 + candidates.
func TestEditPathRoutesToTheBox(t *testing.T) {
	a := newApp()
	epic := mustEpic(t, a, "a box", EpicAddOpts{Repos: []string{"o/r"}})

	_, err := a.EditPath(epic)
	if fe := core.AsError(err); fe == nil || fe.Code != core.CodeInternal {
		t.Errorf("a resolved box on a memory store should be the not-file-backed exit 3, got %v", err)
	}
	// A unique title substring resolves too — the epic-ref contract.
	if _, err := a.EditPath("box"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeInternal {
		t.Errorf("a title substring should resolve to the box, got %v", err)
	}

	_, err = a.EditPath("e-nope0")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("unknown box want CodeValidation, got %v", err)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown box should carry candidates: %+v", fe)
	}

	// A task-shaped ref that names nothing keeps the task path's NotFound.
	if _, err := a.EditPath("t-nope0"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeNotFound {
		t.Errorf("unknown task id want CodeNotFound, got %v", err)
	}
}

func strptr(s string) *string { return &s }
