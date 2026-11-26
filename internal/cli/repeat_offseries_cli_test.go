package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// A clock typed onto a whole-day series moves this occurrence and nothing
// else, and the write is the only moment that can say so cheaply: the row then
// reads `due … 11:00  repeats`, and the close that hands back 23:59 arrives a
// cycle later looking like a defect (t-6n5v). The note is two-sided like the
// bind-time ones — stderr for a human, `repeat_warnings` for a front-end that
// reads only stdout on exit 0.
func TestSetDueOffTheSeriesSaysSoOnBothChannels(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "朝の点検", "--due", "2099-03-02", "--repeat", "daily")
	id := addedID(t, out, code)
	const said = "repeats, and --due alone moves THIS occurrence only"
	const remedy = "`furrow set %s --due 2099-03-02T11:00 --repeat 'FREQ=DAILY'`"

	stdout, se := mustSplit(t, "set", id, "--due", "2099-03-02T11:00")
	if !strings.Contains(se, "note: "+id+" "+said) {
		t.Errorf("stderr did not say the series stood still:\n%s", se)
	}
	if want := strings.Replace(remedy, "%s", id, 1); !strings.Contains(se, want) {
		t.Errorf("stderr did not print the write that moves the series (%s):\n%s", want, se)
	}
	if strings.Contains(stdout, said) {
		t.Errorf("the note leaked onto stdout:\n%s", stdout)
	}

	// The same write again changes nothing and still owes the same answer.
	stdout, se = mustSplit(t, "set", id, "--due", "2099-03-02T11:00", "--json")
	var envs []struct {
		Changed        []string `json:"changed"`
		RepeatWarnings []string `json:"repeat_warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &envs); err != nil {
		t.Fatalf("set --json: %v\n%s", err, stdout)
	}
	if len(envs) != 1 || len(envs[0].RepeatWarnings) != 1 || !strings.Contains(envs[0].RepeatWarnings[0], said) {
		t.Fatalf("set --json did not carry the note:\n%s", stdout)
	}
	// The envelope carries the bare message; `note: ` is stderr's framing of it.
	if w := envs[0].RepeatWarnings[0]; strings.HasPrefix(w, "note:") || !strings.Contains(se, "note: "+w+"\n") {
		t.Errorf("want the bare message in the envelope and `note: <it>` on stderr:\nenvelope: %q\nstderr: %s", w, se)
	}

	// The offsets are deltas: silent, with no key rather than [] or null.
	for _, args := range [][]string{
		{"set", id, "--due", "+1d", "--json"},
		{"set", id, "--due-shift", "+1d", "--json"},
	} {
		stdout, se = mustSplit(t, args...)
		if strings.Contains(se, said) || strings.Contains(stdout, "repeat_warnings") {
			t.Errorf("%v spoke about the series:\nstdout: %s\nstderr: %s", args, stdout, se)
		}
	}

	// The write the note printed moves the series and is silent about itself.
	stdout, se = mustSplit(t, "set", id, "--due", "2099-03-02T11:00", "--repeat", "FREQ=DAILY", "--json")
	if strings.Contains(se, said) || strings.Contains(stdout, "repeat_warnings") {
		t.Errorf("the re-anchoring write was warned about:\nstdout: %s\nstderr: %s", stdout, se)
	}
	if !strings.Contains(stdout, `"repeat_anchor"`) {
		t.Fatalf("set --json: no repeat_anchor in the envelope:\n%s", stdout)
	}
	var moved []struct {
		After struct {
			Due          string `json:"due"`
			RepeatAnchor string `json:"repeat_anchor"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(stdout), &moved); err != nil {
		t.Fatalf("set --json: %v\n%s", err, stdout)
	}
	if len(moved) != 1 || moved[0].After.Due == "" || moved[0].After.Due != moved[0].After.RepeatAnchor {
		t.Errorf("after the printed write the anchor is %q and the due %q — the series did not move", moved[0].After.RepeatAnchor, moved[0].After.Due)
	}

	// A close that re-dates in the same write settles the occurrence it moved;
	// the one it mints was dated by the rule, so there is no series left to
	// speak about — only the receipt.
	stdout, se = mustSplit(t, "set", id, "-s", "done", "--due", "2099-03-02T08:00", "--json")
	if strings.Contains(se, said) || strings.Contains(stdout, "repeat_warnings") {
		t.Errorf("a close that re-dated was warned about:\nstdout: %s\nstderr: %s", stdout, se)
	}
	if !strings.Contains(stdout, `"created"`) {
		t.Errorf("the close minted no occurrence:\n%s", stdout)
	}
}

