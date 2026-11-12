package app

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// A rename reaches the heading `epic add` seeded — and that line only: the
// same text further down the body is prose.
func TestEpicSetTitleCarriesTheSeededHeading(t *testing.T) {
	a, clk := newAppWith()
	box := mustEpic(t, a, "old box", EpicAddOpts{}) // body seeded "# old box\n"
	if err := a.Store.SaveBody(box, "\n# old box\n\nprose\n# old box\n"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Hour)
	title := "  new box  "
	before, after, _, err := a.EpicSet(box, EpicSetOpts{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != "old box" || after.Title != "new box" {
		t.Errorf("title %q → %q, want old box → new box", before.Title, after.Title)
	}
	if body, _ := a.Store.LoadBody(box); body != "\n# new box\n\nprose\n# old box\n" {
		t.Errorf("heading not carried: %q", body)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("updated = %v, want %v", after.Updated, clk.Now())
	}
}

// A heading that no longer reads the title was written by hand; a rename moves
// the shard and leaves the body byte-for-byte. (A task's retitle would rewrite
// the first of these.)
func TestEpicSetTitleLeavesAHandWrittenBody(t *testing.T) {
	for name, body := range map[string]string{
		"a heading that is not the title": "# short name\n\nprose\n",
		"no leading heading":              "prose first\n# old box\n",
		"an empty body":                   "",
	} {
		t.Run(name, func(t *testing.T) {
			a := newApp()
			box := mustEpic(t, a, "old box", EpicAddOpts{})
			if err := a.Store.SaveBody(box, body); err != nil {
				t.Fatal(err)
			}
			title := "new box"
			_, after, _, err := a.EpicSet(box, EpicSetOpts{Title: &title})
			if err != nil {
				t.Fatal(err)
			}
			if after.Title != "new box" {
				t.Errorf("title = %q", after.Title)
			}
			if got, _ := a.Store.LoadBody(box); got != body {
				t.Errorf("body rewritten: %q, want %q", got, body)
			}
		})
	}
}

// The title the box already holds stays the no-op it was: nothing is written
// and the clock is kept, whatever the heading says.
func TestEpicSetSameTitleWritesNothing(t *testing.T) {
	a, clk := newAppWith()
	box := mustEpic(t, a, "old box", EpicAddOpts{})
	if err := a.Store.SaveBody(box, "# drifted\n"); err != nil {
		t.Fatal(err)
	}
	inner := a.Store
	rec := &bodySaves{Store: inner}
	a.Store = rec
	clk.t = clk.t.Add(time.Hour)
	title := "old box"
	before, after, _, err := a.EpicSet(box, EpicSetOpts{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.saved) != 0 {
		t.Errorf("a same-title set wrote the body: %q", rec.saved)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("a same-title set advanced updated to %v", after.Updated)
	}
}

// bodySaves records every body write on its way to the store.
type bodySaves struct {
	Store
	saved []string
}

func (b *bodySaves) SaveBody(id, body string) error {
	b.saved = append(b.saved, body)
	return b.Store.SaveBody(id, body)
}

// refusingBodySave is the body write itself failing (a full disk, a read-only
// bodies/).
type refusingBodySave struct{ Store }

func (refusingBodySave) SaveBody(string, string) error { return errors.New("body refused") }

// The session guard judges the repo a box is GAINING only after the edit is
// computed; the heading must not have moved by then.
func TestEpicSetTitleRefusedByTheGuardLeavesTheHeading(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	var box *core.Epic
	offGuard(a, func() {
		_, _ = a.EpicAdd("occupied", EpicAddOpts{Repos: []string{"o/glyph"}})
		box, _ = a.EpicAdd("old box", EpicAddOpts{Repos: []string{"o/furrow"}})
	})
	title := "new box"
	_, _, _, err := a.EpicSet(box.ID, EpicSetOpts{Title: &title, AddRepos: []string{"o/glyph"}})
	wantSessionBusy(t, err, box.ID)
	if body, _ := a.Store.LoadBody(box.ID); body != "# old box\n" {
		t.Fatalf("a refused rename moved the heading: %q", body)
	}
}

// A refused follower write (the anchor move riding the same call) refuses the
// rename with it, heading included.
func TestEpicSetTitleRefusedFollowerSaveLeavesTheHeading(t *testing.T) {
	a, _, epic, _, _, _, _ := anchorFixture(t)
	inner := a.Store
	a.Store = refusingIndexSave{inner}
	day, title := "2026-11-28", "新会場"
	if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: &day, Title: &title}); err == nil {
		t.Fatal("the refused index save must surface")
	}
	a.Store = inner
	if body, _ := a.Store.LoadBody(epic); body != "# 会場\n" {
		t.Fatalf("a refused rename moved the heading: %q", body)
	}
}

// A refused heading write refuses the rename — the title stays with the
// heading — and takes back the followers that had already landed.
func TestEpicSetTitleRefusedHeadingSaveLeavesTheBox(t *testing.T) {
	a, _, epic, follower, _, _, _ := anchorFixture(t)
	wasDue := dueOf(t, a, follower.ID)
	inner := a.Store
	a.Store = refusingBodySave{inner}
	day, title := "2026-11-28", "新会場"
	if _, _, _, err := a.EpicSet(epic, EpicSetOpts{Anchor: &day, Title: &title}); err == nil {
		t.Fatal("the refused body save must surface")
	}
	a.Store = inner
	e, _, err := a.Store.LoadEpic(epic)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "会場" || e.Anchor != "2026-11-21" {
		t.Fatalf("box landed (title %q, day %s) although its heading was refused", e.Title, e.Anchor)
	}
	if got := dueOf(t, a, follower.ID); !got.Equal(wasDue) {
		t.Fatalf("follower left at %s after the heading was refused (want %s back)", got, wasDue)
	}
}

// The box save failing after the heading landed is the one window left: the
// old body is put back, so the heading does not run ahead of the title.
func TestEpicSetTitleRefusedBoxSavePutsTheHeadingBack(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "old box", EpicAddOpts{})
	inner := a.Store
	rec := &bodySaves{Store: inner}
	a.Store = refusingEpicSave{rec}
	title := "new box"
	if _, _, _, err := a.EpicSet(box, EpicSetOpts{Title: &title}); err == nil {
		t.Fatal("the refused epic save must surface")
	}
	a.Store = inner
	if want := []string{"# new box\n", "# old box\n"}; !slices.Equal(rec.saved, want) {
		t.Fatalf("body writes = %q, want %q (the sync, then the way back)", rec.saved, want)
	}
	if body, _ := a.Store.LoadBody(box); body != "# old box\n" {
		t.Fatalf("heading left ahead of the title: %q", body)
	}
}

