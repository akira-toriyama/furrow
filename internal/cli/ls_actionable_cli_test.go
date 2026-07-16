package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// lsItemView mirrors the flat `ls --json` row: the task fields we assert on plus
// the derived facts the change adds.
type lsItemView struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	Actionable bool     `json:"actionable"`
	BlockedBy  []string `json:"blocked_by"`
	Container  bool     `json:"container"`
	Stuck      bool     `json:"stuck"`
}

func lsJSON(t *testing.T, args ...string) []lsItemView {
	t.Helper()
	out, code := run(t, append([]string{"ls", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("ls --json exit = %d:\n%s", code, out)
	}
	var views []lsItemView
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("parse ls --json: %v\n%s", err, out)
	}
	return views
}

// TestCLILsDerivedFactsJSON: ls --json carries actionable / blocked_by / container
// / stuck on every row (the deliverable — the derived facts were tree-only before).
func TestCLILsDerivedFactsJSON(t *testing.T) {
	initStore(t)
	gate := addTask(t, "gate", "-s", "ready")
	blocked := addTask(t, "waits on gate", "-s", "ready")
	if _, code := run(t, "dep", blocked, gate); code != 0 {
		t.Fatalf("dep exit = %d", code)
	}

	views := lsJSON(t)
	byID := map[string]lsItemView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if g := byID[gate]; !g.Actionable || len(g.BlockedBy) != 0 || g.Container {
		t.Errorf("gate row should be actionable/unblocked/non-container: %+v", g)
	}
	if b := byID[blocked]; b.Actionable || len(b.BlockedBy) != 1 || b.BlockedBy[0] != gate {
		t.Errorf("blocked row should be non-actionable with blocked_by=[gate]: %+v", b)
	}
}

// TestCLILsActionableBlockedFilters: the --actionable / --blocked filters narrow
// the flat list by derived state, and are mutually exclusive (a task can't be
// both), which cobra enforces as exit 2.
func TestCLILsActionableBlockedFilters(t *testing.T) {
	initStore(t)
	gate := addTask(t, "gate", "-s", "ready")
	blocked := addTask(t, "waits on gate", "-s", "ready")
	if _, code := run(t, "dep", blocked, gate); code != 0 {
		t.Fatalf("dep exit = %d", code)
	}

	act := lsJSON(t, "--actionable")
	if len(act) != 1 || act[0].ID != gate {
		t.Errorf("--actionable should keep only the gate: %+v", act)
	}
	blk := lsJSON(t, "--blocked")
	if len(blk) != 1 || blk[0].ID != blocked {
		t.Errorf("--blocked should keep only the blocked task: %+v", blk)
	}

	// Mutually exclusive: combining them is a usage error (exit 2), not an
	// always-empty result.
	if _, code := run(t, "ls", "--actionable", "--blocked"); code != int(core.CodeValidation) {
		t.Errorf("--actionable --blocked should be exit 2 (mutually exclusive), got %d", code)
	}
}

// TestCLILsGlyphColumn: the human `ls` gains a leading one-character state glyph —
// ★ for an actionable task, · for one that is merely open (blocked).
func TestCLILsGlyphColumn(t *testing.T) {
	initStore(t)
	gate := addTask(t, "gate", "-s", "ready")
	blocked := addTask(t, "waits on gate", "-s", "ready")
	if _, code := run(t, "dep", blocked, gate); code != 0 {
		t.Fatalf("dep exit = %d", code)
	}

	out, code := run(t, "ls")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "★  "+gate) {
		t.Errorf("actionable gate should render with a ★ glyph:\n%s", out)
	}
	if !strings.Contains(out, "·  "+blocked) {
		t.Errorf("blocked task should render with a · glyph:\n%s", out)
	}
}

// TestCLINextLanesOverride: --lanes widens the lanes `next` considers for this call
// only; an unknown lane is exit 2 with candidates, and --json's reason names the
// matched lane.
func TestCLINextLanesOverride(t *testing.T) {
	initStore(t)
	ready := addTask(t, "ready one", "-s", "ready")
	back := addTask(t, "backlog one", "-s", "backlog")

	// Default: the backlog task is not surfaced.
	out, _ := run(t, "next", "--json")
	if strings.Contains(out, back) {
		t.Errorf("default next must not surface a backlog task:\n%s", out)
	}

	// --lanes backlog,ready surfaces both; the reason names the matched lane.
	out, code := run(t, "next", "--lanes", "backlog,ready", "--json")
	if code != 0 {
		t.Fatalf("next --lanes exit = %d:\n%s", code, out)
	}
	var rows []struct {
		ID     string `json:"id"`
		Reason struct {
			InNextLane string `json:"in_next_lane"`
		} `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse next --json: %v\n%s", err, out)
	}
	seen := map[string]string{}
	for _, r := range rows {
		seen[r.ID] = r.Reason.InNextLane
	}
	if seen[back] != "backlog" || seen[ready] != "ready" {
		t.Errorf("--lanes should surface both with reason.in_next_lane naming the lane: %v", seen)
	}

	// An unknown --lanes token is exit 2 with the configured lanes in candidates.
	fe, _ := runErr(t, "next", "--lanes", "nope")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("unknown --lanes token should be exit 2 + candidates, got %+v", fe)
	}
}
