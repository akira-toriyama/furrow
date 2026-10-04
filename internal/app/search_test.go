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

	hits, err := a.Search(QueryOpts{}, "teatest", false)
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
	hits, err := a.Search(QueryOpts{}, "TEATEST", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("case-insensitive search should find 1, got %d", len(hits))
	}
}

func TestSearchTitleTakesPrecedenceOverBody(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("sync fixes", AddOpts{Body: "the sync command is also mentioned here"})
	hits, err := a.Search(QueryOpts{}, "sync", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.ID != t1.ID || hits[0].MatchedField != "title" {
		t.Fatalf("a title-and-body match should be one title hit, got %+v", hits)
	}
}

func TestSearchScopeByStatusAndLabel(t *testing.T) {
	a := newApp()
	a.Add("sync one", AddOpts{Labels: []string{"cli"}}) // inbox, cli
	a.Add("sync two", AddOpts{Status: "in-progress"})   // in-progress, no label

	hits, err := a.Search(QueryOpts{Status: "in-progress"}, "sync", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.Status != "in-progress" {
		t.Fatalf("status filter should narrow to the 1 in-progress task, got %+v", hits)
	}

	hits, err = a.Search(QueryOpts{Label: "cli"}, "sync", false)
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
	hits, err := a.Search(QueryOpts{Limit: 2}, "sync", false)
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
	if _, err := a.Search(QueryOpts{}, "   ", false); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("a blank term should be a validation error (exit 2), got %v", err)
	}
}

func TestSearchUnknownLaneFilterFailsFast(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	if _, err := a.Search(QueryOpts{Status: "ghost"}, "any", false); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("an unknown -s lane should fail fast (exit 2), got %v", err)
	}
}

