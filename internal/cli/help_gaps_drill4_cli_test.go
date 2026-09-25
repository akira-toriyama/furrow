package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// t-cgpc (fourth drill run, reschedule #2 / #6 / #8 / #21): four facts the
// help did not state. Each case measures the fact first, then asserts the
// help now states it.

// reschedule #6: a bare datetime is read in the board's [due].timezone; the
// stored instant is UTC and `ls --json` / `show --json` print it that way.
func TestSetHelpNamesTheZoneABareDueIsReadIn(t *testing.T) {
	initStore(t)
	mustRun(t, "config", "set", "due.timezone", "Asia/Tokyo")
	id := addTask(t, "load-in", "--due", "2026-03-02T16:00")
	var rows []struct {
		Due string `json:"due"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "show", id)), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("show: %v", err)
	}
	if rows[0].Due != "2026-03-02T07:00:00Z" {
		t.Errorf("due = %q, want 16:00 JST stored as 07:00Z", rows[0].Due)
	}
	if help, _ := run(t, "set", "--help"); !strings.Contains(help, "read in the board's [due].timezone") {
		t.Errorf("set --help does not say which zone a bare --due is read in")
	}
}

// reschedule #8: --repeat alone re-binds the rule to the due the task already
// carries — the anchor stays, the rule changes.
func TestSetRepeatAloneReanchorsToTheExistingDue(t *testing.T) {
	initStore(t)
	id := addTask(t, "tally", "--due", "2026-03-02", "--repeat", "weekly")
	mustRun(t, "set", id, "--repeat", "every 2 weeks")
	var rows []struct {
		Repeat       string `json:"repeat"`
		RepeatAnchor string `json:"repeat_anchor"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "show", id)), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(rows[0].Repeat, "INTERVAL=2") || !strings.HasPrefix(rows[0].RepeatAnchor, "2026-03-02") {
		t.Errorf("repeat %q anchor %q, want the new rule bound to the existing due", rows[0].Repeat, rows[0].RepeatAnchor)
	}
	if help, _ := run(t, "set", "--help"); !strings.Contains(help, "--repeat alone re-anchors the rule to the due the task already carries") {
		t.Errorf("set --help does not say what --repeat alone does")
	}
}

// reschedule #2: with no -s, ls lists every lane — done and the terminal
// lanes included.
func TestLsHelpSaysTheDefaultSpansEveryLane(t *testing.T) {
	initStore(t)
	shipped := addTask(t, "shipped")
	mustRun(t, "done", shipped)
	addTask(t, "parked", "-s", "icebox")
	var rows []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "ls")), &rows); err != nil {
		t.Fatal(err)
	}
	lanes := map[string]bool{}
	for _, r := range rows {
		lanes[r.Status] = true
	}
	if len(rows) != 2 || !lanes["done"] || !lanes["icebox"] {
		t.Errorf("bare ls = %v, want the done row and the icebox row", rows)
	}
	if help, _ := run(t, "ls", "--help"); !strings.Contains(help, "done and the terminal lanes included") {
		t.Errorf("ls --help does not say the default spans every lane")
	}
}

// reschedule #21: `lint --json` is ONE top-level array of findings on stdout,
// with the exit-2 envelope on stderr beside it when an error is among them.
func TestLintHelpNamesTheJSONShape(t *testing.T) {
	initStore(t)
	addTask(t, "late", "--due", "2000-01-01")
	fe, stdout, _ := execCLI(t, "", "--json", "lint")
	var findings []struct {
		Severity string `json:"severity"`
		Code     string `json:"code"`
		ID       string `json:"id"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(stdout), &findings); err != nil {
		t.Fatalf("stdout is not one array: %v\n%s", err, stdout)
	}
	overdue := 0
	for _, f := range findings {
		if f.Code == "due-overdue" && f.Severity == "error" && f.ID != "" && f.Message != "" {
			overdue++
		}
	}
	// The harness captures the envelope as the structured error the binary
	// prints to stderr; stdout must still be the whole array.
	if fe == nil || exitOf(fe) != 2 || overdue != 1 {
		t.Errorf("error %+v, %d due-overdue finding(s) — want exit 2 and one finding in the stdout array", fe, overdue)
	}
	if help, _ := run(t, "lint", "--help"); !strings.Contains(help, "ONE top-level array of {severity, code, id, message}") {
		t.Errorf("lint --help does not name the --json shape")
	}
}
