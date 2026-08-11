package app

import (
	"testing"
	"time"
)

// Updated is the clock `is:stale`, revisit's stale signal, lint's reconcile-gap
// and `ls --since` all read. Every write path used to stamp it unconditionally,
// so an idempotent retry — the shape an agent's error handling produces —
// rewrote the shard, reset those clocks, and added one more commit for every
// machine sharing the board to sync. The rule now: a write that changes nothing
// PERSISTED leaves the clock alone (App.stampIfChanged), with prose the one
// deliberate exception (the body is content but lives outside the shard).

// noopSeed builds a task carrying a value on every axis the no-op cases below
// re-assert, plus a second task to depend on.
func noopSeed(t *testing.T) (*App, string, string) {
	t.Helper()
	a := newApp()
	dep, err := a.Add("dep target", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Add("seed", AddOpts{
		Status: "ready", Labels: []string{"keep"}, Repos: []string{"me/x"},
		Refs: []string{"a.go:1"}, Deps: []string{dep.ID},
		Value: intptr(3), Effort: intptr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, tk.ID, dep.ID
}

// advance moves the app's clock so a stamp, if one happens, is unmistakable.
func advance(t *testing.T, a *App, d time.Duration) {
	t.Helper()
	c, ok := a.Clock.(*fixedClock)
	if !ok {
		t.Fatalf("expected a fixedClock, got %T", a.Clock)
	}
	c.t = c.t.Add(d)
}

func updatedOf(t *testing.T, a *App, id string) time.Time {
	t.Helper()
	tk, _, err := a.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Updated
}

func TestNoOpMutationsLeaveUpdatedAlone(t *testing.T) {
	cases := []struct {
		name string
		prep func(a *App, id, dep string) error // runs BEFORE the baseline is taken
		edit func(a *App, id, dep string) error
	}{
		{name: "move into the lane it is already in", edit: func(a *App, id, _ string) error {
			_, err := a.Move(id, "ready")
			return err
		}},
		{name: "set with the values it already has", edit: func(a *App, id, _ string) error {
			ready := "ready"
			_, _, err := a.Set(id, SetOpts{Status: &ready, Value: intptr(3), Effort: intptr(2)})
			return err
		}},
		{name: "label --add one it already carries", edit: func(a *App, id, _ string) error {
			_, err := a.Relabel(id, []string{"keep"}, nil)
			return err
		}},
		{name: "label --rm one it does not carry", edit: func(a *App, id, _ string) error {
			_, err := a.Relabel(id, nil, []string{"absent"})
			return err
		}},
		{name: "retitle to the same title", edit: func(a *App, id, _ string) error {
			_, err := a.Retitle(id, "seed")
			return err
		}},
		{name: "value re-set to the score it has", edit: func(a *App, id, _ string) error {
			_, err := a.SetValue(id, intptr(3))
			return err
		}},
		{name: "effort re-set to the score it has", edit: func(a *App, id, _ string) error {
			_, err := a.SetEffort(id, intptr(2))
			return err
		}},
		{name: "repo --add one it already has", edit: func(a *App, id, _ string) error {
			_, err := a.Rerepo(id, []string{"me/x"}, nil)
			return err
		}},
		{name: "ref --add one it already has", edit: func(a *App, id, _ string) error {
			_, err := a.Reref(id, []string{"a.go:1"}, nil)
			return err
		}},
		{name: "dep re-added", edit: func(a *App, id, dep string) error {
			_, err := a.AddDep(id, dep)
			return err
		}},
		{name: "reorder to the priority it has", edit: func(a *App, id, _ string) error {
			tk, _, err := a.Get(id)
			if err != nil {
				return err
			}
			_, err = a.Reorder(id, tk.Priority)
			return err
		}},
		{
			name: "done on a task already in the done lane",
			prep: func(a *App, id, _ string) error { _, err := a.Done(id); return err },
			edit: func(a *App, id, _ string) error { _, err := a.Done(id); return err },
		},
		{
			name: "check toggled back to the state it was in",
			prep: func(a *App, id, _ string) error { _, err := a.AddCheck(id, "one"); return err },
			edit: func(a *App, id, _ string) error { _, err := a.Check(id, 0, false); return err },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, id, dep := noopSeed(t)
			if c.prep != nil {
				if err := c.prep(a, id, dep); err != nil {
					t.Fatal(err)
				}
			}
			want := updatedOf(t, a, id)
			advance(t, a, time.Hour)
			if err := c.edit(a, id, dep); err != nil {
				t.Fatal(err)
			}
			if got := updatedOf(t, a, id); !got.Equal(want) {
				t.Errorf("updated advanced on a no-op: %s -> %s", want, got)
			}
		})
	}
}

// The other half of the contract: a write that DOES change something must still
// stamp — including the prose paths, whose change is invisible to a comparison
// of shard bytes.
func TestRealMutationsStampUpdated(t *testing.T) {
	cases := []struct {
		name string
		edit func(a *App, id, dep string) error
	}{
		{"value changed", func(a *App, id, _ string) error { _, err := a.SetValue(id, intptr(4)); return err }},
		{"value cleared", func(a *App, id, _ string) error { _, err := a.SetValue(id, nil); return err }},
		{"moved to another lane", func(a *App, id, _ string) error { _, err := a.Move(id, "backlog"); return err }},
		{"label added", func(a *App, id, _ string) error { _, err := a.Relabel(id, []string{"new"}, nil); return err }},
		{"dep removed", func(a *App, id, dep string) error { _, err := a.RemoveDep(id, dep); return err }},
		// Prose: the shard is byte-identical afterwards, so these prove the
		// comparison is not the whole rule.
		{"note appended", func(a *App, id, _ string) error { _, err := a.AddNote(id, "progress"); return err }},
		{"body replaced", func(a *App, id, _ string) error { _, err := a.SetBody(id, "# seed\n\nnew"); return err }},
		{"closed with a note", func(a *App, id, _ string) error { _, err := a.DoneNote(id, "superseded"); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, id, dep := noopSeed(t)
			was := updatedOf(t, a, id)
			advance(t, a, time.Hour)
			if err := c.edit(a, id, dep); err != nil {
				t.Fatal(err)
			}
			got := updatedOf(t, a, id)
			if !got.After(was) {
				t.Errorf("a real change must advance updated: %s -> %s", was, got)
			}
			if want := a.Clock.Now(); !got.Equal(want) {
				t.Errorf("updated = %s, want the write's clock %s", got, want)
			}
		})
	}
}

// A batch stamps only the tasks it actually moved, and all of them with ONE
// instant (the single Save is the point; so is the single clock read).
func TestMoveManyStampsOnlyWhatMoved(t *testing.T) {
	a := newApp()
	stay, err := a.Add("already there", AddOpts{Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	moves, err := a.Add("will move", AddOpts{Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	stayWas := updatedOf(t, a, stay.ID)
	advance(t, a, time.Hour)
	if _, err := a.MoveMany([]string{stay.ID, moves.ID}, "ready"); err != nil {
		t.Fatal(err)
	}
	if got := updatedOf(t, a, stay.ID); !got.Equal(stayWas) {
		t.Errorf("a task already in the destination lane was stamped: %s -> %s", stayWas, got)
	}
	if got, want := updatedOf(t, a, moves.ID), a.Clock.Now(); !got.Equal(want) {
		t.Errorf("the moved task's updated = %s, want %s", got, want)
	}
}

// Boxes obey the same rule through their own funnel (mutateEpic), with the same
// prose exception (mutateEpicProse).
func TestEpicNoOpLeavesUpdatedAloneButProseStamps(t *testing.T) {
	a := newApp()
	goal := "ship it"
	e, err := a.EpicAdd("box", EpicAddOpts{Goal: goal})
	if err != nil {
		t.Fatal(err)
	}
	advance(t, a, time.Hour)
	if _, _, err := a.EpicSet(e.ID, EpicSetOpts{Goal: &goal}); err != nil {
		t.Fatal(err)
	}
	got, err := a.EpicShow(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Epic.Updated.Equal(e.Updated) {
		t.Errorf("epic updated advanced on a no-op: %s -> %s", e.Updated, got.Epic.Updated)
	}
	if _, _, err := a.EpicNote(e.ID, "progress"); err != nil {
		t.Fatal(err)
	}
	after, err := a.EpicShow(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := a.Clock.Now(); !after.Epic.Updated.Equal(want) {
		t.Errorf("a box note must stamp updated: got %s want %s", after.Epic.Updated, want)
	}
}