// Several ids re-dated at once each get their own note — the remedy names the
// task and its own rule — while the ones that do not repeat stay silent.
func TestSetDueOffTheSeriesNotesEachRepeatingTask(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "毎日", "--due", "2099-03-02", "--repeat", "daily")
	daily := addedID(t, out, code)
	out, code = run(t, "add", "毎週", "--due", "2099-03-02", "--repeat", "weekly")
	weekly := addedID(t, out, code)
	out, code = run(t, "add", "一回", "--due", "2099-03-02")
	once := addedID(t, out, code)

	stdout, se := mustSplit(t, "set", daily, weekly, once, "--due", "2099-03-02T09:00", "--json")
	var envs []struct {
		After struct {
			ID string `json:"id"`
		} `json:"after"`
		RepeatWarnings []string `json:"repeat_warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &envs); err != nil {
		t.Fatalf("set --json: %v\n%s", err, stdout)
	}
	got := map[string][]string{}
	for _, e := range envs {
		got[e.After.ID] = e.RepeatWarnings
	}
	for id, rule := range map[string]string{daily: "FREQ=DAILY", weekly: "FREQ=WEEKLY"} {
		if len(got[id]) != 1 || !strings.Contains(got[id][0], "set "+id+" --due 2099-03-02T09:00 --repeat '"+rule+"'") {
			t.Errorf("%s: repeat_warnings = %q, want its own remedy with %s", id, got[id], rule)
		}
	}
	if len(got[once]) != 0 {
		t.Errorf("the one-off task carried %q", got[once])
	}
	if n := strings.Count(se, "moves THIS occurrence only"); n != 2 {
		t.Errorf("stderr said it %d times, want once per repeating task:\n%s", n, se)
	}
}

// A date the series lands on further on reads as a deadline the series keeps
// (t-fq40). The count of the occurrences passed needs the due each task carried
// BEFORE the write — the shard no longer holds it — so two tasks moved to one
// date in one write each get their own, and the same write again passes
// nothing and says nothing.
func TestSetDueFurtherOnTheSeriesSaysTheOccurrencesBetweenAreNeverCreated(t *testing.T) {
	initStore(t)
	out, code := run(t, "add", "毎日", "--due", "2099-03-02", "--repeat", "daily")
	first := addedID(t, out, code)
	out, code = run(t, "add", "毎日も", "--due", "2099-03-03", "--repeat", "daily")
	second := addedID(t, out, code)

	stdout, se := mustSplit(t, "set", first, second, "--due", "2099-03-05", "--json")
	var envs []struct {
		After struct {
			ID string `json:"id"`
		} `json:"after"`
		RepeatWarnings []string `json:"repeat_warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &envs); err != nil {
		t.Fatalf("set --json: %v\n%s", err, stdout)
	}
	got := map[string][]string{}
	for _, e := range envs {
		got[e.After.ID] = e.RepeatWarnings
	}
	for id, between := range map[string]string{
		first:  "the 2 occurrences between its old date and this one are never created",
		second: "the occurrence between its old date and this one is never created",
	} {
		if len(got[id]) != 1 || !strings.Contains(got[id][0], between) || !strings.Contains(got[id][0], "belongs on another task's due") {
			t.Errorf("%s: repeat_warnings = %q, want %q and where a deadline belongs", id, got[id], between)
		}
		if !strings.Contains(se, "note: "+got[id][0]+"\n") {
			t.Errorf("%s: stderr did not carry the envelope's note:\n%s", id, se)
		}
	}

	stdout, se = mustSplit(t, "set", first, "--due", "2099-03-05", "--json")
	if strings.Contains(se, "never created") || strings.Contains(stdout, "repeat_warnings") {
		t.Errorf("the same write again spoke about the series:\nstdout: %s\nstderr: %s", stdout, se)
	}
}
