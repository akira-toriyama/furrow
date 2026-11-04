package core

import "testing"

func successorTestEpic(id, successor string) Epic {
	e := depTestEpic(id)
	e.Successor = successor
	return e
}

func closedSuccessorTestEpic(id, successor string) Epic {
	e := closedDepTestEpic(id)
	e.Successor = successor
	return e
}

// The chain walk behind the write path's loop refusal: following successor
// pointers from `from` reaches `to` or ends — at an unknown id, an empty
// pointer, or a box already visited (a cycle a merge landed must not hang).
func TestEpicSuccessorReaches(t *testing.T) {
	chain := []Epic{successorTestEpic("e-a", "e-b"), successorTestEpic("e-b", "e-c"), successorTestEpic("e-c", "")}
	if !EpicSuccessorReaches(chain, "e-a", "e-c") {
		t.Error("a→b→c must reach c from a")
	}
	if EpicSuccessorReaches(chain, "e-c", "e-a") {
		t.Error("the chain runs one way: c reaches nothing")
	}
	if EpicSuccessorReaches(chain, "e-a", "e-zz") {
		t.Error("an id outside the chain is not reached")
	}
	loop := []Epic{successorTestEpic("e-a", "e-b"), successorTestEpic("e-b", "e-a")}
	if EpicSuccessorReaches(loop, "e-a", "e-zz") {
		t.Error("a cycle on disk must end the walk, not hang it")
	}
	dangling := []Epic{successorTestEpic("e-a", "e-gone")}
	if EpicSuccessorReaches(dangling, "e-a", "e-b") {
		t.Error("a dangling pointer ends the walk")
	}
}

// lint's view of a declaration: an OPEN declarer naming a missing box is an
// error, naming a closed one a warning; a closed declarer is history and
// says nothing either way.
func TestEpicSuccessorProblems(t *testing.T) {
	idx := &Index{}
	terminal := map[string]bool{"done": true}

	t.Run("a missing successor is an error on the declarer", func(t *testing.T) {
		ps := EpicProblems(idx, []Epic{successorTestEpic("e-a", "e-gone")}, terminal, nil)
		if !hasProblem(ps, "epic-successor-missing", "e-a") {
			t.Errorf("want epic-successor-missing for e-a, got %v", ps)
		}
	})
	t.Run("a closed successor warns on the open declarer", func(t *testing.T) {
		ps := EpicProblems(idx, []Epic{successorTestEpic("e-a", "e-b"), closedSuccessorTestEpic("e-b", "")}, terminal, nil)
		if !hasProblem(ps, "epic-successor-closed", "e-a") {
			t.Errorf("want epic-successor-closed for e-a, got %v", ps)
		}
		for _, p := range ps {
			if p.Code == "epic-successor-closed" && p.Severity != SevWarn {
				t.Errorf("epic-successor-closed must be a warning, got %s", p.Severity)
			}
		}
	})
	t.Run("a closed declarer is history", func(t *testing.T) {
		ps := EpicProblems(idx, []Epic{closedSuccessorTestEpic("e-a", "e-gone"), closedSuccessorTestEpic("e-b", "e-c"), closedSuccessorTestEpic("e-c", "")}, terminal, nil)
		for _, p := range ps {
			if p.Code == "epic-successor-missing" || p.Code == "epic-successor-closed" {
				t.Errorf("a closed declarer must raise nothing, got %v", p)
			}
		}
	})
	t.Run("waiting on the declared successor is an error", func(t *testing.T) {
		a := successorTestEpic("e-a", "e-b")
		a.Deps = []string{"e-mid"}
		ps := EpicProblems(idx, []Epic{a, depTestEpic("e-mid", "e-b"), depTestEpic("e-b")}, terminal, nil)
		if !hasProblem(ps, "epic-successor-dep", "e-a") {
			t.Errorf("want epic-successor-dep for e-a (it waits on e-b through e-mid), got %v", ps)
		}
	})
	t.Run("a standing declarer warns", func(t *testing.T) {
		a := successorTestEpic("e-a", "e-b")
		a.Standing = true
		ps := EpicProblems(idx, []Epic{a, depTestEpic("e-b")}, terminal, nil)
		if !hasProblem(ps, "epic-successor-standing", "e-a") {
			t.Errorf("want epic-successor-standing for e-a, got %v", ps)
		}
	})
	t.Run("an open chain is clean", func(t *testing.T) {
		ps := EpicProblems(idx, []Epic{successorTestEpic("e-a", "e-b"), successorTestEpic("e-b", "")}, terminal, nil)
		for _, p := range ps {
			if p.Code == "epic-successor-missing" || p.Code == "epic-successor-closed" {
				t.Errorf("unexpected %v", p)
			}
		}
	})
}