func TestRenameHeading(t *testing.T) {
	cases := []struct {
		name, body, from, to, want string
		changed                    bool
	}{
		{"the seeded heading", "# old\nbody\n", "old", "new", "# new\nbody\n", true},
		{"leading blank lines then the heading", "\n\n# old\ntail\n", "old", "new", "\n\n# new\ntail\n", true},
		{"only the leading line", "# old\n\n# old\n", "old", "new", "# new\n\n# old\n", true},
		{"a heading that is not the title", "# short\nbody\n", "old", "new", "# short\nbody\n", false},
		{"a heading that only starts with the title", "# old and more\n", "old", "new", "# old and more\n", false},
		{"trailing space is not the seeded line", "# old \n", "old", "new", "# old \n", false},
		{"the title below prose", "prose\n# old\n", "old", "new", "prose\n# old\n", false},
		{"h2 is not the heading", "## old\n", "old", "new", "## old\n", false},
		{"an empty body is not seeded", "", "old", "new", "", false},
		{"a whitespace body is not seeded", "  \n\n", "old", "new", "  \n\n", false},
		// A task occurrence's back-link is retitleHeading's concern; a box has none.
		{"a back-link line is prose here", "previous: [[e-abc1]]\n\n# old\n", "old", "new", "previous: [[e-abc1]]\n\n# old\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := renameHeading(c.body, c.from, c.to)
			if got != c.want || changed != c.changed {
				t.Errorf("renameHeading(%q, %q, %q) = (%q, %v), want (%q, %v)", c.body, c.from, c.to, got, changed, c.want, c.changed)
			}
		})
	}
}
