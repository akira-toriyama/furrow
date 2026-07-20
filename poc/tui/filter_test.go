package main

import (
	"sort"
	"strings"
	"testing"
)

func fixtureGraph(t *testing.T) (*Board, *Graph) {
	t.Helper()
	b := NewBoard(fixtureTasks())
	return b, NewGraph(b)
}

// matched runs a query over the whole fixture and returns the ids, sorted.
func matched(t *testing.T, q string) []string {
	t.Helper()
	b, g := fixtureGraph(t)
	parsed := ParseQuery(q)
	var out []string
	for _, task := range b.Tasks() {
		if parsed.Match(task, g) {
			out = append(out, task.ID)
		}
	}
	sort.Strings(out)
	return out
}

func TestParseQueryShapes(t *testing.T) {
	tests := []struct {
		in       string
		terms    int
		problems int
		check    func(t *testing.T, q Query)
	}{
		{in: "", terms: 0},
		{in: "   ", terms: 0},
		{in: "lane:ready", terms: 1, check: func(t *testing.T, q Query) {
			if q.Terms[0].Key != "lane" || q.Terms[0].Vals[0] != "ready" {
				t.Errorf("got %+v", q.Terms[0])
			}
		}},
		{in: "status:ready", terms: 1, check: func(t *testing.T, q Query) {
			if q.Terms[0].Key != "lane" {
				t.Errorf("status: must alias to lane, got %q", q.Terms[0].Key)
			}
		}},
		{in: "-lane:done", terms: 1, check: func(t *testing.T, q Query) {
			if !q.Terms[0].Neg {
				t.Error("leading - must negate")
			}
		}},
		{in: "lane:inbox,backlog", terms: 1, check: func(t *testing.T, q Query) {
			if len(q.Terms[0].Vals) != 2 {
				t.Errorf("comma is OR within a token, got %v", q.Terms[0].Vals)
			}
		}},
		{in: "lane:ready repo:vista", terms: 2},
		{in: "filter bar", terms: 2, check: func(t *testing.T, q Query) {
			if q.Terms[0].Key != "" {
				t.Error("a bare word must have no key")
			}
		}},
		// Problems are reported but never fatal: the rest of the query still
		// applies, because you type a filter one character at a time.
		{in: "nope:x lane:ready", terms: 1, problems: 1},
		{in: "lane:", terms: 0, problems: 1},
		// An unknown VALUE is dropped, leaving no term. "Reported but not fatal"
		// has to mean the board still shows something: keeping the term made it
		// match nothing at all, so `is:bogus` silently emptied all 24 tasks
		// while the doc comment promised the opposite.
		{in: "is:wat", terms: 0, problems: 1},
		{in: "no:banana", terms: 0, problems: 1},
		// One good value beside a bad one keeps the good half.
		{in: "is:blocked,wat", terms: 1, problems: 1, check: func(t *testing.T, q Query) {
			if len(q.Terms[0].Vals) != 1 || q.Terms[0].Vals[0] != "blocked" {
				t.Errorf("the good half of the term must survive, got %v", q.Terms[0].Vals)
			}
		}},
		{in: "-", terms: 1}, // a lone dash is a bare word, not a negation
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			q := ParseQuery(tc.in)
			if len(q.Terms) != tc.terms {
				t.Errorf("terms = %d, want %d (%+v)", len(q.Terms), tc.terms, q.Terms)
			}
			if len(q.Problems) != tc.problems {
				t.Errorf("problems = %d, want %d (%v)", len(q.Problems), tc.problems, q.Problems)
			}
			if tc.check != nil && len(q.Terms) > 0 {
				tc.check(t, q)
			}
		})
	}
}

func TestQueryMatchesFixture(t *testing.T) {
	_, g := fixtureGraph(t)

	tests := []struct {
		q    string
		want []string // exact set, or nil to use the checks below
		min  int
	}{
		{q: "lane:ready", want: []string{"t-n2fc"}},
		{q: "lane:in-progress", want: []string{"t-fw2m"}},
		{q: "type:epic", want: []string{"t-fw2m"}},
		{q: "is:actionable", want: []string{"t-n2fc"}},
		{q: "is:epic", want: []string{"t-fw2m"}},
		{q: "id:t-jv3j", want: []string{"t-jv3j"}},
		{q: "parent:t-fw2m", min: 18},
		{q: "repo:vista", min: 22},
		{q: "label:ui", min: 9},
		{q: "no:label", min: 1},
		{q: "no:repo", want: []string{}}, // the fixture has no drafts
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			got := matched(t, tc.q)
			if tc.want != nil {
				if strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Errorf("%s = %v, want %v", tc.q, got, tc.want)
				}
				return
			}
			if len(got) < tc.min {
				t.Errorf("%s matched %d, want >= %d", tc.q, len(got), tc.min)
			}
		})
	}

	// is:blocked must agree with the graph, exactly — one definition, shared.
	blocked := matched(t, "is:blocked")
	var fromGraph []string
	b := NewBoard(fixtureTasks())
	for _, task := range b.Tasks() {
		if len(g.BlockedBy(task.ID)) > 0 {
			fromGraph = append(fromGraph, task.ID)
		}
	}
	sort.Strings(fromGraph)
	if strings.Join(blocked, ",") != strings.Join(fromGraph, ",") {
		t.Errorf("is:blocked = %v, graph says %v", blocked, fromGraph)
	}
}

func TestQueryAndOrNegation(t *testing.T) {
	// Separate tokens AND.
	both := matched(t, "lane:backlog is:blocked")
	onlyLane := matched(t, "lane:backlog")
	onlyBlocked := matched(t, "is:blocked")
	if len(both) >= len(onlyLane) || len(both) == 0 {
		t.Errorf("AND did not narrow: %d vs %d", len(both), len(onlyLane))
	}
	for _, id := range both {
		if !contains(onlyLane, id) || !contains(onlyBlocked, id) {
			t.Errorf("%s matched the conjunction but not both halves", id)
		}
	}

	// Comma ORs within a token.
	or := matched(t, "lane:ready,in-progress")
	if len(or) != len(matched(t, "lane:ready"))+len(matched(t, "lane:in-progress")) {
		t.Errorf("comma OR = %v", or)
	}

	// Negation is the complement.
	all := matched(t, "")
	notDone := matched(t, "-lane:done")
	done := matched(t, "lane:done")
	if len(notDone)+len(done) != len(all) {
		t.Errorf("negation is not the complement: %d + %d != %d", len(notDone), len(done), len(all))
	}
}

func TestQueryBareWordIsCaseInsensitiveSubstring(t *testing.T) {
	if got := matched(t, "KANBAN"); len(got) == 0 {
		t.Error("bare word must be case-insensitive")
	}
	// A CJK bare word has to work: the whole fixture is Japanese titles.
	got := matched(t, "依存")
	if len(got) == 0 {
		t.Fatal("CJK bare word matched nothing")
	}
	for _, id := range got {
		b := NewBoard(fixtureTasks())
		if !strings.Contains(b.Task(id).Title, "依存") {
			t.Errorf("%s does not contain the needle", id)
		}
	}
	// A bare word also matches an id, which is how you paste one in.
	if got := matched(t, "t-jv3j"); len(got) != 1 || got[0] != "t-jv3j" {
		t.Errorf("bare id = %v", got)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
