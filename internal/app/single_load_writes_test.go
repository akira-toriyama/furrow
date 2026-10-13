package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// Every write reads the board once. saveIndex used to read it a SECOND time
// for the write-lint snapshot; load() keeps that copy now.
func TestWritesLoadTheBoardOnce(t *testing.T) {
	cases := []struct {
		name  string
		write func(a *App, id string) error
	}{
		{"add", func(a *App, _ string) error { _, err := a.Add("new", AddOpts{}); return err }},
		{"set -s", func(a *App, id string) error {
			s := "ready"
			_, _, _, err := a.Set(id, SetOpts{Status: &s})
			return err
		}},
		{"set many -e", func(a *App, id string) error {
			e := ""
			_, _, err := a.SetMany([]string{id}, SetOpts{Epic: &e})
			return err
		}},
		{"done", func(a *App, id string) error { _, err := a.Done(id); return err }},
		{"label", func(a *App, id string) error { _, err := a.Relabel(id, []string{"x"}, nil); return err }},
		{"note", func(a *App, id string) error { _, err := a.AddNote(id, "hi"); return err }},
		{"check --add", func(a *App, id string) error { _, err := a.AddChecks(id, []string{"one"}); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, spy := spyApp(t)
			id := seedForEdit(t, a)
			// A FRESH App over the seeded store: the write-lint snapshot is
			// taken on an App's first write, which is where the second read
			// lived; a second write on the same App never showed it.
			fresh := NewWithStore(spy, a.Cfg, a.Clock)
			spy.loads = 0
			if err := c.write(fresh, id); err != nil {
				t.Fatal(err)
			}
			if spy.loads != 1 {
				t.Errorf("%s read the board %d times; want 1", c.name, spy.loads)
			}
		})
	}
}

// A batch `set -e` resolved the box once per task; it resolves once per
// write, and EpicActivate judges and writes on one read of the boxes (three
// reads used to put the slot check and the write on different snapshots).
func TestBoxWritesReadTheBoxesOnce(t *testing.T) {
	a, spy := spyApp(t)
	box, err := a.EpicAdd("box", EpicAddOpts{Repos: []string{"me/x"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		tk, err := a.Add("m", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	spy.epicLoads = 0
	ref := box.ID
	if _, _, err := a.SetMany(ids, SetOpts{Epic: &ref}); err != nil {
		t.Fatal(err)
	}
	if spy.epicLoads > 2 { // validateSetOpts resolves once, resolveEpicOpt once
		t.Errorf("set -e over 3 ids read the boxes %d times; want at most 2, never per task", spy.epicLoads)
	}
	spy.epicLoads = 0
	if _, _, _, err := a.EpicActivate(box.ID, ""); err != nil {
		t.Fatal(err)
	}
	if spy.epicLoads != 1 {
		t.Errorf("epic activate read the boxes %d times; want 1", spy.epicLoads)
	}
}

// tickingClock advances a second on every read, so two reads in one write
// are tellable apart.
type tickingClock struct{ t time.Time }

func (c *tickingClock) Now() time.Time { c.t = c.t.Add(time.Second); return c.t }

// A close carries ONE instant: closed and updated used to come from two
// clock reads (closed 12:00:03, updated 12:00:06 under a ticking clock).
func TestACloseStampsOneInstant(t *testing.T) {
	for _, way := range []string{"set -s done", "done"} {
		t.Run(way, func(t *testing.T) {
			a := newApp()
			a.Clock = &tickingClock{t: time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)}
			tk, err := a.Add("x", AddOpts{})
			if err != nil {
				t.Fatal(err)
			}
			var after *core.Task
			if way == "done" {
				after, err = a.Done(tk.ID)
			} else {
				s := a.Cfg.DoneLane
				after, _, _, err = a.Set(tk.ID, SetOpts{Status: &s})
			}
			if err != nil {
				t.Fatal(err)
			}
			if after.Closed == nil || !after.Closed.Equal(after.Updated) {
				t.Errorf("closed %v updated %v; want one instant", after.Closed, after.Updated)
			}
		})
	}
}

// tidy reports rows in canonical order (lane → priority → id), as ls does —
// it read the raw index, the one non-lint reader that did.
func TestTidyReportsInCanonicalOrder(t *testing.T) {
	a := newApp()
	done := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	idx := &core.Index{Tasks: []core.Task{
		{ID: "t-00001", Title: "ready", Status: "ready", Priority: 10, Created: done, Updated: done, Deps: []string{"t-00003"}},
		{ID: "t-00002", Title: "backlog", Status: "backlog", Priority: 10, Created: done, Updated: done, Deps: []string{"t-00003"}},
		{ID: "t-00003", Title: "gate", Status: a.Cfg.DoneLane, Priority: 10, Created: done, Updated: done, Closed: &done},
	}}
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	rep, err := a.Tidy(TidyOpts{DoneDeps: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DoneDeps) != 2 || rep.DoneDeps[0].ID != "t-00002" || rep.DoneDeps[1].ID != "t-00001" {
		t.Errorf("done_deps order = %+v; want backlog (t-00002) before ready (t-00001), as ls lists them", rep.DoneDeps)
	}
}
