package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `furrow ref` is the after-the-fact edit for what `add --ref` sets at
// creation: --add appends (idempotent), --rm drops (absent = no-op), and the
// stored order is the user's, not sorted (refs are a sequence, unlike labels).
func TestCLIRefCommandMutatesAndReportsChanged(t *testing.T) {
	initStore(t)
	id := addTask(t, "edit me", "--ref", "docs/a.md:10")

	out, code := run(t, "--json", "ref", id, "--add", "internal/cli/root.go:42", "--rm", "docs/a.md:10")
	if code != 0 {
		t.Fatalf("ref --add/--rm exit = %d:\n%s", code, out)
	}
	var res struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("ref --json should be a mutation object: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(res.After.Refs, []string{"internal/cli/root.go:42"}) {
		t.Errorf("after refs = %v, want the added ref only", res.After.Refs)
	}
	if !contains(res.Changed, "refs") {
		t.Errorf("changed should include refs, got %v", res.Changed)
	}

	// Adding an existing ref is a no-op: changed stays empty.
	out, _ = run(t, "--json", "ref", id, "--add", "internal/cli/root.go:42")
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 0 {
		t.Errorf("idempotent add should report no change, got %v", res.Changed)
	}

	// No flags is bad usage (exit 2), never a silent no-op.
	if _, code := run(t, "ref", id); code != int(core.CodeValidation) {
		t.Errorf("ref with no flags should exit 2, got %d", code)
	}

	// Unknown id is exit 1 (a specifically requested id was not found).
	if _, code := run(t, "ref", "t-9999", "--add", "x.md:1"); code != int(core.CodeNotFound) {
		t.Errorf("ref on unknown id should exit 1, got %d", code)
	}
}
