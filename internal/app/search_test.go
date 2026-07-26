package app

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestSearchMatchesTitleAndBody(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("adopt teatest harness", AddOpts{})                                    // title match
	t2, _ := a.Add("unrelated title", AddOpts{Body: "we should use teatest for the TUI"}) // body match
	a.Add("nothing to see", AddOpts{Body: "plain prose"})                                 // no match

	hits, err := a.Search(QueryOpts{}, "teatest")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	byID := map[string]SearchHit{}
	for _, h := range hits {
		byID[h.Task.ID] = h
	}
	if h := byID[t1.ID]; h.MatchedField != "title" || h.Snippet != "adopt teatest harness" {
		t.Errorf("t1 should be a title hit with the full-title snippet, got %+v", h)
	}
	if h := byID[t2.ID]; h.MatchedField != "body" || !strings.Contains(h.Snippet, "teatest") {
		t.Errorf("t2 should be a body hit whose snippet holds the term, got %+v", h)
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	a := newApp()
	a.Add("Adopt TeaTest", AddOpts{})
	hits, err := a.Search(QueryOpts{}, "TEATEST")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("case-insensitive search should find 1, got %d", len(hits))
	}
}

func TestSearchTitleTakesPrecedenceOverBody(t *testing.T) {
	a := newApp()
	// term in BOTH title and body -> a single hit, reported as a title match
	t1, _ := a.Add("sync fixes", AddOpts{Body: "the sync command is also mentioned here"})
	hits, err := a.Search(QueryOpts{}, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.ID != t1.ID || hits[0].MatchedField != "title" {
		t.Fatalf("a title+body match should be one title hit, got %+v", hits)
	}
}

func TestSearchScopeByStatusAndLabel(t *testing.T) {
	a := newApp()
	a.Add("sync one", AddOpts{Labels: []string{"cli"}}) // inbox, cli
	a.Add("sync two", AddOpts{Status: "in-progress"})   // in-progress, no label

	hits, err := a.Search(QueryOpts{Status: "in-progress"}, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.Status != "in-progress" {
		t.Fatalf("status filter should narrow to the 1 in-progress task, got %+v", hits)
	}

	hits, err = a.Search(QueryOpts{Label: "cli"}, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !contains(hits[0].Task.Labels, "cli") {
		t.Fatalf("label filter should narrow to the cli task, got %+v", hits)
	}
}

func TestSearchLimit(t *testing.T) {
	a := newApp()
	a.Add("sync a", AddOpts{})
	a.Add("sync b", AddOpts{})
	a.Add("sync c", AddOpts{})
	hits, err := a.Search(QueryOpts{Limit: 2}, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("limit 2 should cap the result at 2, got %d", len(hits))
	}
}

func TestSearchEmptyTermIsValidationError(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	if _, err := a.Search(QueryOpts{}, "   "); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("a blank term should be a validation error (exit 2), got %v", err)
	}
}

func TestSearchUnknownLaneFilterFailsFast(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	if _, err := a.Search(QueryOpts{Status: "ghost"}, "any"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("an unknown -s lane should fail fast (exit 2), got %v", err)
	}
}

func TestSearchZeroMatchIsCleanEmpty(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	hits, err := a.Search(QueryOpts{}, "nomatchxyz")
	if err != nil {
		t.Fatalf("a zero-match search must not error (exit 0), got %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("want 0 hits, got %d", len(hits))
	}
}

// `search --archived` reads the archive store INSTEAD of the hot board, the
// same meaning --archived has on ls/show. The load-bearing case is a BODY-only
// hit: switching the index alone (and leaving the hot store's LoadBody) would
// match archived titles and silently miss — or error on — every body match, and
// a title-only test would look green through the whole bug.
func TestSearchArchivedReadsTheArchiveBodies(t *testing.T) {
	a := newFSApp(t)

	hot, err := a.Add("stays hot with zulu in the title", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	old, err := a.Add("will be archived", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddNote(old.ID, "the zulu paragraph lives only in the body"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(old.ID); err != nil {
		t.Fatal(err)
	}
	// Sixty days later, sweep everything closed more than thirty days ago.
	a.Clock = &fixedClock{t: a.Clock.Now().AddDate(0, 0, 60)}
	moved, err := a.Archive(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 {
		t.Fatalf("precondition: one task should have been archived, got %d", len(moved))
	}

	hits, err := a.Search(QueryOpts{}, "zulu")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.ID != hot.ID {
		t.Fatalf("the hot search must see only the hot task, got %+v", hits)
	}

	hits, err = a.Search(QueryOpts{Archived: true}, "zulu")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.ID != old.ID {
		t.Fatalf("--archived must search the archive INSTEAD of the hot board, got %+v", hits)
	}
	if hits[0].MatchedField != "body" {
		t.Errorf("matched_field = %q, want body — the archive's own body must be read", hits[0].MatchedField)
	}
	if hits[0].Snippet == "" {
		t.Error("a body hit must carry a snippet cut from the ARCHIVED body")
	}
}

// A board that never archived anything reads as a clean empty result, not an
// error — the same contract `ls --archived` has.
func TestSearchArchivedEmptyStoreIsHealthy(t *testing.T) {
	a := newFSApp(t)
	if _, err := a.Add("hot only", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	hits, err := a.Search(QueryOpts{Archived: true}, "hot")
	if err != nil {
		t.Fatalf("searching an empty archive must be healthy, got %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("want no hits, got %+v", hits)
	}
}

// newFSApp opens a real file-backed board in a temp dir — the archive store is
// a sibling DIRECTORY, so memstore cannot exercise these paths.
func newFSApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	return openBoard(t, dir)
}
