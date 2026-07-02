package app

import (
	"sort"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// revisitApp builds an app with a mutable clock so tests can age tasks.
func revisitApp() (*App, *fixedClock) {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk), clk
}

func codesByID(items []RevisitItem) map[string][]string {
	m := map[string][]string{}
	for _, it := range items {
		cs := make([]string, len(it.Reasons))
		for i, r := range it.Reasons {
			cs[i] = r.Code
		}
		sort.Strings(cs)
		m[it.Task.ID] = cs
	}
	return m
}

func p(n int) *int { return &n }

func TestRevisitSignalsAndExclusions(t *testing.T) {
	a, clk := revisitApp()

	// At T0: a dependency we will finish, and a stale (but estimated) task.
	dep, _ := a.Add("dep", AddOpts{Status: "ready", Value: p(1), Effort: p(1)})
	a.Done(dep.ID) // dep -> done lane (terminal)
	stale, _ := a.Add("stale", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"o/r"}})
	a.Add("parked", AddOpts{Status: "icebox"}) // terminal; unset estimates

	// Advance 60 days, then add fresh tasks.
	clk.t = clk.t.AddDate(0, 0, 60)
	fresh, _ := a.Add("fresh-needs-est", AddOpts{Status: "ready", Repos: []string{"o/r"}}) // value+effort unset, fresh
	user, _ := a.Add("dep-user", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	got := codesByID(items)

	if want := []string{"stale"}; !eq(got[stale.ID], want) {
		t.Errorf("stale task: codes = %v, want %v", got[stale.ID], want)
	}
	if want := []string{"effort_unset", "value_unset"}; !eq(got[fresh.ID], want) {
		t.Errorf("fresh-needs-est: codes = %v, want %v", got[fresh.ID], want)
	}
	if want := []string{"dep_done"}; !eq(got[user.ID], want) {
		t.Errorf("dep-user: codes = %v, want %v", got[user.ID], want)
	}
	// terminal (done dep, icebox parked) must never surface.
	if _, ok := got[dep.ID]; ok {
		t.Errorf("done dep must be excluded, got %v", got[dep.ID])
	}
	if len(items) != 3 {
		t.Errorf("expected exactly 3 items (stale, fresh, dep-user), got %d: %v", len(items), got)
	}
}

func TestRevisitStaleDaysZeroDisablesStale(t *testing.T) {
	a, clk := revisitApp()
	st, _ := a.Add("old-but-estimated", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"o/r"}})
	clk.t = clk.t.AddDate(0, 0, 90)

	items, _ := a.Revisit(QueryOpts{}, 0) // staleDays 0 disables stale
	if got := codesByID(items); len(got[st.ID]) != 0 {
		t.Errorf("with staleDays=0 the estimated task should have no reasons, got %v", got[st.ID])
	}
}

func TestRevisitLabelFilterAndLimit(t *testing.T) {
	a, _ := revisitApp()
	a.Add("furrow task", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	a.Add("chord task", AddOpts{Status: "ready", Labels: []string{"chord"}})

	only, _ := a.Revisit(QueryOpts{Label: "furrow"}, 30)
	if len(only) != 1 || only[0].Task.Labels[0] != "furrow" {
		t.Errorf("label filter should keep only furrow, got %+v", only)
	}

	a.Add("furrow task 2", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	limited, _ := a.Revisit(QueryOpts{Label: "furrow", Limit: 1}, 30)
	if len(limited) != 1 {
		t.Errorf("limit=1 should return 1 item, got %d", len(limited))
	}
}

func TestRevisitCanonicalOrderAndLimitIdentity(t *testing.T) {
	a, _ := revisitApp()
	// Added out of canonical order; all unestimated so all surface. Canonical
	// order is lane-rank -> priority -> id: ready (rank 2) sorts before
	// in-progress (rank 3), and within ready the sparse priority orders them.
	ip, _ := a.Add("in progress", AddOpts{Status: "in-progress"})
	r1, _ := a.Add("ready a", AddOpts{Status: "ready"}) // priority 100
	r2, _ := a.Add("ready b", AddOpts{Status: "ready"}) // priority 110

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := make([]string, len(items))
	for i, it := range items {
		gotOrder[i] = it.Task.ID
	}
	if want := []string{r1.ID, r2.ID, ip.ID}; !eq(gotOrder, want) {
		t.Errorf("revisit order = %v, want canonical %v", gotOrder, want)
	}

	// limit must return the canonical-FIRST item, not just any one.
	one, _ := a.Revisit(QueryOpts{Limit: 1}, 30)
	if len(one) != 1 || one[0].Task.ID != r1.ID {
		t.Errorf("limit=1 should return canonical-first %s, got %+v", r1.ID, one)
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
