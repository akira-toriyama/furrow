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
	if !strings.Contains(out, id+"  repeat: next due ") {
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
	out, se, code := runSplit(t, "add", "月末", "--due", "2026-03-31", "--repeat", "monthly on 31")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if !strings.Contains(se, "does not exist in every month") {
		t.Errorf("no note about the skipped months on stderr:\n%s", se)
	}

	out, se, code = runSplit(t, "add", "月末2", "--due", "2026-03-31", "--repeat", "monthly on last")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if strings.Contains(se, "does not exist in every month") {
		t.Errorf("`monthly on last` always lands; it must not be warned about:\n%s", se)
	}
}

// February 29 is the same defect with a four-year feedback loop: the operator
// spells the rule `yearly`, the successor is minted silently, and the next
// occurrence is 2032. So it is said at bind time too — with the date, and never
// with the monthly remedy, which is a rule of another frequency.
func TestAddWarnsAboutALeapDayRule(t *testing.T) {
	initStore(t)
	out, se, code := runSplit(t, "add", "うるう", "--due", "2028-02-29", "--repeat", "yearly")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if !strings.Contains(se, "February 29 exists only in leap years") {
		t.Errorf("no note about the skipped years on stderr:\n%s", se)
	}
	if !strings.Contains(se, "the next occurrence is 2032-02-29") {
		t.Errorf("the note does not name the date the rule really lands on:\n%s", se)
	}
	if strings.Contains(se, "on last") || strings.Contains(se, "every month") {
		t.Errorf("a yearly rule was handed the monthly remedy:\n%s", out)
	}

	// The other half of the same question: a yearly rule on any other day past
	// 28 names its month and lands every year, so it stays silent.
	out, code = run(t, "add", "1月末", "--due", "2026-01-31", "--repeat", "yearly")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if strings.Contains(out, "RFC 5545") {
		t.Errorf("`yearly` on January 31 lands every January; it must not be warned about:\n%s", out)
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
		{"a DTSTART on set", []string{"set", id, "--repeat", "FREQ=DAILY;DTSTART=20200101T000000Z"}},
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
	if !strings.Contains(out, id+"  repeat: next due ") {
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

// A write that BINDS a rule and CLOSES in one go hands the rule straight to the
// successor, so the returned task carries none — the note has to follow it, or
// it is silent exactly when a rule was just bound.
func TestTheBindNoteFollowsTheRuleToTheSuccessor(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "y", "--due", "2026-01-31")
	id := addedID(t, out, code)

	out, se, code := runSplit(t, "set", id, "-s", "done", "--repeat", "every 3 months")
	if code != 0 {
		t.Fatalf("set exit %d: %s", code, out)
	}
	if !strings.Contains(se, "does not exist in every month") {
		t.Errorf("no bind-time note when the rule was bound and closed at once:\n%s", se)
	}
	// The remedy must not prescribe a different frequency to someone who wrote
	// `every 3 months`.
	if strings.Contains(se, "`monthly on last`") {
		t.Errorf("the note prescribes a monthly rule for an every-3-months one:\n%s", out)
	}
}

// A close CREATES a task. A preview that reports it as a plain lane move hides
// that one is coming.
func TestApplyDryRunSaysItWouldCreateTheNextOccurrence(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "x", "--due", "2026-10-01", "--repeat", "monthly")
	id := addedID(t, out, code)

	out, code = runIn(t, "SetStatus-task: "+id+" done\n", "apply", "--on", "merge", "--dry-run")
	if code != 0 {
		t.Fatalf("apply exit %d: %s", code, out)
	}
	if !strings.Contains(out, "would also create the next occurrence") {
		t.Errorf("the preview reported a close as a plain lane move:\n%s", out)
	}
}

// A DTSTART used to be taken at exit 0 and then dropped by the renderer: the
// task was created carrying a rule the operator did not type. Both spellings are
// refused as a validation error now, and nothing is created.
func TestADtstartIsRefusedAtTheDoor(t *testing.T) {
	initStore(t)
	for _, spec := range []string{
		"FREQ=DAILY;DTSTART=20200101T000000Z",
		"DTSTART:20200101T000000Z\nRRULE:FREQ=DAILY",
	} {
		fe, out := runErr(t, "add", "watering", "--due", "2026-10-01", "--repeat", spec)
		if fe == nil {
			t.Fatalf("--repeat %q was accepted:\n%s", spec, out)
		}
		if fe.Kind != "validation" || !strings.Contains(fe.Msg, "DTSTART") {
			t.Errorf("--repeat %q failed as %+v, want a validation error naming the DTSTART", spec, fe)
		}
	}
	out, code := run(t, "ls", "--json")
	if code != 0 {
		t.Fatalf("ls exit %d: %s", code, out)
	}
	// Not a substring check: every task row carries empty arrays of its own.
	var tasks []struct{}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if len(tasks) != 0 {
		t.Errorf("a refused add left %d task(s) behind:\n%s", len(tasks), out)
	}
}

// assertRepeatTag checks every row of a human listing: the ids in `tagged` must
// carry the tag, the ids in `plain` must not. Both halves matter — a tag that
// leaked onto every row is as useless as a missing one, and only the pair can
// tell them apart.
func assertRepeatTag(t *testing.T, out string, tagged, plain []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		for _, id := range tagged {
			if strings.Contains(line, id) {
				seen[id] = true
				if !strings.Contains(line, "repeats") {
					t.Errorf("a repeating row carries no tag:\n%s", line)
				}
			}
		}
		for _, id := range plain {
			if strings.Contains(line, id) && strings.Contains(line, "repeats") {
				t.Errorf("a one-off row carries a repeat tag:\n%s", line)
			}
		}
	}
	for _, id := range tagged {
		if !seen[id] {
			t.Errorf("%s never appeared in the listing:\n%s", id, out)
		}
	}
}

