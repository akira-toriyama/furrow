package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestSetPriorityAbsolute(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("task", AddOpts{})

	got, changes, err := a.Set(tk.ID, SetOpts{Priority: intp(42)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 42 {
		t.Errorf("priority = %d, want 42", got.Priority)
	}
	if len(changes) != 0 {
		t.Errorf("an absolute priority must not renumber anything: %v", changes)
	}
}

func TestSetLanePlusRelativePositionIsOneWrite(t *testing.T) {
	a := newApp()
	b, _ := a.Add("b", AddOpts{Status: "ready"}) // 100
	c, _ := a.Add("c", AddOpts{Status: "ready"}) // 110
	x, _ := a.Add("x", AddOpts{})                // inbox

	// The cross-column drop: lane AND position land together.
	got, changes, err := a.Set(x.ID, SetOpts{Status: strp("ready"), Before: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" {
		t.Errorf("status = %q, want ready", got.Status)
	}
	if got.Priority != 105 {
		t.Errorf("priority = %d, want 105 (midpoint of %s and %s)", got.Priority, b.ID, c.ID)
	}
	if len(changes) != 0 {
		t.Errorf("midpoint insert must not renumber: %v", changes)
	}
}

func TestSetRelativeRespaceReturnsRenumbered(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	b, _ := a.Add("b", AddOpts{Status: "ready"})
	c, _ := a.Add("c", AddOpts{Status: "ready"})
	x, _ := a.Add("x", AddOpts{})
	if _, err := a.Reorder(b.ID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reorder(c.ID, 11); err != nil {
		t.Fatal(err)
	}
	baseline, _, _ := a.Get(b.ID)

	clk.t = clk.t.Add(time.Hour)
	got, changes, err := a.Set(x.ID, SetOpts{Status: strp("ready"), Before: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 110 {
		t.Errorf("target priority = %d, want 110", got.Priority)
	}
	want := []core.PriorityChange{
		{ID: b.ID, From: 10, To: 100},
		{ID: c.ID, From: 11, To: 120},
	}
	if len(changes) != 2 || changes[0] != want[0] || changes[1] != want[1] {
		t.Errorf("changes = %v, want %v", changes, want)
	}
	// The respaced neighbors moved, but their Updated did not.
	nb, _, _ := a.Get(b.ID)
	if nb.Priority != 100 || !nb.Updated.Equal(baseline.Updated) {
		t.Errorf("neighbor: priority=%d updated moved=%v", nb.Priority, !nb.Updated.Equal(baseline.Updated))
	}
}

func TestSetRelativeValidation(t *testing.T) {
	a := newApp()
	b, _ := a.Add("b", AddOpts{Status: "ready"})
	x, _ := a.Add("x", AddOpts{}) // inbox

	// A relative target must sit in the DESTINATION lane — here no -s is given,
	// so the destination is x's own lane (inbox), and b is not there. Nothing
	// may be half-applied by the failure.
	_, _, err := a.Set(x.ID, SetOpts{Before: b.ID})
	if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("cross-lane target: err = %v, want validation", err)
	}
	nx, _, _ := a.Get(x.ID)
	if nx.Status != "inbox" || nx.Priority != x.Priority {
		t.Errorf("failed set must not mutate: %+v", nx)
	}

	// --priority and --before are exclusive; --before and --after too.
	if _, _, err := a.Set(x.ID, SetOpts{Priority: intp(5), Before: b.ID}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("priority+before: err = %v, want validation", err)
	}
	if _, _, err := a.Set(x.ID, SetOpts{Before: b.ID, After: b.ID}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("before+after: err = %v, want validation", err)
	}
	// Self-reference and a missing target keep their reorder semantics.
	if _, _, err := a.Set(x.ID, SetOpts{After: x.ID}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("self target: err = %v, want validation", err)
	}
	if _, _, err := a.Set(x.ID, SetOpts{After: "t-none"}); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("missing target: err = %v, want not-found", err)
	}
}
