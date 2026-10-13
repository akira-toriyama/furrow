package app

import (
	"errors"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// refusingIndexSave is the followers' store refusing (a duplicate id on disk,
// a marshal failure) after the box's edit was accepted.
type refusingIndexSave struct{ Store }

func (refusingIndexSave) Save(*core.Index) error { return errors.New("index refused") }

// A refused follower write leaves the box's day where it was: the box landing
// first made a re-run compute a delta of zero while the followers never caught
// up (t-r6vt).
func TestEpicSetAnchorRefusedFollowerSaveLeavesTheBoxUntouched(t *testing.T) {
	a, _, epic, follower, _, _, _ := anchorFixture(t)
	wasDue := dueOf(t, a, follower.ID)
	inner := a.Store
	a.Store = refusingIndexSave{inner}
	day := "2026-11-28"
	if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: &day}); err == nil {
		t.Fatal("the refused index save must surface")
	}
	a.Store = inner
	e, _, err := a.Store.LoadEpic(epic)
	if err != nil {
		t.Fatal(err)
	}
	if e.Anchor != "2026-11-21" {
		t.Fatalf("box day landed (%s) although its followers were refused", e.Anchor)
	}
	if got := dueOf(t, a, follower.ID); !got.Equal(wasDue) {
		t.Fatalf("follower moved to %s although the write was refused", got)
	}
}

// refusingEpicSave is the box's own store refusing after the followers landed.
type refusingEpicSave struct{ Store }

func (refusingEpicSave) SaveEpic(*core.Epic) error { return errors.New("epic refused") }

func TestEpicSetAnchorRefusedBoxSaveRevertsTheFollowers(t *testing.T) {
	a, _, epic, follower, _, _, _ := anchorFixture(t)
	wasDue := dueOf(t, a, follower.ID)
	inner := a.Store
	a.Store = refusingEpicSave{inner}
	day := "2026-11-28"
	if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: &day}); err == nil {
		t.Fatal("the refused epic save must surface")
	}
	a.Store = inner
	if got := dueOf(t, a, follower.ID); !got.Equal(wasDue) {
		t.Fatalf("follower left at %s after the box save was refused (want %s back)", got, wasDue)
	}
}

// The envelope's `before` is a snapshot, not a view sharing the box's maps and
// slices with the edit: --meta showed the new map on both sides and `changed`
// omitted it.
func TestEpicSetBeforeSnapshotDoesNotShareMeta(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Meta: map[string]string{"place": "here"}})
	before, after, _, err := a.EpicSet(box, EpicSetOpts{SetMeta: map[string]string{"place": "there"}, AddLabels: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if before.Meta["place"] != "here" || after.Meta["place"] != "there" {
		t.Fatalf("before.meta=%v after.meta=%v: before must keep the old map", before.Meta, after.Meta)
	}
	if len(before.Labels) != 0 || len(after.Labels) != 1 {
		t.Fatalf("before.labels=%v after.labels=%v", before.Labels, after.Labels)
	}
}
