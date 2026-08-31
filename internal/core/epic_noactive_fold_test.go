package core

import (
	"strings"
	"testing"
)

// epic-no-active folds: one idle repo keeps the targeted per-repo line, several
// collapse into ONE problem naming them all (22 one-per-repo lines measured on
// a dormant-heavy board — a wall the reader scrolls past). Same code either
// way, so the filters are unaffected.
func TestEpicNoActiveFoldsManyRepos(t *testing.T) {
	terminal := map[string]bool{"done": true, "icebox": true}
	epics := []Epic{{ID: "e-1", Title: "box", Repos: []string{"me/x"}}}

	noActive := func(ps []Problem) []Problem {
		var out []Problem
		for _, p := range ps {
			if p.Code == "epic-no-active" {
				out = append(out, p)
			}
		}
		return out
	}

	idx := &Index{Tasks: []Task{
		{ID: "t-1", Title: "a", Status: "inbox", Repos: []string{"me/a"}, Epic: "e-1"},
	}}
	got := noActive(EpicProblems(idx, epics, terminal, nil))
	if len(got) != 1 || got[0].ID != "me/a" || !strings.Contains(got[0].Msg, "furrow epic ls -r me/a") {
		t.Fatalf("single idle repo should keep the per-repo line, got %+v", got)
	}

	idx = &Index{Tasks: []Task{
		{ID: "t-1", Title: "a", Status: "inbox", Repos: []string{"me/a"}, Epic: "e-1"},
		{ID: "t-2", Title: "b", Status: "inbox", Repos: []string{"me/b"}, Epic: "e-1"},
		{ID: "t-3", Title: "c", Status: "inbox", Repos: []string{"me/c"}, Epic: "e-1"},
		{ID: "t-4", Title: "done elsewhere", Status: "done", Repos: []string{"me/quiet"}, Epic: "e-1"},
	}}
	got = noActive(EpicProblems(idx, epics, terminal, nil))
	if len(got) != 1 {
		t.Fatalf("several idle repos should fold to ONE problem, got %+v", got)
	}
	p := got[0]
	if p.ID != "board" || p.Severity != SevWarn {
		t.Errorf("folded problem should carry id 'board' and warn, got %+v", p)
	}
	for _, want := range []string{"3 repos", "me/a", "me/b", "me/c"} {
		if !strings.Contains(p.Msg, want) {
			t.Errorf("folded message should contain %q, got %q", want, p.Msg)
		}
	}
	if strings.Contains(p.Msg, "me/quiet") {
		t.Errorf("terminal-only repo must not be named, got %q", p.Msg)
	}

	// A repo with an ACTIVE epic is not idle: only the uncovered ones fold.
	epics = []Epic{
		{ID: "e-1", Title: "box", Repos: []string{"me/a"}, Active: true},
	}
	got = noActive(EpicProblems(idx, epics, terminal, nil))
	if len(got) != 1 || got[0].ID != "board" || strings.Contains(got[0].Msg, "me/a,") {
		t.Fatalf("covered repo must drop out of the fold, got %+v", got)
	}
	if !strings.Contains(got[0].Msg, "2 repos") {
		t.Errorf("fold should count 2 remaining repos, got %q", got[0].Msg)
	}
}
