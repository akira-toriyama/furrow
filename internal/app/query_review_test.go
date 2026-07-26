package app

import (
	"slices"
	"testing"
	"time"
)

// Comma is OR on EVERY qualifier — the rule README and the glossary state
// unconditionally. The ordinals parsed only the FIRST value, so `value:2,4`
// silently answered with the value-2 tasks alone at exit 0: a filter quietly
// returning a subset, which is the failure shape the read contract exists to
// prevent. Labels/ids/lanes/dates always looped; these did not.
func TestQueryOrdinalCommaIsOR(t *testing.T) {
	a := newApp()
	mustAdd := func(title string, value, effort int) {
		t.Helper()
		if _, err := a.Add(title, AddOpts{Value: &value, Effort: &effort}); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("v2", 2, 1)
	mustAdd("v4", 4, 2)
	mustAdd("v5", 5, 3)

	for q, want := range map[string][]string{
		"value:2,4":    {"v2", "v4"},
		"value:4,5":    {"v4", "v5"},
		"effort:1,2":   {"v2", "v4"},
		"value:2":      {"v2"},
		"-value:2,4":   {"v5"},
		"priority:100": {"v2"},
	} {
		if got := qTitles(t, a, q); !slices.Equal(got, want) {
			t.Errorf("-q %q = %v, want %v", q, got, want)
		}
	}
}

// `is:stuck` means "a CONTAINER with open work but nothing actionable under it"
// — README and the spec both say so. Ungated, it also selected plain tasks,
// whose own row in the SAME --json payload reported container:false, stuck:false:
// one command, one payload, two contradictory answers.
func TestQueryIsStuckOnlyMatchesContainers(t *testing.T) {
	a := newApp()
	epic, err := a.Add("an epic", AddOpts{Type: "epic"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.Add("plain parent", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Each parent gets an open child that is NOT actionable (inbox is not a next
	// lane), which is exactly the "stuck" shape.
	for _, parent := range []string{epic.ID, plain.ID} {
		if _, err := a.Add("child of "+parent, AddOpts{Parent: parent, Status: "inbox"}); err != nil {
			t.Fatal(err)
		}
	}

	got := qTitles(t, a, "is:stuck")
	if !slices.Equal(got, []string{"an epic"}) {
		t.Errorf("is:stuck = %v, want only the container [an epic]", got)
	}
}

// A misspelled qualifier that happens to carry an OPERATOR must still be
// diagnosed as an unknown field. The ordered-field gate ran first, so
// `updatd:>=-2w` was answered with query-type and NO candidates, in a message
// that asserts the field exists — and dates are documented only with >= and ..
// idioms, so that is the likeliest place to typo one.
func TestQueryUnknownFieldWithOperator(t *testing.T) {
	a := newApp()
	for _, q := range []string{"updatd:>=-2w", "valu:>3", "creatd:2026-01-01..2026-02-01", "zzz:1"} {
		e := qErr(t, a, q)
		if e.ID != "query-unknown-field" {
			t.Errorf("-q %q: id = %q, want query-unknown-field", q, e.ID)
		}
		if len(e.Candidates) == 0 {
			t.Errorf("-q %q: an unknown qualifier must carry candidates", q)
		}
	}
}

// `-q repo:` resolves through the SAME strict path `-r` uses, so the two cannot
// disagree: an ambiguous short name is exit 2 with candidates (it used to union
// across BOTH owners at exit 0 — a scoped read quietly crossing a repo
// boundary), and casing folds (`repo:Tool` used to answer [] where `-r Tool`
// resolved).
func TestQueryRepoResolvesLikeDashR(t *testing.T) {
	a := newApp()
	if _, err := a.Add("alice work", AddOpts{Repos: []string{"alice/tool"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("bob work", AddOpts{Repos: []string{"bob/tool"}}); err != nil {
		t.Fatal(err)
	}

	e := qErr(t, a, "repo:tool")
	if len(e.Candidates) != 2 {
		t.Errorf("an ambiguous short name must exit 2 with both candidates, got %+v", e)
	}
	if got := qTitles(t, a, "repo:alice/TOOL"); !slices.Equal(got, []string{"alice work"}) {
		t.Errorf("repo: must fold case like -r, got %v", got)
	}
	if got := qTitles(t, a, "repo:alice/tool"); !slices.Equal(got, []string{"alice work"}) {
		t.Errorf("a full owner/repo must match exactly one repo, got %v", got)
	}
	// An unresolvable name is exit 2, not a silent empty result.
	if e := qErr(t, a, "repo:nosuch"); e.Code == 0 {
		t.Error("an unresolvable repo must be an error, not []")
	}
}

// A relative offset of ~292 years or more overflows int64 nanoseconds and wraps
// NEGATIVE, which the sign flip then turns into +292 years — inverting the
// comparison at exit 0. It must be refused, landing on the ordinary query-type
// error instead.
func TestQueryRelativeOffsetOverflowIsRefused(t *testing.T) {
	a := newApp()
	if _, err := a.Add("recent", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	// Just under the ceiling still works and matches everything.
	if got := qTitles(t, a, "updated:>=-106751d"); !slices.Equal(got, []string{"recent"}) {
		t.Errorf("a large but representable offset should still match: %v", got)
	}
	for _, q := range []string{"updated:>=-106752d", "updated:>=-15251w", "updated:<-1000000d"} {
		if e := qErr(t, a, q); e.ID != "query-type" {
			t.Errorf("-q %q: id = %q, want query-type (never a silently inverted result)", q, e.ID)
		}
	}
}

// The whole-day interval a bare YYYY-MM-DD binds to is only pinned at its
// edges. Every other date fixture sits at 12:00, half a day from any bound, so
// shrinking the interval to 13h left the suite green — the whole afternoon was
// untested, not merely the last second.
func TestQueryBareDateCoversTheWholeDay(t *testing.T) {
	a, clk := revisitApp()
	day := clk.t.UTC().Truncate(24 * time.Hour)
	for _, at := range []time.Duration{0, 13 * time.Hour, 24*time.Hour - time.Second} {
		clk.t = day.Add(at)
		if _, err := a.Add(at.String(), AddOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	// One instant past the day, which must fall OUTSIDE it.
	clk.t = day.Add(24 * time.Hour)
	if _, err := a.Add("next day", AddOpts{}); err != nil {
		t.Fatal(err)
	}

	stamp := day.Format("2006-01-02")
	got := qTitles(t, a, "created:"+stamp)
	want := []string{"0s", "13h0m0s", "23h59m59s"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("created:%s = %v, want the whole day %v", stamp, got, want)
	}
	if got := qTitles(t, a, "created:>"+stamp); !slices.Equal(got, []string{"next day"}) {
		t.Errorf("created:>%s must mean AFTER the whole day, got %v", stamp, got)
	}
}
