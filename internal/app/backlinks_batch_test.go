package app

import (
	"reflect"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// backlinkIDs is the ids of one BacklinksBatch entry, in returned order.
func backlinkIDs(tasks []core.Task) []string {
	out := []string{}
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestBacklinksBatchMatchesPerIDAndIsSinglePass(t *testing.T) {
	a := newApp()
	target1, _ := a.Add("target one", AddOpts{})
	target2, _ := a.Add("target two", AddOpts{})
	lonely, _ := a.Add("nobody mentions me", AddOpts{})
	// Two mentioners of target1, one of target2, none of lonely. A double
	// mention in one body must still count its author once.
	a.Add("m1", AddOpts{Body: "blocks [[" + target1.ID + "]] and again [[" + target1.ID + "]]"})
	a.Add("m2", AddOpts{Body: "also see [[" + target1.ID + "]] plus [[" + target2.ID + "]]"})

	got, err := a.BacklinksBatch([]string{target1.ID, target2.ID, lonely.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Each requested id's batch result equals calling Backlinks individually.
	for _, id := range []string{target1.ID, target2.ID, lonely.ID} {
		want, err := a.Backlinks(id)
		if err != nil {
			t.Fatalf("Backlinks(%s): %v", id, err)
		}
		if !reflect.DeepEqual(backlinkIDs(got[id]), backlinkIDs(want)) {
			t.Errorf("id %s: batch=%v single=%v", id, backlinkIDs(got[id]), backlinkIDs(want))
		}
	}

	// lonely is present with an empty (non-nil) slice, not missing.
	if v, ok := got[lonely.ID]; !ok || v == nil || len(v) != 0 {
		t.Errorf("unmentioned id should map to [] (present, empty), got %#v ok=%v", got[lonely.ID], ok)
	}
}

func TestBacklinksBatchUnknownIDAbsent(t *testing.T) {
	a := newApp()
	real, _ := a.Add("real", AddOpts{})

	got, err := a.BacklinksBatch([]string{real.ID, "t-nope0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["t-nope0"]; ok {
		t.Errorf("unknown id must be absent from the map, got %#v", got["t-nope0"])
	}
	if _, ok := got[real.ID]; !ok {
		t.Errorf("known id must be present")
	}
}
