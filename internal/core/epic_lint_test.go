package core

import "testing"

// Two active boxes naming one repo in two spellings are one repo twice:
// the slot arithmetic folds case, as every repo match does.
func TestActiveEpicProblemsFoldRepoCase(t *testing.T) {
	epics := []Epic{
		{ID: "e-upper", Title: "u", Active: true, Repos: []string{"O/R"}},
		{ID: "e-lower", Title: "l", Active: true, Repos: []string{"o/r"}},
	}
	ps := activeEpicProblems(&Index{}, epics, map[string]bool{})
	found := false
	for _, p := range ps {
		if p.Code == "epic-multi-active" && p.ID == "o/r" {
			found = true
		}
	}
	if !found {
		t.Errorf("want epic-multi-active for o/r, got %+v", ps)
	}
}
