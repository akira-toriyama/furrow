package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The human tables pad each column to its longest cell, never to a guessed
// constant: brief's lane column is as wide as the board's longest lane, lint's
// code and id columns as wide as the longest code and id it lists. A fixed
// `%-16s` had nine of fifty-eight lint codes spilling past it, doctor's `%-22s`
// two, and brief's `%-12s` left every lane a trailing blank (t-62c1).
func TestHumanColumnsPadToTheirLongestCell(t *testing.T) {
	initStore(t)
	id := addTask(t, "ready a", "-s", "ready")
	brief := mustRun(t, "brief")
	// The default lane set's longest is in-progress (11): the status column
	// is exactly that wide, then the single space before the title.
	if want := fmt.Sprintf("  ★ %s  %-*s %s", id, len("in-progress"), "ready", "ready a"); !strings.Contains(brief, want+"\n") {
		t.Errorf("brief pads the lane to the longest configured lane; want %q in:\n%s", want, brief)
	}

	// A typo'd ignore entry is a config-clamp warn under id "config"; whatever
	// else the board reports, the row is padded to the longest code and id
	// among the findings actually printed.
	writeConfig(t, "[lint]\nignore_codes = [\"totally-made-up\"]\n")
	jsonOut, _ := run(t, "--json", "lint")
	var ps []struct {
		Severity string `json:"severity"`
		Code     string `json:"code"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &ps); err != nil || len(ps) == 0 {
		t.Fatalf("lint --json: %v\n%s", err, jsonOut)
	}
	wCode, wID := 0, 0
	for _, p := range ps {
		wCode, wID = max(wCode, len(p.Code)), max(wID, len(p.ID))
	}
	human, _ := run(t, "lint")
	for _, p := range ps {
		if row := fmt.Sprintf("%-5s  %-*s  %-*s  ", p.Severity, wCode, p.Code, wID, p.ID); !strings.Contains(human, row) {
			t.Errorf("lint pads code to %d and id to %d; want %q in:\n%s", wCode, wID, row, human)
		}
	}
}
