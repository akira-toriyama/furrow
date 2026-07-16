package core

import (
	"errors"
	"reflect"
	"testing"
)

// planIndex builds an index of (id, lane, priority) triples for plan tests.
func planIndex(rows ...[3]any) *Index {
	idx := &Index{}
	for _, r := range rows {
		idx.Add(Task{ID: r[0].(string), Status: r[1].(string), Priority: r[2].(int)})
	}
	return idx
}

func TestPlanRelativePriorityMidpoint(t *testing.T) {
	idx := planIndex(
		[3]any{"t-a", "ready", 10},
		[3]any{"t-b", "ready", 20},
		[3]any{"t-c", "ready", 30},
	)
	// c before b: midpoint of (10, 20).
	got, changes, err := idx.PlanRelativePriority("t-c", "t-b", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != 15 || len(changes) != 0 {
		t.Errorf("before: got %d changes %v, want 15 with no respace", got, changes)
	}
	// a after b: midpoint of (20, 30).
	got, changes, err = idx.PlanRelativePriority("t-a", "t-b", false, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != 25 || len(changes) != 0 {
		t.Errorf("after: got %d changes %v, want 25 with no respace", got, changes)
	}
}

func TestPlanRelativePriorityLaneEdges(t *testing.T) {
	idx := planIndex(
		[3]any{"t-a", "ready", 10},
		[3]any{"t-b", "ready", 20},
		[3]any{"t-z", "ready", 900},
	)
	// Before the lane's first task: first - step (may go negative — priority is
	// any integer, only order matters).
	got, _, err := idx.PlanRelativePriority("t-z", "t-a", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("before-first = %d, want 0", got)
	}
	// After the lane's last task: last + step (same rule as add's NextPriority).
	got, _, err = idx.PlanRelativePriority("t-a", "t-z", false, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != 910 {
		t.Errorf("after-last = %d, want 910", got)
	}
}

func TestPlanRelativePriorityRespacesExhaustedGap(t *testing.T) {
	idx := planIndex(
		[3]any{"t-a", "ready", 10},
		[3]any{"t-b", "ready", 11},
		[3]any{"t-c", "ready", 12},
	)
	target, changes, err := idx.PlanRelativePriority("t-c", "t-b", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Lane respaced to 100,110,120 with c slotted between a and b.
	if target != 110 {
		t.Errorf("target = %d, want 110", target)
	}
	want := []PriorityChange{
		{ID: "t-a", From: 10, To: 100},
		{ID: "t-b", From: 11, To: 120},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %v, want %v", changes, want)
	}
}

func TestPlanRelativePriorityRespacesDuplicates(t *testing.T) {
	// Duplicate priorities (possible via absolute reorder) order by id; the
	// respace resolves them instead of wedging.
	idx := planIndex(
		[3]any{"t-a", "ready", 20},
		[3]any{"t-b", "ready", 20},
		[3]any{"t-c", "ready", 20},
	)
	target, changes, err := idx.PlanRelativePriority("t-c", "t-b", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if target != 110 {
		t.Errorf("target = %d, want 110 (between a and b after respace)", target)
	}
	want := []PriorityChange{
		{ID: "t-a", From: 20, To: 100},
		{ID: "t-b", From: 20, To: 120},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %v, want %v", changes, want)
	}
}

func TestPlanRelativePriorityRespaceSkipsUnmovedNeighbors(t *testing.T) {
	// A neighbor whose respaced value equals its current one is not reported —
	// the changes list is exactly what moved.
	idx := planIndex(
		[3]any{"t-a", "ready", 100},
		[3]any{"t-b", "ready", 101},
		[3]any{"t-c", "ready", 102},
	)
	target, changes, err := idx.PlanRelativePriority("t-c", "t-b", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	if target != 110 {
		t.Errorf("target = %d, want 110", target)
	}
	// t-a is already at its respaced slot (100); only t-b moves.
	want := []PriorityChange{{ID: "t-b", From: 101, To: 120}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %v, want %v", changes, want)
	}
}

func TestPlanRelativePriorityIgnoresOtherLanes(t *testing.T) {
	idx := planIndex(
		[3]any{"t-a", "ready", 10},
		[3]any{"t-b", "ready", 20},
		[3]any{"t-x", "inbox", 15}, // sits "between" a and b numerically, but in another lane
	)
	got, changes, err := idx.PlanRelativePriority("t-b", "t-a", true, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	// b before a with nothing else in the lane: a is first, so first - step.
	if got != 0 || len(changes) != 0 {
		t.Errorf("got %d changes %v, want 0 with no respace (inbox task must not count)", got, changes)
	}
}

func TestPlanRelativePriorityValidation(t *testing.T) {
	idx := planIndex(
		[3]any{"t-a", "ready", 10},
		[3]any{"t-b", "inbox", 20},
	)
	cases := []struct {
		name     string
		id, ref  string
		wantCode Code
	}{
		{"self", "t-a", "t-a", CodeValidation},
		{"cross-lane", "t-a", "t-b", CodeValidation},
		{"id missing", "t-zz", "t-a", CodeNotFound},
		{"ref missing", "t-a", "t-zz", CodeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := idx.PlanRelativePriority(c.id, c.ref, true, 100, 10)
			var fe *Error
			if !errors.As(err, &fe) || fe.Code != c.wantCode {
				t.Errorf("err = %v, want code %d", err, c.wantCode)
			}
		})
	}
}
