package app

import (
	"testing"

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
