package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// futureDay is a date far enough out that the repeating member is not overdue
// while the test runs — the disclosure is about membership, not dates.
func futureDay() string { return time.Now().AddDate(0, 0, 30).Format("2006-01-02") }

// `epic done` never refuses a box with open members — it DISCLOSES them: one
// stderr note naming them, a second calling out the repeating ones (whose
// epic-closed comes back every cycle because the successor inherits the epic),
// and an open_members array beside the envelope. stdout stays the envelope.
func TestEpicDoneDisclosesOpenMembers(t *testing.T) {
	initStore(t)
	box := epicID(t, "repeat box", "-r", "me/r1")
	chore := addTask(t, "daily chore", "-e", box, "--due", futureDay(), "--repeat", "daily")
	plain := addTask(t, "plain member", "-e", box)
	parked := addTask(t, "parked member", "-e", box)
	mustRun(t, "move", parked, "icebox")

	stdout, stderr, code := runSplit(t, "--json", "epic", "done", box)
	if code != 0 {
		t.Fatalf("epic done exit = %d — an open member must never refuse the close:\n%s\n%s", code, stdout, stderr)
	}

	if !strings.Contains(stderr, "2 open member(s)") ||
		!strings.Contains(stderr, chore) || !strings.Contains(stderr, plain) {
		t.Errorf("stderr must name the members left open:\n%s", stderr)
	}
	if strings.Contains(stderr, parked) {
		t.Errorf("a parked (terminal-lane) member is not left behind — lint stays quiet about it too:\n%s", stderr)
	}
	if !strings.Contains(stderr, "1 of them repeats") || !strings.Contains(stderr, "furrow set "+chore+" -e <open-epic>") {
		t.Errorf("stderr must call out the repeating member and its one-shot remedy:\n%s", stderr)
	}

	var env struct {
		Members []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Repeat string `json:"repeat"`
		} `json:"open_members"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, stdout)
	}
	if len(env.Members) != 2 {
		t.Fatalf("open_members = %+v, want the two non-terminal members", env.Members)
	}
	byID := map[string]string{}
	for _, m := range env.Members {
		if m.Title == "" || m.Status == "" {
			t.Errorf("open_members entry %+v is missing the fields that let a reader act on it", m)
		}
		byID[m.ID] = m.Repeat
	}
	if byID[chore] == "" {
		t.Errorf("the repeating member's rule must ride the envelope: %+v", env.Members)
	}
	if r, ok := byID[plain]; !ok || r != "" {
		t.Errorf("a non-repeating member must carry repeat \"\" (present=%v, got %q) — absence is not an answer", ok, r)
	}
}

// Nothing open: no note at all, and the key is [] rather than missing.
func TestEpicDoneOnATidyBoxSaysNothing(t *testing.T) {
	initStore(t)
	box := epicID(t, "tidy box", "-r", "me/r1")
	id := addTask(t, "finished member", "-e", box)
	mustRun(t, "done", id)

	stdout, stderr, code := runSplit(t, "--json", "epic", "done", box)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, stdout)
	}
	if strings.Contains(stderr, "open member") {
		t.Errorf("a box closed with nothing open must say nothing:\n%s", stderr)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, stdout)
	}
	raw, ok := env["open_members"]
	if !ok || string(raw) != "[]" {
		t.Errorf("open_members = %s (present=%v), want [] — null is reserved for \"could not be read\"", raw, ok)
	}
}

// deactivate does not close the box, so nothing is left BEHIND one: the
// disclosure is `done`'s alone. Asserted as a contrast on ONE box, so the two
// halves can never drift apart.
func TestOpenMembersRideDoneNotDeactivate(t *testing.T) {
	initStore(t)
	box := epicID(t, "box", "-r", "me/r1")
	member := addTask(t, "member", "-e", box)
	mustRun(t, "epic", "activate", box)

	stdout, stderr, code := runSplit(t, "--json", "epic", "deactivate", box)
	if code != 0 {
		t.Fatalf("epic deactivate exit = %d:\n%s", code, stdout)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, stdout)
	}
	if _, ok := env["open_members"]; ok {
		t.Errorf("deactivate must not carry open_members:\n%s", stdout)
	}
	if strings.Contains(stderr, "open member") {
		t.Errorf("deactivate must not disclose open members:\n%s", stderr)
	}

	stdout, stderr, code = runSplit(t, "--json", "epic", "done", box)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, stdout)
	}
	if !strings.Contains(stderr, "1 open member(s)") || !strings.Contains(stderr, member) {
		t.Errorf("closing the same box must disclose the member deactivate stayed quiet about:\n%s", stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, stdout)
	}
	if _, ok := env["open_members"]; !ok {
		t.Errorf("done must carry open_members:\n%s", stdout)
	}
}
