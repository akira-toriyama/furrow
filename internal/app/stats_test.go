package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func laneCount(s Stats, lane string) int {
	for _, c := range s.ByLane {
		if c.Key == lane {
			return c.Count
		}
	}
	return -1
}

func TestStatsDistribution(t *testing.T) {
	a := newApp()
	a.Add("t1", AddOpts{Labels: []string{"cli", "bug"}})
	a.Add("t2", AddOpts{Status: "backlog", Labels: []string{"cli"}})
	a.Add("t3", AddOpts{Status: "backlog"})

	s, err := a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	// by_lane is a complete histogram in configured order (0-count lanes present).
	if got := laneCount(s, "inbox"); got != 1 {
		t.Errorf("inbox count = %d, want 1", got)
	}
	if got := laneCount(s, "backlog"); got != 2 {
		t.Errorf("backlog count = %d, want 2", got)
	}
	if got := laneCount(s, "ready"); got != 0 {
		t.Errorf("an empty configured lane should still appear with 0, got %d", got)
	}
	// by_label: cli(2) before bug(1), most-used first.
	if len(s.ByLabel) != 2 || s.ByLabel[0].Key != "cli" || s.ByLabel[0].Count != 2 || s.ByLabel[1].Key != "bug" {
		t.Errorf("by_label should be cli(2), bug(1) most-used first, got %+v", s.ByLabel)
	}
}

func TestStatsLaneOrderMatchesConfig(t *testing.T) {
	a := newApp()
	a.Add("t1", AddOpts{})
	s, err := a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ByLane) != len(a.Cfg.Lanes) {
		t.Fatalf("by_lane should list every configured lane, got %d want %d", len(s.ByLane), len(a.Cfg.Lanes))
	}
	for i, lane := range a.Cfg.Lanes {
		if s.ByLane[i].Key != lane {
			t.Errorf("by_lane[%d] = %q, want configured order %q", i, s.ByLane[i].Key, lane)
		}
	}
}

func TestStatsTiesSortByKey(t *testing.T) {
	a := newApp()
	a.Add("t1", AddOpts{Labels: []string{"zebra", "apple"}})
	s, err := a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// both count 1 -> tie broken by key ascending.
	if len(s.ByLabel) != 2 || s.ByLabel[0].Key != "apple" || s.ByLabel[1].Key != "zebra" {
		t.Errorf("ties should sort by key ascending, got %+v", s.ByLabel)
	}
}