// A row whose close MINTS the next occurrence must not render like a one-off.
// The date tag beside it says nothing about recurrence: two rows promised for
// the same day are the same row until one of them is marked.
func TestCLILsShowsRepeat(t *testing.T) {
	initStore(t)
	series := addTask(t, "water the plants", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30), "--repeat", "daily")
	dated := addTask(t, "ship the thing", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30))
	bare := addTask(t, "no dates at all", "-s", "ready", "-r", "o/r")

	out, code := run(t, "ls", "-r", "o/r")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	assertRepeatTag(t, out, []string{series}, []string{dated, bare})
}

// `--tree` is the same matched rows regrouped, so it must not be the one view
// where a series disappears.
func TestCLITreeShowsRepeat(t *testing.T) {
	initStore(t)
	epic, code := run(t, "--json", "epic", "add", "ops", "-r", "o/r")
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, epic)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(epic), &e); err != nil {
		t.Fatalf("parse epic add: %v\n%s", err, epic)
	}
	series := addTask(t, "water the plants", "-s", "ready", "-r", "o/r", "-e", e.ID, "--due", dayOffset(t, 30), "--repeat", "daily")
	dated := addTask(t, "ship the thing", "-s", "ready", "-r", "o/r", "-e", e.ID, "--due", dayOffset(t, 30))

	out, code := run(t, "ls", "-r", "o/r", "--tree")
	if code != 0 {
		t.Fatalf("ls --tree exit = %d:\n%s", code, out)
	}
	assertRepeatTag(t, out, []string{series}, []string{dated})

	// The box's own detail view lists the same members and is a place a close is
	// launched from just as much as the tree is.
	out, code = run(t, "epic", "show", e.ID)
	if code != 0 {
		t.Fatalf("epic show exit = %d:\n%s", code, out)
	}
	assertRepeatTag(t, out, []string{series}, []string{dated})
}

// `next` and `revisit` share one table, and it is the table a session picks work
// off: the row it hands you is the row whose close writes another task.
func TestCLINextAndRevisitShowRepeat(t *testing.T) {
	initStore(t)
	series := addTask(t, "water the plants", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30), "--repeat", "daily")
	dated := addTask(t, "ship the thing", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30))

	for _, cmd := range []string{"next", "revisit"} {
		out, code := run(t, cmd, "-r", "o/r")
		if code != 0 {
			t.Fatalf("%s exit = %d:\n%s", cmd, code, out)
		}
		assertRepeatTag(t, out, []string{series}, []string{dated})
	}
}

