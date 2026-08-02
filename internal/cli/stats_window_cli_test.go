package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `stats --since/--until` adds the window flow section — counts AND ids for
// created/closed inside the bounds — while a windowless stats keeps the classic
// object with no `window` key at all (absent, not null-ish).
func TestCLIStatsWindow(t *testing.T) {
	initStore(t)
	id := addTask(t, "one", "-s", "ready")
	done := addTask(t, "two", "-s", "ready")
	if _, code := run(t, "done", done); code != 0 {
		t.Fatalf("done exit = %d", code)
	}

	out, code := run(t, "--json", "stats", "--since", "2000-01-01")
	if code != 0 {
		t.Fatalf("stats --since exit = %d:\n%s", code, out)
	}
	var v struct {
		Window *struct {
			Since      string   `json:"since"`
			Created    int      `json:"created"`
			Closed     int      `json:"closed"`
			CreatedIDs []string `json:"created_ids"`
			ClosedIDs  []string `json:"closed_ids"`
		} `json:"window"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if v.Window == nil {
		t.Fatal("a --since stats must carry the window section")
	}
	if v.Window.Created != 2 || len(v.Window.CreatedIDs) != 2 {
		t.Errorf("created = %d %v, want both tasks", v.Window.Created, v.Window.CreatedIDs)
	}
	if v.Window.Closed != 1 || len(v.Window.ClosedIDs) != 1 || v.Window.ClosedIDs[0] != done {
		t.Errorf("closed = %d %v, want [%s]", v.Window.Closed, v.Window.ClosedIDs, done)
	}
	_ = id

	// A window that nothing falls into still emits the section, with empty
	// arrays ([] not null) and zero counts.
	out, code = run(t, "--json", "stats", "--since", "2000-01-01", "--until", "2000-01-02")
	if code != 0 {
		t.Fatalf("empty-window exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"created_ids": []`) || !strings.Contains(out, `"closed_ids": []`) {
		t.Errorf("empty flow must be [] not null:\n%s", out)
	}

	// No window flags -> no window key.
	out, _ = run(t, "--json", "stats")
	if strings.Contains(out, `"window"`) {
		t.Errorf("windowless stats must not carry a window key:\n%s", out)
	}

	// A malformed date is exit 2 (validation), same contract as `ls`.
	fe, _ := runErr(t, "stats", "--since", "notadate")
	if fe == nil || fe.Code != core.CodeValidation || fe.Kind != core.KindValidation {
		t.Fatalf("bad --since want kind validation exit 2, got %+v", fe)
	}
}
