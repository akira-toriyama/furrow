package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

type revisitBoxRow struct {
	ID      string `json:"id"`
	Revisit []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"revisit"`
}

// `revisit --boxes` is the read the sync/brief nudge names for the box counts:
// the epics with a signal, as an array of boxes carrying the same `revisit`
// array a task row does — and the plain task read stays an array of tasks
// (ridge decodes it as one), so a box never shows up there.
func TestCLIRevisitBoxesListsFlaggedEpics(t *testing.T) {
	initStore(t)
	stuck := addEpic(t, "a box with nothing actionable")
	addTask(t, "parked member", "-s", "backlog", "-e", stuck)
	quiet := addEpic(t, "a box that is fine")
	addTask(t, "ready member", "-s", "ready", "-e", quiet)

	out, code := run(t, "--json", "revisit", "--boxes")
	if code != 0 {
		t.Fatalf("revisit --boxes --json exit = %d:\n%s", code, out)
	}
	var rows []revisitBoxRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("revisit --boxes --json should be an array of boxes, got %v:\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].ID != stuck {
		t.Fatalf("want exactly the stuck box %s, got %+v", stuck, rows)
	}
	if len(rows[0].Revisit) != 1 || rows[0].Revisit[0].Code != core.RevisitEpicStuck {
		t.Errorf("want one epic_stuck reason, got %+v", rows[0].Revisit)
	}

	hout, hcode := run(t, "revisit", "--boxes")
	if hcode != 0 {
		t.Fatalf("revisit --boxes exit = %d:\n%s", hcode, hout)
	}
	if !strings.Contains(hout, stuck) || !strings.Contains(hout, "← "+core.RevisitEpicStuck+": ") {
		t.Errorf("the human row should name the box and end with its signal:\n%s", hout)
	}
	if strings.Contains(hout, quiet) {
		t.Errorf("a box with no signal must not be listed:\n%s", hout)
	}

	tout, tcode := run(t, "--json", "revisit")
	if tcode != 0 {
		t.Fatalf("revisit --json exit = %d:\n%s", tcode, tout)
	}
	var trows []revisitRow
	if err := json.Unmarshal([]byte(tout), &trows); err != nil {
		t.Fatalf("revisit --json should stay an array of tasks: %v\n%s", err, tout)
	}
	for _, r := range trows {
		if strings.HasPrefix(r.ID, "e-") {
			t.Errorf("the task read must stay tasks-only; a box row leaked into it: %+v", r)
		}
	}
}

// A clean board lists no box: [] in --json, exit 0 — the same healthy-empty
// contract as the task read.
func TestCLIRevisitBoxesEmptyExit0(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "revisit", "--boxes")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("empty revisit --boxes --json = %q (exit %d), want [] exit 0", out, code)
	}
	hout, hcode := run(t, "revisit", "--boxes")
	if hcode != 0 || strings.TrimSpace(hout) != "(no boxes)" {
		t.Errorf("empty human revisit --boxes = %q (exit %d), want (no boxes) exit 0", hout, hcode)
	}
}

// The task-shaped filters have no meaning on a box row, so combining them
// with --boxes is a usage error rather than a silently ignored flag.
func TestCLIRevisitBoxesRefusesTaskFilters(t *testing.T) {
	initStore(t)
	for _, args := range [][]string{
		{"revisit", "--boxes", "-q", "label:cli"},
		{"revisit", "--boxes", "-l", "cli"},
		{"revisit", "--boxes", "-e", "e-none"},
		{"revisit", "--boxes", "-e", ""}, // presence refuses, not the value
	} {
		fe, _ := runErr(t, append([]string{"--json"}, args...)...)
		if fe == nil || fe.Code != core.CodeValidation || fe.Kind != core.KindValidation || !strings.Contains(fe.Msg, "--boxes") {
			t.Errorf("%v: want a validation error naming --boxes, got %+v", args, fe)
		}
	}
}