// brief tags ALL THREE task bands, which is wider than the due tag rides: the
// due band is the only surface a date is guaranteed, while a rule has none —
// a chore promised for next month reaches the session only as a `next` row.
func TestCLIBriefBandsShowRepeat(t *testing.T) {
	freezeClock(t)
	initStore(t)
	// The blocker sits in no band of its own: it is named only by the ← edge of
	// the rows that wait on it, which the per-row assert must not read as a row.
	blocker := addTask(t, "upstream answer", "-s", "waiting", "-r", "o/r")
	lateSeries := addTask(t, "weekly review", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, -1), "--repeat", "weekly")
	lateOnce := addTask(t, "one-off overdue", "-s", "waiting", "-r", "o/r", "--due", dayOffset(t, -1))
	nextSeries := addTask(t, "water the plants", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30), "--repeat", "daily")
	blockedSeries := addTask(t, "monthly rotation", "-s", "ready", "-r", "o/r", "--due", dayOffset(t, 30), "--repeat", "monthly", "--dep", blocker)
	blockedOnce := addTask(t, "ship the thing", "-s", "ready", "-r", "o/r", "--dep", blocker)

	out, code := run(t, "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	for _, band := range []string{"due (", "next (", "blocked ("} {
		if !strings.Contains(out, band) {
			t.Fatalf("brief printed no %q band:\n%s", band, out)
		}
	}
	assertRepeatTag(t, out, []string{lateSeries, nextSeries, blockedSeries}, []string{lateOnce, blockedOnce})
}

// The rule sits on exactly one task of a series, so the tag has to move with it:
// a closed occurrence advertising a mint that already happened would be the same
// defect in the other direction.
func TestRepeatTagFollowsTheLiveOccurrence(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "water the plants", "--due", dayOffset(t, 30), "--repeat", "daily")
	closed := addedID(t, out, code)
	if out, code := run(t, "done", closed); code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}

	out, code = run(t, "ls", "-s", "")
	if code != 0 {
		t.Fatalf("ls exit = %d:\n%s", code, out)
	}
	tagged := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "repeats") {
			continue
		}
		tagged++
		if strings.Contains(line, closed) {
			t.Errorf("the closed occurrence still advertises the series:\n%s", line)
		}
	}
	if tagged != 1 {
		t.Errorf("%d rows carry the tag, want exactly the live successor:\n%s", tagged, out)
	}
}

// A batch close prints one verb line per task but only as many receipts as
// there were series, so a receipt has to name the occurrence it belongs to.
// Two spent series otherwise render byte-identical lines and the mapping is
// unrecoverable from the text.
func TestABatchCloseNamesTheOccurrenceEachReceiptBelongsTo(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "alpha", "--due", "2026-10-01", "--repeat", "daily for 2 times")
	a := addedID(t, out, code)
	out, code = run(t, "add", "beta", "--due", "2026-10-01", "--repeat", "daily for 2 times")
	b := addedID(t, out, code)

	out, code = run(t, "done", a, b)
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	for _, id := range []string{a, b} {
		if !strings.Contains(out, id+"  repeat: next due ") {
			t.Errorf("no receipt naming %s:\n%s", id, out)
		}
	}

	// The successors carry the last slot of each series, so closing both in one
	// batch is the case where the two lines say the same words.
	last := liveRepeatIDs(t)
	if len(last) != 2 {
		t.Fatalf("want 2 live occurrences, got %v", last)
	}
	out, code = run(t, append([]string{"done"}, last...)...)
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	for _, id := range last {
		if !strings.Contains(out, id+"  repeat: series complete") {
			t.Errorf("no receipt naming %s:\n%s", id, out)
		}
	}
}

// liveRepeatIDs reads the ids of the occurrences that still carry a rule.
func liveRepeatIDs(t *testing.T) []string {
	t.Helper()
	out, code := run(t, "ls", "-q", "has:repeat", "--json")
	if code != 0 {
		t.Fatalf("ls exit %d: %s", code, out)
	}
	var tasks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

// The mixed batch is where position — the only mapping an unprefixed receipt
// offered — pointed at the WRONG task: one trailing line under a block whose
// first entry is the task that does not repeat.
func TestAMixedBatchCloseNamesTheRepeatingTask(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "plain")
	plain := addedID(t, out, code)
	out, code = run(t, "add", "chore", "--due", "2026-10-01", "--repeat", "daily")
	rep := addedID(t, out, code)

	out, code = run(t, "done", plain, rep)
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	if !strings.Contains(out, rep+"  repeat: next due ") {
		t.Errorf("the receipt does not name the repeating task %s:\n%s", rep, out)
	}
	if strings.Contains(out, plain+"  repeat:") {
		t.Errorf("a receipt was attached to the task that does not repeat:\n%s", out)
	}
}

// An anchor the rule does not land on is legal and RFC 5545 §3.8.5.3 leaves it
// undefined; furrow's answer is that the anchor stands OUTSIDE the series, so
// `for 3 times` hands out four tasks. The operator cannot see that from the
// shard or from the count, so it is said once, at bind time, on stderr — and
// `set --repeat` binds too.
func TestBindingAnOffLatticeAnchorSaysSo(t *testing.T) {
	initStore(t)
	// 2026-09-18 is a Friday; the rule lands on Mondays.
	out, se, code := runSplit(t, "add", "週次", "--due", "2026-09-18", "--repeat", "weekly on mon for 3 times")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if !strings.Contains(se, "not a date this rule lands on") {
		t.Errorf("no note about the off-lattice anchor on stderr:\n%s", se)
	}
	if !strings.Contains(se, "2026-09-21") {
		t.Errorf("the note does not name the rule's own first date:\n%s", se)
	}

	out, se, code = runSplit(t, "add", "週次2", "--due", "2026-09-21", "--repeat", "weekly on mon for 3 times")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, out)
	}
	if strings.Contains(se, "not a date this rule lands on") {
		t.Errorf("an anchor ON the lattice was warned about:\n%s", se)
	}

	out, code = run(t, "add", "後付け", "--due", "2026-09-18")
	id := addedID(t, out, code)
	out, se, code = runSplit(t, "set", id, "--repeat", "weekly on mon")
	if code != 0 {
		t.Fatalf("set exit %d: %s", code, out)
	}
	if !strings.Contains(se, "not a date this rule lands on") {
		t.Errorf("`set --repeat` bound an off-lattice anchor silently:\n%s", se)
	}
}

