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

// A ref is free text, so the flag layer must not CSV-parse it (t-pwrp): a URL
// with a comma in its query stayed one ref only by luck of quoting, and a bare
// `"` was a pflag parse error (exit 2). Both `add --ref` and `ref --add/--rm`
// take each value verbatim, and --rm's exact match sees the same bytes.
func TestCLIRefValuesAreVerbatim(t *testing.T) {
	initStore(t)
	url := "https://example.com/spec?rows=1,2"
	quoted := `docs/"quoted".md:3`
	id := addTask(t, "verbatim", "--ref", url, "--ref", quoted)

	var shown []core.Task
	out, code := run(t, "--json", "show", id, "--no-body")
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("show --json: %v\n%s", err, out)
	}
	if want := []string{url, quoted}; !reflect.DeepEqual(shown[0].Refs, want) {
		t.Fatalf("add --ref refs = %q, want %q (each value verbatim, no CSV split)", shown[0].Refs, want)
	}

	var res struct {
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	out, code = run(t, "--json", "ref", id, "--add", "a,b", "--rm", url)
	if code != 0 {
		t.Fatalf("ref --add/--rm exit = %d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("ref --json: %v\n%s", err, out)
	}
	if want := []string{quoted, "a,b"}; !reflect.DeepEqual(res.After.Refs, want) {
		t.Errorf("refs = %q, want %q (--rm matches the comma URL exactly; --add keeps a,b whole)", res.After.Refs, want)
	}
}
