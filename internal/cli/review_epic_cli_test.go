package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// `furrow review <epic-ref>` stamps the box's reviewed timestamp — the v9 arm
// of the review dispatch, tried after task and repo so no pre-v9 ref changes
// meaning. The envelope is the epic's own {before,after,changed}.
func TestCLIReviewEpicStampsBox(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "epic", "add", "mandate inbox")
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, out)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.ID == "" {
		t.Fatalf("parse epic add --json (%v):\n%s", err, out)
	}

	out, code = run(t, "--json", "review", e.ID)
	if code != 0 {
		t.Fatalf("review epic exit = %d:\n%s", code, out)
	}
	var env struct {
		Changed []string `json:"changed"`
		After   struct {
			Reviewed *string `json:"reviewed"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse review --json: %v\n%s", err, out)
	}
	if env.After.Reviewed == nil {
		t.Errorf("after.reviewed must be stamped:\n%s", out)
	}
	if strings.Join(env.Changed, ",") != "reviewed" {
		t.Errorf("changed = %v, want exactly [reviewed]", env.Changed)
	}

	// The epic-ref contract holds here too: a unique title substring resolves.
	if out, code := run(t, "review", "mandate"); code != 0 {
		t.Errorf("unique title substring must resolve, exit = %d:\n%s", code, out)
	}
}

// The incumbent dispatch is untouched: a task id still stamps the task, and an
// unknown epic-shaped ref gets the epic resolver's exit 2 with candidates.
func TestCLIReviewDispatchPrecedence(t *testing.T) {
	initStore(t)
	id := addTask(t, "a task")

	out, code := run(t, "--json", "review", id)
	if code != 0 || !strings.Contains(out, "\"reviewed\"") {
		t.Fatalf("task review exit = %d:\n%s", code, out)
	}

	err, _ := runErr(t, "review", "e-nope0")
	if err == nil || int(err.Code) != 2 {
		t.Fatalf("unknown epic-shaped ref want exit 2, got %+v", err)
	}
}
