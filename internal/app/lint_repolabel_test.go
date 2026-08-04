package app

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// A label that names a repo — full owner/repo or a short name the board's repo
// universe resolves (case-insensitively) — warns repo-as-label. The read-side
// -l did-you-mean guard never second-guesses an existing label, so this warn
// is where the "no repo-named labels" invariant lives.
func TestLintRepoAsLabel(t *testing.T) {
	a := newApp()
	if _, err := a.Add("carrier", AddOpts{Repos: []string{"me/demo"}}); err != nil {
		t.Fatal(err)
	}
	short, err := a.Add("short-name label", AddOpts{Labels: []string{"demo"}})
	if err != nil {
		t.Fatal(err)
	}
	full, _ := a.Add("full-name label", AddOpts{Labels: []string{"me/demo"}})
	folded, _ := a.Add("case-folded label", AddOpts{Labels: []string{"Demo"}})
	if _, err := a.Add("innocent tag", AddOpts{Labels: []string{"bug"}}); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range ps {
		if p.Code == "repo-as-label" {
			if p.Severity != core.SevWarn {
				t.Errorf("repo-as-label severity = %s, want warn", p.Severity)
			}
			got[p.ID] = p.Msg
		}
	}
	for _, id := range []string{short.ID, full.ID, folded.ID} {
		if _, ok := got[id]; !ok {
			t.Errorf("task %s should be flagged repo-as-label; got %v", id, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("want exactly 3 flagged tasks, got %v", got)
	}
	if !strings.Contains(got[short.ID], "furrow repo "+short.ID+" --add demo") {
		t.Errorf("message should name the fix, got %q", got[short.ID])
	}
}

// The check must consult BoardRepos too: a board-scoped repo with no task yet
// is still a repo name, not a free tag.
func TestLintRepoAsLabelUsesBoardRepos(t *testing.T) {
	a := newApp()
	a.BoardRepos = []string{"me/tool"}
	flagged, err := a.Add("tagged with board repo", AddOpts{Labels: []string{"tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Code == "repo-as-label" && p.ID == flagged.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("board-derived repo name should be flagged, problems: %+v", ps)
	}
}
