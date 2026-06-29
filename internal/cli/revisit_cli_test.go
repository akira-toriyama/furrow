package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

type revisitRow struct {
	ID      string `json:"id"`
	Revisit []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"revisit"`
}

func TestCLIRevisitJSONReasons(t *testing.T) {
	initStore(t)
	id := addTask(t, "needs estimates", "-s", "ready") // fresh, no value/effort

	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Fatalf("revisit --json exit = %d:\n%s", code, out)
	}
	var rows []revisitRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("revisit --json should be an array with reasons, got %v:\n%s", err, out)
	}
	var got *revisitRow
	for i := range rows {
		if rows[i].ID == id {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatalf("unestimated task %s should surface for revisit:\n%s", id, out)
	}
	codes := map[string]bool{}
	for _, r := range got.Revisit {
		codes[r.Code] = true
	}
	if !codes[core.RevisitValueUnset] || !codes[core.RevisitEffortUnset] {
		t.Errorf("expected value_unset + effort_unset, got %+v", got.Revisit)
	}
}

func TestCLIRevisitEmptyExit0(t *testing.T) {
	initStore(t)
	// nothing to revisit is the healthy state: exit 0 (diverges from `next`),
	// and --json is [] not null.
	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Errorf("empty revisit should exit 0, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty revisit --json should be [], got %q", out)
	}
}

func TestCLIRevisitStaleDaysFlagParses(t *testing.T) {
	initStore(t)
	addTask(t, "estimated", "-s", "ready", "--value", "3", "--effort", "2")
	// estimated + fresh + no deps + stale disabled -> nothing surfaces.
	out, code := run(t, "--json", "revisit", "--stale-days", "0")
	if code != 0 {
		t.Fatalf("revisit --stale-days exit = %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("estimated fresh task should not surface with stale disabled, got %q", out)
	}
}