func TestSearchZeroMatchIsCleanEmpty(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	hits, err := a.Search(QueryOpts{}, "nomatchxyz", false)
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
	a.Clock = &fixedClock{t: a.Clock.Now().AddDate(0, 0, 60)}
	rep, err := a.Archive(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tasks) != 1 {
		t.Fatalf("precondition: one task should have been archived, got %d", len(rep.Tasks))
	}

	hits, err := a.Search(QueryOpts{}, "zulu", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Task.ID != hot.ID {
		t.Fatalf("the hot search must see only the hot task, got %+v", hits)
	}

	hits, err = a.Search(QueryOpts{Archived: true}, "zulu", false)
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
	hits, err := a.Search(QueryOpts{Archived: true}, "hot", false)
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

// The text a reschedule or a candidate drop has to find lives in checklist rows
// and refs too (furrow-test drills: a `docs/<event>/2026-11-21/…` ref and a
// checklist-only 「下見枠」 were invisible to search and pushed the session to
// grep the shards). Search and -q free text walk the same four fields, title
// first and body last, and a shard-field hit never reads the body.
func TestSearchReachesChecklistAndRefs(t *testing.T) {
	a := newApp()
	inList := mustAdd(t, a, "venue walkthrough", AddOpts{Checklist: UncheckedItems([]string{"book the slot", "下見枠 10/10 を確保"})})
	inRef := mustAdd(t, a, "print the menu", AddOpts{Refs: []string{"docs/oneday-restaurant/2026-11-21/menu.md"}})
	mustAdd(t, a, "unrelated", AddOpts{Body: "plain prose"})
	cs := &countingStore{Store: a.Store}
	a.Store = cs

	for _, c := range []struct {
		term, id, field, snippet string
	}{
		{"下見枠", inList.ID, core.FieldChecklist, "下見枠 10/10 を確保"},
		{"oneday-restaurant", inRef.ID, core.FieldRefs, "docs/oneday-restaurant/2026-11-21/menu.md"},
	} {
		cs.loads = 0
		hits, err := a.Search(QueryOpts{}, c.term, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Task.ID != c.id || hits[0].MatchedField != c.field || hits[0].Snippet != c.snippet {
			t.Fatalf("search %q: want one %s hit on %s with the whole %s as snippet, got %+v", c.term, c.field, c.id, c.field, hits)
		}
		// The two misses paid one body read each; the shard hit paid none.
		if cs.loads != 2 {
			t.Errorf("search %q: %d body loads, want 2 (a %s hit must not read its body)", c.term, cs.loads, c.field)
		}
		ls, err := a.List(QueryOpts{Query: c.term})
		if err != nil {
			t.Fatal(err)
		}
		if len(ls) != 1 || ls[0].ID != c.id {
			t.Errorf("-q %q must find what search finds (%s), got %v", c.term, c.id, ls)
		}
	}
}

// One task, the term in every field: the reported field follows the walk order
// title → checklist → refs → body, so the report is stable across boards.
func TestSearchFieldOrder(t *testing.T) {
	cases := []struct {
		opts AddOpts
		want string
	}{
		{AddOpts{Checklist: UncheckedItems([]string{"kiwi row"}), Refs: []string{"kiwi.md"}, Body: "kiwi body"}, core.FieldChecklist},
		{AddOpts{Refs: []string{"kiwi.md"}, Body: "kiwi body"}, core.FieldRefs},
		{AddOpts{Body: "kiwi body"}, core.FieldBody},
	}
	for _, c := range cases {
		a := newApp()
		mustAdd(t, a, "fruit", c.opts)
		hits, err := a.Search(QueryOpts{}, "kiwi", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].MatchedField != c.want {
			t.Errorf("%+v: matched_field = %+v, want %s", c.opts, hits, c.want)
		}
	}
}

// --regex is the escape from substring noise the drills hit eight runs in a
// row: a one-letter candidate name ("B") matched every BGM and every b. RE2's
// \b is ASCII, so B beside kana or a space is at a boundary and B inside BGM
// is not; case still folds unless the pattern clears it with (?-i).
func TestSearchRegex(t *testing.T) {
	a := newApp()
	cand := mustAdd(t, a, "候補 B の見積を取り下げる", AddOpts{})
	company := mustAdd(t, a, "見積の比較", AddOpts{Body: "A/B/C のうち B社 だけが未回答"})
	mustAdd(t, a, "BGM のプレイリスト", AddOpts{})
	lower := mustAdd(t, a, "plan b if it rains", AddOpts{})

	ids := func(hits []SearchHit) []string {
		var out []string
		for _, h := range hits {
			out = append(out, h.Task.ID)
		}
		return out
	}
	hits, err := a.Search(QueryOpts{}, `\bB\b`, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(hits), ","); got != strings.Join([]string{cand.ID, company.ID, lower.ID}, ",") {
		t.Errorf(`\bB\b (case-folded) = %s, want the candidate, B社 and "plan b" — never BGM`, got)
	}
	if hits, err = a.Search(QueryOpts{}, `(?-i)\bB\b`, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(hits), ","); got != cand.ID+","+company.ID {
		t.Errorf(`(?-i)\bB\b = %s, want only the capital B tasks`, got)
	}
	// The body snippet is cut around the regex match, not the pattern text.
	for _, h := range hits {
		if h.Task.ID == company.ID && (h.MatchedField != core.FieldBody || !strings.Contains(h.Snippet, "B社")) {
			t.Errorf("a regex body hit carries an excerpt around the match, got %+v", h)
		}
	}
}

func TestSearchRegexRefusals(t *testing.T) {
	a := newApp()
	a.Add("anything", AddOpts{})
	for _, p := range []string{`(unclosed`, `x*`, `^`, `(?i)`} {
		_, err := a.Search(QueryOpts{}, p, true)
		if e := core.AsError(err); e == nil || e.Code != core.CodeValidation {
			t.Errorf("--regex %q: want a validation error (exit 2), got %v", p, err)
		}
	}
	// Without --regex the same text is a literal substring, never a pattern.
	if hits, err := a.Search(QueryOpts{}, `x*`, false); err != nil || len(hits) != 0 {
		t.Errorf("a substring search for %q must be a clean literal miss, got %v %v", `x*`, hits, err)
	}
}
