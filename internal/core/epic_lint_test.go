package core

import (
	"testing"
	"time"
)

// Two active boxes naming one repo in two spellings are one repo twice:
// the slot arithmetic folds case, as every repo match does, and the problem
// names the repo in the smallest spelling the boxes carry, not the fold's.
// One box listing the same repo in two spellings holds ONE slot.
func TestActiveEpicProblemsFoldRepoCase(t *testing.T) {
	epics := []Epic{
		{ID: "e-upper", Title: "u", Active: true, Repos: []string{"O/R"}},
		{ID: "e-lower", Title: "l", Active: true, Repos: []string{"o/r"}},
	}
	ps := activeEpicProblems(&Index{}, epics, map[string]bool{})
	found := false
	for _, p := range ps {
		if p.Code == "epic-multi-active" && p.ID == "O/R" {
			found = true
		}
	}
	if !found {
		t.Errorf("want epic-multi-active with id O/R, got %+v", ps)
	}

	one := []Epic{{ID: "e-both", Title: "b", Active: true, Repos: []string{"O/R", "o/r"}}}
	for _, p := range activeEpicProblems(&Index{}, one, map[string]bool{}) {
		if p.Code == "epic-multi-active" {
			t.Errorf("one box naming O/R and o/r: got %+v", p)
		}
	}
}

// The idle-repo direction folds the same way: an active box spelled
// `Foo-Bar/baz` covers open work spelled `Foo-Bar/baz` (the slot map is keyed
// folded since #489, and the lookup compared the task's raw spelling against
// it — t-5h94), and open work in two spellings of one repo with no box is ONE
// idle repo, reported in the spelling its tasks carry.
func TestActiveEpicProblemsIdleRepoFoldsCase(t *testing.T) {
	epics := []Epic{{ID: "e-box", Title: "box", Active: true, Repos: []string{"Foo-Bar/baz"}}}
	idx := &Index{Tasks: []Task{{ID: "t-1", Status: "ready", Repos: []string{"Foo-Bar/baz"}}}}
	for _, p := range activeEpicProblems(idx, epics, map[string]bool{}) {
		if p.Code == "epic-no-active" {
			t.Errorf("active box Foo-Bar/baz, open task Foo-Bar/baz: got %+v", p)
		}
	}

	idx = &Index{Tasks: []Task{
		{ID: "t-1", Status: "ready", Repos: []string{"foo-bar/baz"}},
		{ID: "t-2", Status: "ready", Repos: []string{"Foo-Bar/baz"}},
	}}
	closed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	epics = []Epic{{ID: "e-closed", Title: "closed", Active: true, Closed: &closed, Repos: []string{"Foo-Bar/baz"}}}
	var got []Problem
	for _, p := range activeEpicProblems(idx, epics, map[string]bool{}) {
		if p.Code == "epic-no-active" {
			got = append(got, p)
		}
	}
	if len(got) != 1 || got[0].ID != "Foo-Bar/baz" {
		t.Errorf("two spellings of one idle repo: want one epic-no-active with id Foo-Bar/baz, got %+v", got)
	}
}
