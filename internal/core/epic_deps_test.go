package core

import (
	"strings"
	"testing"
	"time"
)

func depTestEpic(id string, deps ...string) Epic {
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	return Epic{ID: id, Title: "box " + id, Repos: []string{"o/r"},
		Created: now, Updated: now, Body: "bodies/" + id + ".md", Deps: deps}
}

func closedDepTestEpic(id string, deps ...string) Epic {
	e := depTestEpic(id, deps...)
	c := e.Created
	e.Closed = &c
	return e
}

func TestEpicDependsOn(t *testing.T) {
	epics := []Epic{
		depTestEpic("e-a", "e-b"),
		depTestEpic("e-b", "e-c"),
		depTestEpic("e-c"),
		depTestEpic("e-x", "e-gone"), // dangling edge: contributes nothing
	}
	cases := []struct {
		from, to string
		want     bool
	}{
		{"e-a", "e-b", true},  // direct
		{"e-a", "e-c", true},  // transitive
		{"e-c", "e-a", false}, // wrong direction
		{"e-x", "e-c", false}, // only edge dangles
		{"e-a", "e-a", true},  // reflexive by definition of the walk (from == to)
	}
	for _, c := range cases {
		if got := EpicDependsOn(epics, c.from, c.to); got != c.want {
			t.Errorf("EpicDependsOn(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// Driven through EpicProblems rather than the graph walk directly, so the lint
// wiring is covered too and not just the rule.
func TestEpicDepProblems(t *testing.T) {
	idx := &Index{}
	terminal := map[string]bool{"done": true}

	t.Run("dangling dep is an error", func(t *testing.T) {
		epics := []Epic{depTestEpic("e-a", "e-gone")}
		ps := EpicProblems(idx, epics, terminal, nil)
		if !hasProblem(ps, "epic-dep-missing", "e-a") {
			t.Errorf("want epic-dep-missing for e-a, got %v", ps)
		}
	})

	t.Run("a merged-in cycle is an error naming the knot", func(t *testing.T) {
		epics := []Epic{depTestEpic("e-a", "e-b"), depTestEpic("e-b", "e-a")}
		ps := EpicProblems(idx, epics, terminal, nil)
		found := false
		for _, p := range ps {
			if p.Code == "epic-dep-cycle" {
				found = true
				if !strings.Contains(p.Msg, "e-a") || !strings.Contains(p.Msg, "e-b") {
					t.Errorf("the cycle message must name every box in it: %q", p.Msg)
				}
			}
		}
		if !found {
			t.Errorf("want epic-dep-cycle, got %v", ps)
		}
	})

	t.Run("an active epic waiting on an open box warns", func(t *testing.T) {
		waiting := depTestEpic("e-a", "e-open", "e-done")
		waiting.Active = true
		epics := []Epic{waiting, depTestEpic("e-open"), closedDepTestEpic("e-done")}
		ps := EpicProblems(idx, epics, terminal, nil)
		var warn *Problem
		for i := range ps {
			if ps[i].Code == "epic-dep-open" {
				warn = &ps[i]
			}
		}
		if warn == nil {
			t.Fatalf("want epic-dep-open for the active waiting box, got %v", ps)
		}
		if warn.Severity != SevWarn || warn.ID != "e-a" {
			t.Errorf("epic-dep-open must be a warn on the active epic, got %+v", warn)
		}
		if !strings.Contains(warn.Msg, "e-open") || strings.Contains(warn.Msg, "e-done") {
			t.Errorf("the warn must name the OPEN dep only (a closed dep is satisfied): %q", warn.Msg)
		}
	})

	t.Run("a parked epic waiting on an open box says nothing", func(t *testing.T) {
		epics := []Epic{depTestEpic("e-a", "e-open"), depTestEpic("e-open")}
		for _, p := range EpicProblems(idx, epics, terminal, nil) {
			if p.Code == "epic-dep-open" {
				t.Errorf("epic-dep-open must fire only for the ACTIVE epic: %+v", p)
			}
		}
	})
}

func hasProblem(ps []Problem, code, id string) bool {
	for _, p := range ps {
		if p.Code == code && p.ID == id {
			return true
		}
	}
	return false
}
