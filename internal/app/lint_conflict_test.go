package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// conflictFindings returns the conflict-marker problems keyed by the body id each
// one blames — the field a --json consumer branches on.
func conflictFindings(t *testing.T, a *App) map[string]core.Problem {
	t.Helper()
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]core.Problem{}
	for _, p := range ps {
		if p.Code == "conflict-marker" {
			found[p.ID] = p
		}
	}
	return found
}

// A body carrying git's markers is a HALF-MERGED progress record — furrow keeps
// "what's done / what's next" there, so half of it is missing, and it is usually
// the half someone just wrote. That is broken data on the board, not a smell:
// error severity, and lint fails.
func TestLintFlagsConflictMarkerBodyAsError(t *testing.T) {
	a := newApp()
	task, err := a.Add("half-merged body", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	body := "# half-merged body\n\n<<<<<<< Updated upstream\n- [x] shipped\n=======\n- [ ] still writing this\n>>>>>>> Stashed changes\n"
	if err := a.Store.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}

	found := conflictFindings(t, a)
	p, ok := found[task.ID]
	if !ok {
		t.Fatalf("lint must flag the marker-carrying body of %s, got %v", task.ID, found)
	}
	if p.Severity != core.SevError {
		t.Errorf("conflict-marker must be an ERROR (broken data on the board), got %q", p.Severity)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if !core.HasErrors(ps) {
		t.Error("a marker-carrying body must fail lint (exit 2), or nobody finds out — which is the whole defect")
	}
}

// The counterpart: a body that DOCUMENTS what a conflict looks like — markers
// quoted inline or indented, and a bare `=======` even inside a fence, where it
// is a setext underline — is not itself corrupt. A fence does NOT shelter
// `<<<<<<<`/`>>>>>>>`: a conflict is exactly what splits a fence, and the
// all-markers skip went silent on the real thing (t-q5fk).
func TestLintDoesNotFlagDocumentedMarkerExample(t *testing.T) {
	a := newApp()
	task, err := a.Add("notes about conflicts", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	body := "# notes\n\ngit writes `<<<<<<< Updated upstream`, then:\n\n```\n    <<<<<<< ours\nours\n=======\ntheirs\n    >>>>>>> theirs\n```\n\nthat is all.\n"
	if err := a.Store.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}
	if found := conflictFindings(t, a); len(found) != 0 {
		t.Errorf("a documented example is documentation, not corruption: %v", found)
	}

	fenced := "# notes\n\n```\n<<<<<<< ours\nmine\n=======\ntheirs\n>>>>>>> theirs\n```\n"
	if err := a.Store.SaveBody(task.ID, fenced); err != nil {
		t.Fatal(err)
	}
	if found := conflictFindings(t, a); len(found) != 1 {
		t.Errorf("column-0 <<<<<<< / >>>>>>> are a conflict wherever they stand, got %v", found)
	}
}
