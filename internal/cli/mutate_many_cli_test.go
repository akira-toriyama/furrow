package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// done/move accept several ids (the write-side twin of `show <id>...`): one
// index write for the whole batch, all-or-nothing on a miss. The --json arity
// convention mirrors show — one id keeps the classic single envelope, ≥2 ids
// emit an array of envelopes, --ndjson streams one envelope per line.
func TestCLIDoneAndMoveAcceptMultipleIds(t *testing.T) {
	initStore(t)
	a := addTask(t, "alpha")
	b := addTask(t, "beta")
	c := addTask(t, "gamma")

	type env struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}

	// move <id>... <lane>: the last arg is the lane, everything before it an id.
	out, code := run(t, "--json", "move", a, b, "ready")
	if code != 0 {
		t.Fatalf("move multi exit = %d:\n%s", code, out)
	}
	var many []env
	if err := json.Unmarshal([]byte(out), &many); err != nil {
		t.Fatalf("multi-id move --json should be an ARRAY of envelopes: %v\n%s", err, out)
	}
	if len(many) != 2 {
		t.Fatalf("envelopes = %d, want 2", len(many))
	}
	for i, e := range many {
		if e.After.Status != "ready" || !contains(e.Changed, "status") {
			t.Errorf("envelope[%d] = %+v; want status ready + changed status", i, e)
		}
	}

	// A single id keeps the classic single-object shape (compat).
	out, code = run(t, "--json", "move", c, "ready")
	if code != 0 {
		t.Fatalf("move single exit = %d:\n%s", code, out)
	}
	var one env
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		t.Fatalf("single-id move --json must stay one object: %v\n%s", err, out)
	}

	// done <id>...: closes the batch, stamping closed on each.
	out, code = run(t, "--json", "done", a, b)
	if code != 0 {
		t.Fatalf("done multi exit = %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &many); err != nil {
		t.Fatalf("multi-id done --json should be an array: %v\n%s", err, out)
	}
	for i, e := range many {
		if e.After.Status != "done" || e.After.Closed == nil {
			t.Errorf("done envelope[%d] = %+v; want done lane with closed stamped", i, e)
		}
	}

	// --ndjson streams one envelope per line.
	out, code = run(t, "--ndjson", "move", a, b, "backlog")
	if code != 0 {
		t.Fatalf("move --ndjson exit = %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines = %d, want 2:\n%s", len(lines), out)
	}
	for _, l := range lines {
		var e env
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Errorf("ndjson line is not an envelope: %v\n%s", err, l)
		}
	}

	// A miss fails the WHOLE batch (exit 1, details.missing) and moves nothing.
	if _, code := run(t, "move", c, "t-nope", "backlog"); code != int(core.CodeNotFound) {
		t.Errorf("batch with a miss should exit 1, got %d", code)
	}
	out, _ = run(t, "--json", "show", c, "--no-body")
	var shown core.Task
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if shown.Status != "ready" {
		t.Errorf("c = %q after failed batch; all-or-nothing broken", shown.Status)
	}

	// An unknown lane on a batch is still exit 2 with candidates.
	if _, code := run(t, "move", a, b, "reddy"); code != int(core.CodeValidation) {
		t.Errorf("unknown lane should exit 2, got %d", code)
	}
}

// `set <id>...` is the bulk-triage arity: --json emits an ARRAY of envelopes for
// two or more ids and keeps the classic object for one (the show arity
// convention), --ndjson is one envelope per line at any arity, and the position
// flags are refused for a batch.
func TestCLISetManyArityAndPositionGuard(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	out, code := run(t, "--json", "set", a, b, "-s", "ready", "--add-label", "triaged")
	if code != 0 {
		t.Fatalf("bulk set exit = %d:\n%s", code, out)
	}
	var envs []struct {
		After struct {
			ID     string   `json:"id"`
			Status string   `json:"status"`
			Labels []string `json:"labels"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil {
		t.Fatalf("two ids must emit an ARRAY of envelopes: %v\n%s", err, out)
	}
	if len(envs) != 2 {
		t.Fatalf("want 2 envelopes, got %d:\n%s", len(envs), out)
	}
	for i, e := range envs {
		if e.After.Status != "ready" || len(e.After.Labels) != 1 || e.After.Labels[0] != "triaged" {
			t.Errorf("envelope %d not fully set: %+v", i, e.After)
		}
	}

	// One id keeps the classic object.
	out, code = run(t, "--json", "set", a, "--effort", "2")
	if code != 0 {
		t.Fatalf("single set exit = %d:\n%s", code, out)
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(out), &single); err != nil {
		t.Fatalf("one id must keep the classic object: %v\n%s", err, out)
	}
	if _, ok := single["changed"]; !ok {
		t.Errorf("single-id set should emit {before,after,changed}, got %v", single)
	}

	// --ndjson is one envelope per line at any arity.
	out, code = run(t, "--ndjson", "set", a, b, "--value", "3")
	if code != 0 {
		t.Fatalf("ndjson set exit = %d:\n%s", code, out)
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 2 {
		t.Errorf("--ndjson should print one line per task, got %d:\n%s", n, out)
	}

	// A position flag over a batch is exit 2.
	if _, code := run(t, "set", a, b, "--priority", "50"); code != int(core.CodeValidation) {
		t.Errorf("--priority over 2 ids should exit 2, got %d", code)
	}

	// A miss sets nothing and names every miss.
	fe, _ := runErr(t, "--json", "set", a, "t-zzzz1", "-s", "backlog")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("a batch with a miss should exit 1, got %+v", fe)
	}
	out, _ = run(t, "--json", "show", a, "--no-body")
	if !strings.Contains(out, `"status": "ready"`) {
		t.Errorf("a failed batch must change nothing; %s moved:\n%s", a, out)
	}
}