func TestStatsScopeFilter(t *testing.T) {
	a := newApp()
	a.Add("t1", AddOpts{Labels: []string{"cli"}})
	a.Add("t2", AddOpts{Status: "backlog"})

	// -s narrows the aggregated set.
	s, err := a.Stats(QueryOpts{Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 1 || laneCount(s, "backlog") != 1 || laneCount(s, "inbox") != 0 {
		t.Errorf("status scope should aggregate only backlog, got %+v", s)
	}
}

func TestStatsEmptyBoard(t *testing.T) {
	a := newApp()
	s, err := a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 0 || len(s.ByRepo) != 0 || len(s.ByLabel) != 0 {
		t.Errorf("empty board should have zero total and empty vocab, got %+v", s)
	}
	// by_lane still lists the configured lanes (all 0) — a valid clean result.
	if len(s.ByLane) != len(a.Cfg.Lanes) {
		t.Errorf("by_lane should still enumerate configured lanes on an empty board")
	}
}

func TestStatsUnknownLaneFilterFailsFast(t *testing.T) {
	a := newApp()
	a.Add("t1", AddOpts{})
	if _, err := a.Stats(QueryOpts{Status: "ghost"}); core.AsError(err) == nil || core.AsError(err).Code != core.CodeValidation {
		t.Fatalf("an unknown -s lane should fail fast (exit 2), got %v", err)
	}
}

// seedTask injects a task with explicit timestamps straight through the store —
// the window tests need created/closed/updated to differ, which Add/Done under
// one fixed clock cannot produce.
func seedTimedTask(t *testing.T, a *App, id string, created, updated time.Time, closed *time.Time, repos ...string) {
	t.Helper()
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	status := "ready"
	if closed != nil {
		status = "done"
	}
	idx.Add(core.Task{ID: id, Title: id, Status: status, Priority: 100,
		Created: created, Updated: updated, Closed: closed,
		Repos: repos, Body: core.BodyPath(id)})
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
}

func TestStatsWindowFlow(t *testing.T) {
	a := newApp()
	day := func(d int) time.Time { return time.Date(2026, 8, d, 12, 0, 0, 0, time.UTC) }
	before, in1, in2, after := day(1), day(3), day(4), day(9)

	seedTimedTask(t, a, "t-old01", before, before, nil)   // created before window
	seedTimedTask(t, a, "t-new01", in2, in2, nil)         // created in window
	seedTimedTask(t, a, "t-new02", in1, in1, nil)         // created in window (earlier)
	seedTimedTask(t, a, "t-done1", before, in1, &in1)     // closed in window
	seedTimedTask(t, a, "t-done2", before, after, &after) // closed after window
	seedTimedTask(t, a, "t-both1", in1, in2, &in2)        // created AND closed in window
	// closed in window, then touched after it: must still count as closed.
	seedTimedTask(t, a, "t-late1", before, after, &in2)

	since, until := day(2), day(5)
	s, err := a.Stats(QueryOpts{Since: &since, Until: &until})
	if err != nil {
		t.Fatal(err)
	}
	if s.Window == nil {
		t.Fatal("a --since/--until stats must carry a window section")
	}
	// Chronological, id tiebreak on equal stamps: t-both1/t-new02 (both day 3,
	// id order) before t-new01 (day 4).
	wantCreated := []string{"t-both1", "t-new02", "t-new01"}
	if !reflect.DeepEqual(s.Window.Created, wantCreated) {
		t.Errorf("created = %v, want %v", s.Window.Created, wantCreated)
	}
	wantClosed := []string{"t-done1", "t-both1", "t-late1"}
	if !reflect.DeepEqual(s.Window.Closed, wantClosed) {
		t.Errorf("closed = %v, want %v", s.Window.Closed, wantClosed)
	}
	// The distributions keep the UPDATED-window semantics `ls` has: t-late1
	// (updated after the window) is out of Total but in the closed flow.
	for _, tc := range []struct {
		id string
		in bool
	}{{"t-late1", false}, {"t-new01", true}} {
		found := false
		list, _ := a.List(QueryOpts{Since: &since, Until: &until})
		for _, x := range list {
			if x.ID == tc.id {
				found = true
			}
		}
		if found != tc.in {
			t.Errorf("updated-window membership of %s = %v, want %v", tc.id, found, tc.in)
		}
	}

	// Open-ended window: --since only.
	s2, err := a.Stats(QueryOpts{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Window.Closed) != 4 { // t-done1, t-both1, t-late1, t-done2
		t.Errorf("open-until closed = %v", s2.Window.Closed)
	}
	// No window flags -> no window section (the classic stats object).
	s3, err := a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if s3.Window != nil {
		t.Errorf("stats without a window must not carry one, got %+v", s3.Window)
	}
}

// The window scan honors the same scope filters as the distributions: a repo
// scope keeps foreign-repo flow out.
func TestStatsWindowRespectsScope(t *testing.T) {
	a := newApp()
	day3 := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	seedTimedTask(t, a, "t-mine1", day3, day3, nil, "o/mine")
	seedTimedTask(t, a, "t-other", day3, day3, nil, "o/other")

	since := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	s, err := a.Stats(QueryOpts{Repo: "o/mine", Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Window.Created, []string{"t-mine1"}) {
		t.Errorf("scoped created = %v, want [t-mine1]", s.Window.Created)
	}
}

// A task archived inside the window still counts as closed: the flow unions
// the archive store, so an archive sweep cannot deflate the session's closed
// count (the budget check would otherwise under-credit the session).
func TestStatsWindowUnionsArchive(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Clock = &fixedClock{t: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

	closed := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	seedTimedTask(t, a, "t-arch1", closed.Add(-time.Hour), closed, &closed)
	if _, err := a.ArchiveIDs([]string{"t-arch1"}, false); err != nil {
		t.Fatal(err)
	}
	// Gone from the hot store…
	if _, _, err := a.Get("t-arch1"); core.AsError(err) == nil {
		t.Fatal("t-arch1 should be archived out of the hot store")
	}
	// …but still in the window's closed flow.
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	s, err := a.Stats(QueryOpts{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Window.Closed, []string{"t-arch1"}) {
		t.Errorf("archived task must stay in the closed flow, got %v", s.Window.Closed)
	}
	if !reflect.DeepEqual(s.Window.Created, []string{"t-arch1"}) {
		t.Errorf("archived task's creation is in-window too, got %v", s.Window.Created)
	}
}

// Drafts spans the repo dimension: a repo scope (auto or -r) must not zero it
// — the old order ran the full match first, so a scoped stats structurally
// reported drafts: 0 while brief said otherwise (t-sv35 (c)).
func TestStatsDraftsCountedUnderRepoScope(t *testing.T) {
	a := newApp()
	a.Add("scoped", AddOpts{Repos: []string{"me/demo"}})
	a.Add("draft plain", AddOpts{})
	a.Add("draft tagged", AddOpts{Labels: []string{"bug"}})

	s, err := a.Stats(QueryOpts{ScopeRepo: "me/demo"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 1 {
		t.Errorf("total = %d, want 1 (the scoped task only)", s.Total)
	}
	if s.Drafts != 2 {
		t.Errorf("drafts = %d, want 2 (the repo dimension must not zero drafts)", s.Drafts)
	}

	// Board-wide, drafts equals brief's count and total includes them.
	s, err = a.Stats(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 || s.Drafts != 2 {
		t.Errorf("board-wide total/drafts = %d/%d, want 3/2", s.Total, s.Drafts)
	}
}

// The non-repo filters still bind the draft count: -s and -l apply (brief has
// no -s/-l, which is the only reason its fresh QueryOpts is correct there).
func TestStatsDraftsHonorStatusAndLabel(t *testing.T) {
	a := newApp()
	a.Add("scoped", AddOpts{Repos: []string{"me/demo"}})
	a.Add("draft bug", AddOpts{Labels: []string{"bug"}})
	a.Add("draft chore", AddOpts{Status: "backlog", Labels: []string{"chore"}})

	s, err := a.Stats(QueryOpts{ScopeRepo: "me/demo", Label: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Drafts != 1 {
		t.Errorf("drafts = %d, want 1 (-l must bind the draft count)", s.Drafts)
	}
	s, err = a.Stats(QueryOpts{ScopeRepo: "me/demo", Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Drafts != 1 {
		t.Errorf("drafts = %d, want 1 (-s must bind the draft count)", s.Drafts)
	}
	if s.Total != 0 {
		t.Errorf("total = %d, want 0 (the scoped task is not in backlog)", s.Total)
	}
}