// The LAST occurrence of a bounded series carries its rule like every other
// one, so a preview that tested the field alone promised a successor the apply
// then did not create — the preview stating the opposite of the write.
func TestApplyDryRunSaysWhenTheCloseWouldEndTheSeries(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "x", "--due", "2026-10-01", "--repeat", "daily for 2 times")
	first := addedID(t, out, code)

	// Closing the first occurrence mints the second, which is the last.
	out, code = run(t, "done", first, "--json")
	if code != 0 {
		t.Fatalf("done exit %d: %s", code, out)
	}
	last := repeatCreated(t, out)

	out, code = runIn(t, "SetStatus-task: "+last+" done\n", "apply", "--on", "merge", "--dry-run")
	if code != 0 {
		t.Fatalf("apply exit %d: %s", code, out)
	}
	if strings.Contains(out, "would also create the next occurrence") {
		t.Errorf("the preview promised an occurrence the series has no room for:\n%s", out)
	}
	if !strings.Contains(out, "would complete the series") {
		t.Errorf("the preview did not say the close would end the series:\n%s", out)
	}

	// --json says the same thing, in the shape a machine branches on.
	out, code = runIn(t, "SetStatus-task: "+last+" done\n", "apply", "--on", "merge", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("apply --json exit %d: %s", code, out)
	}
	var res struct {
		Outcomes []struct {
			WillRepeat   bool `json:"will_repeat"`
			WillComplete bool `json:"will_complete"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("apply --json: %v\n%s", err, out)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].WillRepeat || !res.Outcomes[0].WillComplete {
		t.Errorf("outcomes = %+v, want one {will_repeat:false, will_complete:true}", res.Outcomes)
	}

	// And the real apply agrees with its own preview.
	out, code = runIn(t, "SetStatus-task: "+last+" done\n", "apply", "--on", "merge")
	if code != 0 {
		t.Fatalf("apply exit %d: %s", code, out)
	}
	if !strings.Contains(out, "series complete") {
		t.Errorf("the apply did not report the series ending:\n%s", out)
	}
}

// repeatCreated is the successor id out of a `done --json` envelope.
func repeatCreated(t *testing.T, out string) string {
	t.Helper()
	var envs []struct {
		Repeat struct {
			Created string `json:"created"`
		} `json:"repeat"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil {
		t.Fatalf("done --json: %v\n%s", err, out)
	}
	if len(envs) != 1 || envs[0].Repeat.Created == "" {
		t.Fatalf("no successor in the done envelope:\n%s", out)
	}
	return envs[0].Repeat.Created
}

// The preview of a write that MINTS tasks is the row a close is launched from —
// the case repeatTag exists for. Rendered without the shared row tags, a
// repeating match and a one-off match were the same line, and `--yes` then
// wrote a task the gate never named.
func TestASelectionPreviewTagsTheRowsThatWillMintTasks(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "water the plants", "--due", "2026-10-01", "--repeat", "weekly")
	repeating := addedID(t, out, code)
	out, code = run(t, "add", "ship the thing", "--due", "2026-10-01")
	oneOff := addedID(t, out, code)

	for _, args := range [][]string{
		{"done", "-q", "has:due"},
		{"move", "-q", "has:due", "ready"},
		{"set", "-q", "has:due", "--value", "3"},
	} {
		out, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v exit %d: %s", args, code, out)
		}
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.Contains(line, repeating):
				if !strings.Contains(line, "repeats") {
					t.Errorf("%v previewed the repeating row untagged: %q", args, line)
				}
				if !strings.Contains(line, "due 2026-10-01") {
					t.Errorf("%v previewed the repeating row with no due: %q", args, line)
				}
			case strings.Contains(line, oneOff):
				if strings.Contains(line, "repeats") {
					t.Errorf("%v tagged a one-off row as repeating: %q", args, line)
				}
			}
		}
	}
}
