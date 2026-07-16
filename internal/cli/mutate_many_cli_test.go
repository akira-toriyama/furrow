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
