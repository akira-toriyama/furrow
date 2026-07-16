package cli

import (
	"encoding/json"
	"testing"
)

// setEnv is the slice of the set mutation envelope these tests read.
type setEnv struct {
	After struct {
		Status   string `json:"status"`
		Priority int    `json:"priority"`
	} `json:"after"`
	Changed    []string `json:"changed"`
	Renumbered []struct {
		ID   string `json:"id"`
		From int    `json:"from"`
		To   int    `json:"to"`
	} `json:"renumbered"`
}

func TestSetLanePlusPositionCLI(t *testing.T) {
	initStore(t)
	addTask(t, "b", "-s", "ready")      // ready: 100
	c := addTask(t, "c", "-s", "ready") // ready: 110
	x := addTask(t, "x")

	got, code := run(t, "--json", "set", x, "-s", "ready", "--before", c)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var env setEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Status != "ready" || env.After.Priority != 105 {
		t.Errorf("after = %s/%d, want ready/105", env.After.Status, env.After.Priority)
	}
	has := map[string]bool{}
	for _, ch := range env.Changed {
		has[ch] = true
	}
	if !has["status"] || !has["priority"] {
		t.Errorf("changed = %v, want both status and priority", env.Changed)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(got), &raw)
	if _, ok := raw["renumbered"]; ok {
		t.Errorf("renumbered key present on a plain midpoint drop: %s", got)
	}
}

func TestSetRelativeRespaceCLI(t *testing.T) {
	initStore(t)
	b := addTask(t, "b", "-s", "ready")
	c := addTask(t, "c", "-s", "ready")
	x := addTask(t, "x")
	if _, code := run(t, "reorder", b, "10"); code != 0 {
		t.Fatal("seed reorder failed")
	}
	if _, code := run(t, "reorder", c, "11"); code != 0 {
		t.Fatal("seed reorder failed")
	}

	got, code := run(t, "--json", "set", x, "-s", "ready", "--before", c)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var env setEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Priority != 110 {
		t.Errorf("target priority = %d, want 110", env.After.Priority)
	}
	if len(env.Renumbered) != 2 {
		t.Fatalf("renumbered = %v, want the two respaced neighbors", env.Renumbered)
	}
	if env.Renumbered[0].ID != b || env.Renumbered[0].To != 100 || env.Renumbered[1].ID != c || env.Renumbered[1].To != 120 {
		t.Errorf("renumbered = %+v, want %s->100, %s->120", env.Renumbered, b, c)
	}
}

func TestSetPriorityCLIValidation(t *testing.T) {
	initStore(t)
	b := addTask(t, "b", "-s", "ready")
	x := addTask(t, "x")

	// --priority is exclusive with --before/--after (cobra-enforced).
	if _, code := run(t, "set", x, "--priority", "5", "--before", b); code != 2 {
		t.Errorf("priority+before: exit %d, want 2", code)
	}
	// A relative target outside the destination lane is refused (no -s: the
	// destination is x's own lane).
	if _, code := run(t, "set", x, "--before", b); code != 2 {
		t.Errorf("cross-lane target: exit %d, want 2", code)
	}
	// Absolute --priority combines with the other facets in one write.
	got, code := run(t, "--json", "set", x, "-p", "7", "--value", "3")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var env setEnv
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Priority != 7 {
		t.Errorf("priority = %d, want 7", env.After.Priority)
	}
}
