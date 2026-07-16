package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestReorderRelativeMidpoint(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	ta, _ := a.Add("a", AddOpts{}) // 100
	tb, _ := a.Add("b", AddOpts{}) // 110
	tc, _ := a.Add("c", AddOpts{}) // 120

	clk.t = clk.t.Add(time.Hour)
	got, changes, err := a.ReorderRelative(tc.ID, tb.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 105 {
		t.Errorf("priority = %d, want 105 (midpoint of 100 and 110)", got.Priority)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
	if !got.Updated.Equal(clk.Now()) {
		t.Errorf("id's Updated must advance: got %s", got.Updated)
	}
	// The untouched neighbors keep their priorities.
	if na, _, _ := a.Get(ta.ID); na.Priority != 100 {
		t.Errorf("t-a priority = %d, want 100", na.Priority)
	}
	if nb, _, _ := a.Get(tb.ID); nb.Priority != 110 {
		t.Errorf("t-b priority = %d, want 110", nb.Priority)
	}
}

func TestReorderRelativeRespaceIsOneWrite(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	ta, _ := a.Add("a", AddOpts{})
	tb, _ := a.Add("b", AddOpts{})
	tc, _ := a.Add("c", AddOpts{})
	// Exhaust the gap between a and b.
	if _, err := a.Reorder(ta.ID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reorder(tb.ID, 11); err != nil {
		t.Fatal(err)
	}
	baseline, _, _ := a.Get(ta.ID)

	clk.t = clk.t.Add(time.Hour)
	got, changes, err := a.ReorderRelative(tc.ID, tb.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 110 {
		t.Errorf("target priority = %d, want 110", got.Priority)
	}
	want := []core.PriorityChange{
		{ID: ta.ID, From: 10, To: 100},
		{ID: tb.ID, From: 11, To: 120},
	}
	if len(changes) != 2 || changes[0] != want[0] || changes[1] != want[1] {
		t.Errorf("changes = %v, want %v", changes, want)
	}
	// The respace landed on the neighbors...
	na, _, _ := a.Get(ta.ID)
	nb, _, _ := a.Get(tb.ID)
	if na.Priority != 100 || nb.Priority != 120 {
		t.Errorf("respaced priorities = %d/%d, want 100/120", na.Priority, nb.Priority)
	}
	// ...but did NOT advance their Updated: a respace is positional
	// bookkeeping, not progress, so staleness signals stay honest.
	if !na.Updated.Equal(baseline.Updated) {
		t.Errorf("neighbor Updated moved: %s -> %s", baseline.Updated, na.Updated)
	}
}

func TestReorderRelativeValidationLeavesBoardUntouched(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	ta, _ := a.Add("a", AddOpts{})
	tb, _ := a.Add("b", AddOpts{})
	if _, err := a.Move(tb.ID, "ready"); err != nil {
		t.Fatal(err)
	}

	_, _, err := a.ReorderRelative(ta.ID, tb.ID, true)
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("cross-lane reorder: err = %v, want validation", err)
	}
	na, _, _ := a.Get(ta.ID)
	if na.Priority != ta.Priority {
		t.Errorf("failed reorder must not move priority: %d -> %d", ta.Priority, na.Priority)
	}

	_, _, err = a.ReorderRelative(ta.ID, "t-none", true)
	fe = core.AsError(err)
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("missing ref: err = %v, want not-found", err)
	}
}
