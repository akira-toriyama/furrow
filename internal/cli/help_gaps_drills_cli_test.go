package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// t-557z (reschedule drill #7): `set --due X --repeat <rule>` in one write
// re-anchors the series at X — measured here so the help can say it.
func TestSetDueWithRepeatReanchorsTheSeries(t *testing.T) {
	initStore(t)
	id := addTask(t, "weekly tally", "--due", "2026-03-02", "--repeat", "weekly")
	out, code := run(t, "--json", "set", id, "--due", "2026-03-09", "--repeat", "weekly")
	if code != 0 {
		t.Fatalf("set exit %d:\n%s", code, out)
	}
	show, _ := run(t, "--json", "show", id)
	var tasks []struct {
		Due          string `json:"due"`
		RepeatAnchor string `json:"repeat_anchor"`
	}
	if err := json.Unmarshal([]byte(show), &tasks); err != nil || len(tasks) != 1 {
		t.Fatalf("show: %v\n%s", err, show)
	}
	if !strings.HasPrefix(tasks[0].RepeatAnchor, "2026-03-09") || !strings.HasPrefix(tasks[0].Due, "2026-03-09") {
		t.Errorf("anchor %q due %q, want both on 2026-03-09 (the due of the same write)", tasks[0].RepeatAnchor, tasks[0].Due)
	}

	// --due alone moves the occurrence and leaves the anchor where it was.
	if _, code := run(t, "set", id, "--due", "2026-03-16"); code != 0 {
		t.Fatalf("set --due exit %d", code)
	}
	show, _ = run(t, "--json", "show", id)
	if err := json.Unmarshal([]byte(show), &tasks); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tasks[0].RepeatAnchor, "2026-03-09") {
		t.Errorf("--due alone must not move the anchor: %q", tasks[0].RepeatAnchor)
	}
}

// reschedule drill #3: `ls --all` is exit 2, and the error now names the
// two caps a caller lifts instead.
func TestLsUnknownAllFlagNamesTheEscapes(t *testing.T) {
	initStore(t)
	fe, _ := runErr(t, "ls", "--all")
	if fe == nil || exitOf(fe) != 2 || !strings.Contains(fe.Msg, "-n 0") || !strings.Contains(fe.Msg, "-r ''") {
		t.Fatalf("want exit 2 naming -n 0 and -r '', got %+v", fe)
	}
}
