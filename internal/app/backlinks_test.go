package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestBacklinks(t *testing.T) {
	a := newApp()
	target, _ := a.Add("target", AddOpts{Status: "ready"})
	// Two tasks mention the target via [[id]], both in the same lane so the
	// expected order is by priority (creation order): t1 before t2.
	t1, _ := a.Add("mentions target", AddOpts{Status: "ready", Body: "# t1\n\nblocks [[" + target.ID + "]]\n"})
	t2, _ := a.Add("also mentions target", AddOpts{Status: "ready", Body: "see [[" + target.ID + "]] above"})
	// A bare id (no brackets) is not a mention.
	a.Add("bare only", AddOpts{Status: "ready", Body: "target is " + target.ID + ", unlinked"})
	// A self-mention is not a backlink.
	if err := a.Store.SaveBody(target.ID, "# target\n\nrefers to itself [["+target.ID+"]]\n"); err != nil {
		t.Fatal(err)
	}
	// An orphan body (no task) that mentions the target must not surface.
	if err := a.Store.SaveBody("t-orph1", "[["+target.ID+"]]"); err != nil {
		t.Fatal(err)
	}

	got, err := a.Backlinks(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]string, len(got))
	for i, task := range got {
		gotIDs[i] = task.ID
	}
	want := []string{t1.ID, t2.ID}
	if len(gotIDs) != len(want) || gotIDs[0] != want[0] || gotIDs[1] != want[1] {
		t.Errorf("Backlinks(%s) = %v, want %v (canonical order, self/bare/orphan excluded)", target.ID, gotIDs, want)
	}
}

func TestBacklinksUnknownIDIsNotFound(t *testing.T) {
	a := newApp()
	a.Add("some task", AddOpts{})
	_, err := a.Backlinks("t-nope0")
	if fe := core.AsError(err); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("Backlinks on unknown id should be NotFound, got %v", err)
	}
}

func TestBacklinksNoneIsEmpty(t *testing.T) {
	a := newApp()
	target, _ := a.Add("lonely", AddOpts{})
	got, err := a.Backlinks(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no backlinks, got %v", got)
	}
}
