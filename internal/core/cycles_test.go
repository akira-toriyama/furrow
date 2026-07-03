package core

import (
	"strings"
	"testing"
)

func TestCycleProblems(t *testing.T) {
	cases := []struct {
		name       string
		tasks      []Task
		wantCycles int
		wantIDs    []string // every id that must appear in the single cycle message
	}{
		{
			name:       "acyclic chain has no cycle",
			tasks:      []Task{{ID: "t-1", Deps: []string{"t-2"}}, {ID: "t-2", Deps: []string{"t-3"}}, {ID: "t-3"}},
			wantCycles: 0,
		},
		{
			name:       "two-node cycle",
			tasks:      []Task{{ID: "t-a", Deps: []string{"t-b"}}, {ID: "t-b", Deps: []string{"t-a"}}},
			wantCycles: 1,
			wantIDs:    []string{"t-a", "t-b"},
		},
		{
			name:       "self dependency is a one-node cycle, reported once",
			tasks:      []Task{{ID: "t-x", Deps: []string{"t-x"}}},
			wantCycles: 1,
			wantIDs:    []string{"t-x"},
		},
		{
			name: "three-node cycle",
			tasks: []Task{
				{ID: "t-a", Deps: []string{"t-b"}},
				{ID: "t-b", Deps: []string{"t-c"}},
				{ID: "t-c", Deps: []string{"t-a"}},
			},
			wantCycles: 1,
			wantIDs:    []string{"t-a", "t-b", "t-c"},
		},
		{
			name:       "unknown dep contributes no edge (no false cycle)",
			tasks:      []Task{{ID: "t-a", Deps: []string{"t-missing"}}},
			wantCycles: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ps := CycleProblems(&Index{Tasks: c.tasks})
			if len(ps) != c.wantCycles {
				t.Fatalf("got %d cycle problems, want %d: %+v", len(ps), c.wantCycles, ps)
			}
			for _, p := range ps {
				if p.Severity != SevError {
					t.Errorf("a cycle must be an error, got %q", p.Severity)
				}
			}
			if c.wantCycles == 1 {
				for _, id := range c.wantIDs {
					if !strings.Contains(ps[0].Msg, id) {
						t.Errorf("cycle message %q should mention %s", ps[0].Msg, id)
					}
				}
			}
		})
	}
}

func TestCycleProblemsDedupesAcrossEntryPoints(t *testing.T) {
	// A 3-cycle with an extra edge feeding into it: DFS can reach the loop from
	// two starts, but it must be reported exactly once.
	idx := &Index{Tasks: []Task{
		{ID: "t-a", Deps: []string{"t-b"}},
		{ID: "t-b", Deps: []string{"t-c"}},
		{ID: "t-c", Deps: []string{"t-a"}},
		{ID: "t-d", Deps: []string{"t-a"}}, // into the cycle, not part of it
	}}
	if ps := CycleProblems(idx); len(ps) != 1 {
		t.Fatalf("expected exactly one cycle problem, got %d: %+v", len(ps), ps)
	}
}

func TestCycleProblemsTwoDisjointCycles(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-a", Deps: []string{"t-b"}}, {ID: "t-b", Deps: []string{"t-a"}},
		{ID: "t-c", Deps: []string{"t-d"}}, {ID: "t-d", Deps: []string{"t-c"}},
	}}
	if ps := CycleProblems(idx); len(ps) != 2 {
		t.Fatalf("expected two disjoint cycles, got %d: %+v", len(ps), ps)
	}
}
