package core

import (
	"strings"
	"testing"
)

func TestReadyBlockedProblems(t *testing.T) {
	next := map[string]bool{"ready": true, "in-progress": true}
	doneIDs := map[string]bool{"dep-done": true}

	cases := []struct {
		name    string
		tasks   []Task
		wantIDs []string // task ids that must each own exactly one ERROR
	}{
		{
			name: "ready task with an open dep errors",
			tasks: []Task{
				{ID: "t-a", Status: "ready", Deps: []string{"dep-open"}},
				{ID: "dep-open", Status: "backlog"},
			},
			wantIDs: []string{"t-a"},
		},
		{
			name: "ready task whose deps are all done is clean",
			tasks: []Task{
				{ID: "t-a", Status: "ready", Deps: []string{"dep-done"}},
			},
		},
		{
			name: "blocked task outside the next lanes is not this rule's business",
			tasks: []Task{
				{ID: "t-a", Status: "backlog", Deps: []string{"dep-open"}},
				{ID: "dep-open", Status: "backlog"},
			},
		},
		{
			// Actionable treats an unknown dep as unsatisfied (next never hands
			// the task out), so the lane contradiction is just as real here.
			name: "in-progress task with an unknown dep errors too",
			tasks: []Task{
				{ID: "t-a", Status: "in-progress", Deps: []string{"dep-nowhere"}},
			},
			wantIDs: []string{"t-a"},
		},
		{
			name: "one finding per task naming every unsatisfied dep",
			tasks: []Task{
				{ID: "t-a", Status: "ready", Deps: []string{"dep-done", "dep-x", "dep-y"}},
				{ID: "dep-x", Status: "backlog"},
				{ID: "dep-y", Status: "backlog"},
			},
			wantIDs: []string{"t-a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := &Index{Tasks: tc.tasks}
			ps := ReadyBlockedProblems(idx, next, doneIDs)
			if len(ps) != len(tc.wantIDs) {
				t.Fatalf("got %d problem(s), want %d: %+v", len(ps), len(tc.wantIDs), ps)
			}
			for i, id := range tc.wantIDs {
				p := ps[i]
				if p.ID != id || p.Severity != SevError || p.Code != "ready-blocked" {
					t.Errorf("problem %d = %+v, want ready-blocked ERROR on %s", i, p, id)
				}
			}
		})
	}
}

// The finding names every unsatisfied dep and only those — a done dep must not
// be listed as blocking.
func TestReadyBlockedNamesOnlyOpenDeps(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-a", Status: "ready", Deps: []string{"dep-done", "dep-x", "dep-y"}},
		{ID: "dep-x", Status: "backlog"},
		{ID: "dep-y", Status: "backlog"},
	}}
	ps := ReadyBlockedProblems(idx, map[string]bool{"ready": true}, map[string]bool{"dep-done": true})
	if len(ps) != 1 {
		t.Fatalf("got %d problem(s), want 1: %+v", len(ps), ps)
	}
	msg := ps[0].Msg
	for _, want := range []string{"dep-x", "dep-y"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should name %s", msg, want)
		}
	}
	if strings.Contains(msg, "dep-done") {
		t.Errorf("message %q must not name the satisfied dep", msg)
	}
}
