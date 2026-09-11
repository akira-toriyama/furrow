package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// addedID reads the id out of `add`'s human line, refusing rather than panicking
// when the add did not happen — a test binary that panics takes the whole
// package down with it, and the go-bite gate then cannot judge any test in it.
func addedID(t *testing.T, out string, code int) string {
	t.Helper()
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		t.Fatalf("unexpected add output: %q", out)
	}
	return f[1]
}

// The close that advances a series has to SAY so. Without the line, the only
// evidence a new task exists is a later read — and the id of the occurrence you
// just created is exactly what a caller wants to act on.
func TestDonePrintsTheSeriesLine(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-03-01", "--repeat", "monthly")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	id := addedID(t, out, code)

	out, code = run(t, "done", id)
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	if !strings.Contains(out, "repeat: next due ") {
		t.Errorf("close said nothing about the series:\n%s", out)
	}
}

func TestDoneJSONCarriesTheRepeatKey(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-03-01", "--repeat", "monthly", "--json")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	var created struct {
		ID           string `json:"id"`
		Repeat       string `json:"repeat"`
		RepeatAnchor string `json:"repeat_anchor"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("add --json: %v\n%s", err, out)
	}
	if created.Repeat != "FREQ=MONTHLY" || created.RepeatAnchor == "" {
		t.Fatalf("add stored %q / %q, want the compiled rule and an anchor", created.Repeat, created.RepeatAnchor)
	}

	out, code = run(t, "done", created.ID, "--json")
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	var envs []struct {
		After struct {
			ID     string `json:"id"`
			Repeat string `json:"repeat"`
		} `json:"after"`
		Repeat *struct {
			Created   *string `json:"created"`
			Due       *string `json:"due"`
			Skipped   int     `json:"skipped"`
			Completed bool    `json:"completed"`
		} `json:"repeat"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil {
		t.Fatalf("done --json: %v\n%s", err, out)
	}
	if len(envs) != 1 {
		t.Fatalf("%d envelopes, want 1", len(envs))
	}
	e := envs[0]
	if e.Repeat == nil || e.Repeat.Created == nil || e.Repeat.Due == nil {
		t.Fatalf("envelope has no successor: %+v", e.Repeat)
	}
	if e.Repeat.Completed {
		t.Error("a live series reported completed")
	}
	if e.After.Repeat != "" {
		t.Errorf("the closed occurrence still carries the rule: %q", e.After.Repeat)
	}

	// The id the envelope names must be a real task carrying the series on.
	out, code = run(t, "show", *e.Repeat.Created, "--no-body", "--json")
	if code != 0 {
		t.Fatalf("show exit %d: %s", code, out)
	}
	if !strings.Contains(out, "FREQ=MONTHLY") {
		t.Errorf("the successor does not carry the rule:\n%s", out)
	}
}

// A day past 28 is legal, RFC-correct, and almost never what was meant — so it
// is said once, at bind time, on stderr.
func TestAddWarnsAboutASkippingDayOfMonth(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "月末", "--due", "2026-03-31", "--repeat", "monthly on 31")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if !strings.Contains(out, "does not exist in every month") {
		t.Errorf("no note about the skipped months:\n%s", out)
	}

	out, code = run(t, "add", "月末2", "--due", "2026-03-31", "--repeat", "monthly on last")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if strings.Contains(out, "does not exist in every month") {
		t.Errorf("`monthly on last` always lands; it must not be warned about:\n%s", out)
	}
}

func TestRepeatRefusalsAreExitTwo(t *testing.T) {
	initStore(t)
	cases := []struct {
		name string
		args []string
	}{
		{"--repeat with no --due", []string{"add", "水やり", "--repeat", "weekly"}},
		{"until and for together", []string{"add", "x", "--due", "2026-03-31", "--repeat", "monthly until 2026-12-31 for 3 times"}},
		{"an unknown spelling", []string{"add", "y", "--due", "2026-03-31", "--repeat", "fortnightly"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out, code := run(t, c.args...); code != 2 {
				t.Errorf("exit %d, want 2:\n%s", code, out)
			}
		})
	}
}

// has:repeat is "the live occurrences on this board": the rule sits on exactly
// one task of a series, so the query cannot return the closed ones.
func TestHasRepeatFindsOnlyTheLiveOccurrence(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-03-01", "--repeat", "monthly")
	id := addedID(t, out, code)
	if _, code := run(t, "done", id); code != 0 {
		t.Fatalf("done: %s", out)
	}

	out, code = run(t, "ls", "-q", "has:repeat", "-s", "", "--json")
	if code != 0 {
		t.Fatalf("ls exit %d: %s", code, out)
	}
	var tasks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if len(tasks) != 1 {
		t.Fatalf("has:repeat returned %d tasks, want 1 (the live occurrence)", len(tasks))
	}
	if tasks[0].ID == id {
		t.Error("has:repeat returned the CLOSED occurrence; the rule should have moved on")
	}
}

// Refusals the review found missing. Each was a silent accept before.
func TestRepeatRefusalsAddedAfterReview(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-10-01", "--repeat", "monthly")
	id := addedID(t, out, code)

	cases := []struct {
		name string
		args []string
	}{
		{"an empty --repeat, like an empty --due", []string{"add", "z", "--repeat", ""}},
		{"a rule on a task closed at birth", []string{"add", "z", "-s", "done", "--due", "2026-10-01", "--repeat", "monthly"}},
		{"rebinding and clearing at once", []string{"set", id, "--repeat", "weekly", "--clear-repeat"}},
		{"an empty closing note", []string{"done", id, "--note", ""}},
		{"a sub-daily rule", []string{"add", "z", "--due", "2026-10-01", "--repeat", "FREQ=MINUTELY"}},
		{"a raw rule that already ends itself, plus a count", []string{"add", "z", "--due", "2026-10-01", "--repeat", "FREQ=MONTHLY;COUNT=3 for 5 times"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out, code := run(t, c.args...); code != 2 {
				t.Errorf("exit %d, want 2:\n%s", code, out)
			}
		})
	}
}

// A raw RRULE line carries its own terminator; the compiler used to overwrite
// it with the zero value, turning a bounded series into an endless one.
func TestARawRuleKeepsItsOwnTerminator(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "3 回", "--due", "2026-10-01", "--repeat", "FREQ=MONTHLY;COUNT=3", "--json")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if !strings.Contains(out, "FREQ=MONTHLY;COUNT=3") {
		t.Errorf("the stored rule lost its COUNT:\n%s", out)
	}
}

// `set -s done` closes like `done` does, so it owes the same receipt.
func TestSetToDoneReportsTheSeries(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-10-01", "--repeat", "monthly")
	id := addedID(t, out, code)

	out, code = run(t, "set", id, "-s", "done")
	if code != 0 {
		t.Fatalf("set exit %d: %s", code, out)
	}
	if !strings.Contains(out, "repeat: next due ") {
		t.Errorf("`set -s done` advanced the series in silence:\n%s", out)
	}
}

// A close CONSUMES the rule, so the envelope must say the shard changed.
func TestChangedNamesTheRepeatFields(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "水やり", "--due", "2026-10-01", "--repeat", "monthly")
	id := addedID(t, out, code)

	out, code = run(t, "done", id, "--json")
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	var envs []struct {
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil {
		t.Fatalf("done --json: %v\n%s", err, out)
	}
	joined := strings.Join(envs[0].Changed, ",")
	if !strings.Contains(joined, "repeat") || !strings.Contains(joined, "repeat_anchor") {
		t.Errorf("changed = %v, want it to name repeat and repeat_anchor", envs[0].Changed)
	}
}
