package app

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// qTitles runs List with just a -q query and returns the matched titles,
// sorted — titles read better than random ids in a failing diff.
func qTitles(t *testing.T, a *App, q string) []string {
	t.Helper()
	tasks, err := a.List(QueryOpts{Query: q})
	if err != nil {
		t.Fatalf("List(-q %q): %v", q, err)
	}
	titles := make([]string, len(tasks))
	for i := range tasks {
		titles[i] = tasks[i].Title
	}
	slices.Sort(titles)
	return titles
}

// qErr compiles a -q query expecting a *core.Error, returning it.
func qErr(t *testing.T, a *App, q string) *core.Error {
	t.Helper()
	idx, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.compileQuery(q, idx, a.Cfg.RevisitStaleDays, nil)
	if err == nil {
		t.Fatalf("compileQuery(%q) should have failed", q)
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("compileQuery(%q) error is not *core.Error: %v", q, err)
	}
	return ce
}

// dateFixtureApp builds a board whose timestamps are controlled by the fixed
// clock: "old" at T0 (2026-06-01), "mid" and the closed "closer" at T0+10d,
// "fresh" at T0+40d — which is also NOW when the queries run.
func dateFixtureApp(t *testing.T) (*App, *fixedClock) {
	t.Helper()
	a := newApp()
	clk := a.Clock.(*fixedClock)
	clk.t = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mustAdd(t, a, "old", AddOpts{Status: "ready"})
	clk.t = clk.t.AddDate(0, 0, 10) // 2026-06-11 12:00
	mustAdd(t, a, "mid", AddOpts{Status: "ready"})
	closer := mustAdd(t, a, "closer", AddOpts{Status: "ready"})
	if _, err := a.Done(closer.ID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.AddDate(0, 0, 30) // 2026-07-11 12:00 = now
	mustAdd(t, a, "fresh", AddOpts{Status: "ready"})
	return a, clk
}

func mustAdd(t *testing.T, a *App, title string, o AddOpts) *core.Task {
	t.Helper()
	task, err := a.Add(title, o)
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return task
}

// TestQueryDates pins the date qualifiers: a bare day is a whole-day interval,
// comparisons respect interval ends, relative offsets bind against the Clock,
// ranges are inclusive with * open ends, and a nil closed/reviewed satisfies
// no comparison while negation includes the unset.
func TestQueryDates(t *testing.T) {
	a, _ := dateFixtureApp(t)
	cases := []struct {
		q    string
		want []string
	}{
		// Relative offsets (now = 2026-07-11 12:00). mid/closer sit EXACTLY 30d
		// back: >= includes the boundary instant, < excludes it.
		{"updated:>=-30d", []string{"closer", "fresh", "mid"}},
		{"created:<-30d", []string{"old"}},
		{"updated:>=-2w", []string{"fresh"}},
		// A bare day = the whole UTC day.
		{"created:2026-06-01", []string{"old"}},
		{"created:>2026-06-01", []string{"closer", "fresh", "mid"}}, // strictly after the DAY
		{"created:>=2026-06-01", []string{"closer", "fresh", "mid", "old"}},
		{"created:<2026-06-02", []string{"old"}},
		{"created:<=2026-06-01", []string{"old"}},
		// Comma = OR of days.
		{"created:2026-06-01,2026-06-11", []string{"closer", "mid", "old"}},
		// Ranges, ends inclusive; * = open.
		{"created:2026-06-01..2026-06-11", []string{"closer", "mid", "old"}},
		{"created:*..2026-06-01", []string{"old"}},
		{"created:2026-06-11..*", []string{"closer", "fresh", "mid"}},
		{"updated:-31d..-29d", []string{"closer", "mid"}},
		// RFC3339 instants.
		{"updated:>=2026-06-11T12:00:00Z", []string{"closer", "fresh", "mid"}},
		{"updated:>2026-06-11T12:00:00Z", []string{"fresh"}},
		// Nullable closed: nil never satisfies a comparison; negation includes it.
		{"closed:>=2026-06-01", []string{"closer"}},
		{"-closed:>=2026-06-01", []string{"fresh", "mid", "old"}},
		{"has:closed", []string{"closer"}},
	}
	for _, c := range cases {
		if got := qTitles(t, a, c.q); !slices.Equal(got, c.want) {
			t.Errorf("-q %q = %v, want %v", c.q, got, c.want)
		}
	}
}

// TestQueryReviewed pins the reviewed timestamp: stamped by ReviewTask, never
// satisfied while nil, negation includes the unset.
func TestQueryReviewed(t *testing.T) {
	a, _ := dateFixtureApp(t)
	tasks, err := a.List(QueryOpts{Query: "title:'mid'"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("fixture lookup: %v %v", tasks, err)
	}
	if _, err := a.ReviewTask(tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := qTitles(t, a, "reviewed:>=-1d"); !slices.Equal(got, []string{"mid"}) {
		t.Errorf("reviewed:>=-1d = %v, want [mid]", got)
	}
	if got := qTitles(t, a, "no:reviewed"); !slices.Equal(got, []string{"closer", "fresh", "old"}) {
		t.Errorf("no:reviewed = %v", got)
	}
	if got := qTitles(t, a, "-reviewed:>=-1d"); !slices.Equal(got, []string{"closer", "fresh", "old"}) {
		t.Errorf("-reviewed:>=-1d must include the never-reviewed: %v", got)
	}
}

// TestQueryIsStale pins is:stale on core.IsStale — revisit's own definition
// ([revisit].stale_days, default 30; >= at the boundary; a PURE age test that
// deliberately does not imply open, so a closed task can be stale too).
func TestQueryIsStale(t *testing.T) {
	a, _ := dateFixtureApp(t)
	// now-updated: old 40d, mid/closer exactly 30d (>= threshold), fresh 0d.
	// closer is DONE and still listed: is:stale is age, not lane — compose
	// `is:open is:stale` for revisit's view.
	if got := qTitles(t, a, "is:stale"); !slices.Equal(got, []string{"closer", "mid", "old"}) {
		t.Errorf("is:stale = %v, want [closer mid old]", got)
	}
	if got := qTitles(t, a, "-is:stale"); !slices.Equal(got, []string{"fresh"}) {
		t.Errorf("-is:stale = %v, want [fresh]", got)
	}
	if got := qTitles(t, a, "is:open is:stale"); !slices.Equal(got, []string{"mid", "old"}) {
		t.Errorf("is:open is:stale = %v, want [mid old]", got)
	}
	// The staleDays parameter is honored: 0 disables (nothing stale), 7 keeps
	// the 10d+ tasks — the path `revisit --stale-days N -q is:stale` rides.
	idx, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		days int
		want int
	}{{0, 0}, {7, 3}, {35, 1}} {
		p, err := a.compileQuery("is:stale", idx, tc.days, nil)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for i := range idx.Tasks {
			ok, err := p(&idx.Tasks[i])
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				n++
			}
		}
		if n != tc.want {
			t.Errorf("is:stale with staleDays=%d matched %d tasks, want %d", tc.days, n, tc.want)
		}
	}
}

// TestQueryGraph pins the direct-edge graph qualifiers: parent:/child-of: (two
// spellings of the same down edge), depends-on: and blocks: (the two directions
// of the Deps edge), and the lenient unknown-id contract (0 rows, exit 0).
func TestQueryGraph(t *testing.T) {
	a := newApp()
	p := mustAdd(t, a, "p", AddOpts{})
	c1 := mustAdd(t, a, "c1", AddOpts{Parent: p.ID})
	c2 := mustAdd(t, a, "c2", AddOpts{Parent: p.ID, Deps: []string{c1.ID}})
	mustAdd(t, a, "d3", AddOpts{Deps: []string{c1.ID, c2.ID}})

	cases := []struct {
		q    string
		want []string
	}{
		{"parent:" + p.ID, []string{"c1", "c2"}},
		{"child-of:" + p.ID, []string{"c1", "c2"}}, // the graph spelling of parent:
		{"-child-of:" + p.ID, []string{"d3", "p"}},
		{"depends-on:" + c1.ID, []string{"c2", "d3"}},
		{"depends-on:" + c2.ID, []string{"d3"}},
		{"depends-on:" + c1.ID + "," + c2.ID, []string{"c2", "d3"}}, // comma = OR
		{"blocks:" + c2.ID, []string{"c1"}},
		{"blocks:t-nope0", []string{}}, // unknown id blocks nothing — lenient
		{"depends-on:t-nope0", []string{}},
		{"child-of:t-nope0", []string{}},
	}
	for _, c := range cases {
		if got := qTitles(t, a, c.q); !slices.Equal(got, c.want) {
			t.Errorf("-q %q = %v, want %v", c.q, got, c.want)
		}
	}
	// blocks:d3 needs the id of d3 — both its deps block it.
	tasks, err := a.List(QueryOpts{Query: "title:'d3'"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("fixture lookup: %v %v", tasks, err)
	}
	if got := qTitles(t, a, "blocks:"+tasks[0].ID); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("blocks:d3 = %v, want [c1 c2]", got)
	}
}

// TestQueryFreeTextAndBody pins D1: free text is search's matcher over
// title+body (case-insensitive substring), body: is the explicit body
// qualifier, and a QUOTED value on a text qualifier is whole-field equality
// (still case-folded) where a bare one is a substring.
func TestQueryFreeTextAndBody(t *testing.T) {
	a := newApp()
	mustAdd(t, a, "alpha", AddOpts{Body: "# alpha\n\nthe needle paragraph\n"})
	mustAdd(t, a, "beta needle", AddOpts{Body: "# beta\n"})
	gamma := mustAdd(t, a, "gamma", AddOpts{})
	mustAdd(t, a, "Bug fix", AddOpts{})
	mustAdd(t, a, "Bug fixes", AddOpts{})

	cases := []struct {
		q    string
		want []string
	}{
		{"needle", []string{"alpha", "beta needle"}}, // title OR body, like `furrow search needle`
		{"NEEDLE", []string{"alpha", "beta needle"}}, // case-folded
		{"body:needle", []string{"alpha"}},           // body only — beta's hit is in the title
		{"-body:needle", []string{"Bug fix", "Bug fixes", "beta needle", "gamma"}},
		{`"bug fix"`, []string{"Bug fix", "Bug fixes"}}, // quoted phrase = substring
		{`title:'Bug fix'`, []string{"Bug fix"}},        // quoted qualifier = exact title
		{`title:'bug fix'`, []string{"Bug fix"}},        // …still case-insensitive
		{`title:"Bug fix"`, []string{"Bug fix"}},        // both quote styles
		{"title:fix", []string{"Bug fix", "Bug fixes"}}, // bare = substring
		{"-title:'Bug fix'", []string{"Bug fixes", "alpha", "beta needle", "gamma"}},
	}
	for _, c := range cases {
		if got := qTitles(t, a, c.q); !slices.Equal(got, c.want) {
			t.Errorf("-q %q = %v, want %v", c.q, got, c.want)
		}
	}

	// has:body / no:body — presence of non-whitespace content. Every add seeds
	// a heading, so empty a body deliberately to get a no:body hit.
	if err := a.Store.SaveBody(gamma.ID, "  \n"); err != nil {
		t.Fatal(err)
	}
	if got := qTitles(t, a, "no:body"); !slices.Equal(got, []string{"gamma"}) {
		t.Errorf("no:body = %v, want [gamma]", got)
	}
}

// countingStore counts LoadBody calls — the probe that a query with no
// body-reading term never pays for a body.
type countingStore struct {
	Store
	loads int
}

func (c *countingStore) LoadBody(id string) (string, error) {
	c.loads++
	return c.Store.LoadBody(id)
}

// TestQueryBodyLoadIsLazy pins the cost contract: no body-matching term = zero
// body reads; a free-text term reads bodies only for tasks whose title missed;
// and each body is read at most once per query run.
func TestQueryBodyLoadIsLazy(t *testing.T) {
	a := newApp()
	mustAdd(t, a, "needle title", AddOpts{Status: "ready"})
	mustAdd(t, a, "other", AddOpts{Status: "ready"})
	mustAdd(t, a, "third", AddOpts{})
	cs := &countingStore{Store: a.Store}
	a.Store = cs

	if _, err := a.List(QueryOpts{Query: "status:ready is:open no:value"}); err != nil {
		t.Fatal(err)
	}
	if cs.loads != 0 {
		t.Errorf("a query with no body term must not read bodies; read %d", cs.loads)
	}

	if _, err := a.List(QueryOpts{Query: "needle"}); err != nil {
		t.Fatal(err)
	}
	// "needle title" matched by title (no body read); the other two missed and
	// paid one body read each.
	if cs.loads != 2 {
		t.Errorf("free text should read only the title-missed bodies once: %d loads, want 2", cs.loads)
	}
}

// TestQueryErrors pins the exit-2 contract for the part-2 families: bad dates,
// a bare relative equality, and the widened unknown-field candidates.
func TestQueryErrors(t *testing.T) {
	a := newApp()
	mustAdd(t, a, "x", AddOpts{})

	cases := []struct{ q, id string }{
		{"created:notadate", "query-type"},
		{"updated:-2w", "query-type"}, // a relative offset needs a comparison/range
		{"updated:2026-13-45", "query-type"},
		{"closed:>nope", "query-type"},
		{"title:>x", "query-type"}, // text fields take no operator
		{"descendant-of:t-1", "query-unknown-field"},
		{"is:overdue", "query-unknown-flag"},
		{"has:children", "query-unknown-field"},
	}
	for _, c := range cases {
		ce := qErr(t, a, c.q)
		if ce.Code != core.CodeValidation {
			t.Errorf("-q %q code = %v, want validation", c.q, ce.Code)
		}
		if ce.ID != c.id {
			t.Errorf("-q %q id = %q, want %q", c.q, ce.ID, c.id)
		}
	}

	// The unknown-field candidates name the full v1 vocabulary, including the
	// part-2 fields — the did-you-mean surface a front-end completes from.
	ce := qErr(t, a, "descendant-of:t-1")
	for _, want := range []string{"child-of", "depends-on", "blocks", "created", "updated", "closed", "reviewed", "body"} {
		if !slices.Contains(ce.Candidates, want) {
			t.Errorf("unknown-field candidates missing %q: %v", want, ce.Candidates)
		}
	}
}

// FuzzCompileQuery pins that compile (parse + bind) never panics on arbitrary
// input against a live index — errors are fine, crashes are not. The date and
// graph binders parse untrusted value text, so they get the same panic-freedom
// bar as the parser itself.
func FuzzCompileQuery(f *testing.F) {
	for _, s := range []string{
		"", "status:ready", "is:stale", "updated:>=-2w", "created:2026-07-01..2026-07-15",
		"closed:<-30d", "reviewed:*..-1d", "blocks:t-x", "depends-on:t-1,t-2",
		"child-of:t-y", "body:needle", "has:body", `title:'Bug fix'`, "needle",
		"value:>=4 -label:a,b", "updated:..", "created:-0d", "updated:+1w",
		"created:9999-12-31", "updated:-99999999999999999999d",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a := newApp()
		if _, err := a.Add("seed", AddOpts{Status: "ready"}); err != nil {
			t.Skip()
		}
		idx, err := a.load()
		if err != nil {
			t.Skip()
		}
		p, err := a.compileQuery(s, idx, 30, nil)
		if err != nil || p == nil {
			return
		}
		for i := range idx.Tasks {
			_, _ = p(&idx.Tasks[i]) // must not panic
		}
	})
}
