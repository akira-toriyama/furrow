package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

type statsJSON struct {
	Total  int `json:"total"`
	Drafts int `json:"drafts"`
	ByLane []struct {
		Lane  string `json:"lane"`
		Count int    `json:"count"`
	} `json:"by_lane"`
	ByRepo []struct {
		Repo  string `json:"repo"`
		Count int    `json:"count"`
	} `json:"by_repo"`
	ByLabel []struct {
		Label string `json:"label"`
		Count int    `json:"count"`
	} `json:"by_label"`
}

func TestStatsJSONShape(t *testing.T) {
	initStore(t)
	addTask(t, "one", "-l", "cli")
	addTask(t, "two", "-l", "cli", "-s", "backlog")
	addTask(t, "three", "-s", "backlog")

	out, code := run(t, "--json", "stats")
	if code != 0 {
		t.Fatalf("stats exit=%d:\n%s", code, out)
	}
	var s statsJSON
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	// by_lane is a complete histogram in configured order (inbox first).
	if len(s.ByLane) == 0 || s.ByLane[0].Lane != "inbox" || s.ByLane[0].Count != 1 {
		t.Errorf("by_lane[0] should be inbox=1, got %+v", s.ByLane)
	}
	// by_label: cli(2) most-used first.
	if len(s.ByLabel) != 1 || s.ByLabel[0].Label != "cli" || s.ByLabel[0].Count != 2 {
		t.Errorf("by_label should be cli=2, got %+v", s.ByLabel)
	}
}

func TestStatsEmptyBoardExit0(t *testing.T) {
	initStore(t)

	out, code := run(t, "--json", "stats")
	if code != 0 {
		t.Fatalf("stats on an empty board should exit 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"total": 0`) || !strings.Contains(out, `"by_repo": []`) {
		t.Errorf("empty board should be total 0 + empty vocab arrays:\n%s", out)
	}
}

func TestStatsNDJSONSingleObjectLine(t *testing.T) {
	initStore(t)
	addTask(t, "one")

	out, code := run(t, "--ndjson", "stats")
	if code != 0 {
		t.Fatalf("stats --ndjson exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "{") || !strings.Contains(lines[0], `"by_lane"`) {
		t.Errorf("--ndjson should emit one compact object line:\n%s", out)
	}
}

func TestStatsHumanSummary(t *testing.T) {
	initStore(t)
	addTask(t, "one", "-l", "cli")

	out, code := run(t, "stats")
	if code != 0 {
		t.Fatalf("stats human exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "total: 1") || !strings.Contains(out, "lanes:") ||
		!strings.Contains(out, "labels (1):") || !strings.Contains(out, "cli") {
		t.Errorf("human summary should show totals + labelled sections:\n%s", out)
	}
}

func TestStatsUnknownLaneExit2(t *testing.T) {
	initStore(t)
	addTask(t, "one")

	_, code := run(t, "stats", "-s", "ghost")
	if code != 2 {
		t.Fatalf("an unknown -s lane should exit 2, got %d", code)
	}
}
