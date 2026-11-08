package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// bite-exempt: pins a convention, not a fix — the pre-change tree had no single
// lookup to disagree with EpicsByID (four loops took the first copy, four the
// last), so there is no earlier source for this assertion to fail against.
//
// On a duplicate epic id the slice lookup and the map lookup must hand out the
// SAME copy (the later shard, core.EpicsByID's rule): a read that resolves
// through one and a write that resolves through the other would otherwise
// show one copy and rewrite the other.
func TestFindEpicAgreesWithEpicsByIDOnDuplicate(t *testing.T) {
	epics := []core.Epic{{ID: "e-dup", Title: "first"}, {ID: "e-other"}, {ID: "e-dup", Title: "later"}}
	byMap := core.EpicsByID(epics)["e-dup"]
	bySlice := findEpic(epics, "e-dup")
	if bySlice != byMap {
		t.Fatalf("findEpic = %q, EpicsByID = %q — the two lookups name different copies", bySlice.Title, byMap.Title)
	}
	if bySlice.Title != "later" {
		t.Fatalf("findEpic picked %q, want the later shard", bySlice.Title)
	}
	if findEpic(epics, "e-none") != nil {
		t.Fatal("findEpic must be nil for an id no box carries")
	}
}
