package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// reorderEnv is the slice of the mutation envelope the reorder tests read.
type reorderEnv struct {
	After struct {
		Priority int `json:"priority"`
	} `json:"after"`
	Changed    []string `json:"changed"`
	Renumbered []struct {
		ID   string `json:"id"`
		From int    `json:"from"`
		To   int    `json:"to"`
	} `json:"renumbered"`
}

func TestReorderRelativeCLIMidpoint(t *testing.T) {
	initStore(t)
	addTask(t, "a")
	b := addTask(t, "b")
	c := addTask(t, "c") // inbox: a=100, b=110, c=120

	got, code := run(t, "--json", "reorder", c, "--before", b)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var env reorderEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Priority != 105 {
		t.Errorf("priority = %d, want 105", env.After.Priority)
	}
	if len(env.Changed) != 1 || env.Changed[0] != "priority" {
		t.Errorf("changed = %v, want [priority]", env.Changed)
	}
	// No respace happened, so the envelope must not carry a renumbered key.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(got), &raw)
	if _, ok := raw["renumbered"]; ok {
		t.Errorf("renumbered key present on a plain midpoint move: %s", got)
	}
}

func TestReorderRelativeCLIRespace(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")
	c := addTask(t, "c")
	// Exhaust the gap between a and b.
	if _, code := run(t, "reorder", a, "10"); code != 0 {
		t.Fatal("seed reorder failed")
	}
	if _, code := run(t, "reorder", b, "11"); code != 0 {
		t.Fatal("seed reorder failed")
	}

	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	got, code := run(t, "--json", "reorder", c, "--before", b)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var env reorderEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Priority != 110 {
		t.Errorf("target priority = %d, want 110", env.After.Priority)
	}
	if len(env.Renumbered) != 2 {
		t.Fatalf("renumbered = %v, want the two respaced neighbors", env.Renumbered)
	}
	if env.Renumbered[0].ID != a || env.Renumbered[0].From != 10 || env.Renumbered[0].To != 100 {
		t.Errorf("renumbered[0] = %+v, want %s 10->100", env.Renumbered[0], a)
	}
	if env.Renumbered[1].ID != b || env.Renumbered[1].From != 11 || env.Renumbered[1].To != 120 {
		t.Errorf("renumbered[1] = %+v, want %s 11->120", env.Renumbered[1], b)
	}
	if !strings.Contains(se.String(), "respaced 2") {
		t.Errorf("stderr must note the respace, got: %q", se.String())
	}
}

func TestReorderCLIValidation(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	// Absolute and relative are exclusive; one of them is required.
	if _, code := run(t, "reorder", a, "50", "--before", b); code != 2 {
		t.Errorf("absolute+relative: exit %d, want 2", code)
	}
	if _, code := run(t, "reorder", a); code != 2 {
		t.Errorf("neither: exit %d, want 2", code)
	}
	if _, code := run(t, "reorder", a, "--before", b, "--after", b); code != 2 {
		t.Errorf("--before with --after: exit %d, want 2", code)
	}
	// Relative order across lanes is refused.
	if _, code := run(t, "move", b, "ready"); code != 0 {
		t.Fatal("move failed")
	}
	if _, code := run(t, "reorder", a, "--before", b); code != 2 {
		t.Errorf("cross-lane: exit %d, want 2", code)
	}
	// A missing relative target is a not-found (exit 1), like any requested id.
	if _, code := run(t, "reorder", a, "--after", "t-none"); code != 1 {
		t.Errorf("missing ref: exit %d, want 1", code)
	}
	// The absolute form still works unchanged.
	got, code := run(t, "--json", "reorder", a, "42")
	if code != 0 {
		t.Fatalf("absolute: exit %d: %s", code, got)
	}
	var env reorderEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Priority != 42 {
		t.Errorf("absolute priority = %d, want 42", env.After.Priority)
	}
}
