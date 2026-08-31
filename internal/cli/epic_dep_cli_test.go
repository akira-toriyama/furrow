package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// addEpic runs `epic add --json` and returns the created epic's id.
func addEpic(t *testing.T, args ...string) string {
	t.Helper()
	out, code := run(t, append([]string{"--json", "epic", "add"}, args...)...)
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, out)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("parse epic add --json: %v\n%s", err, out)
	}
	return e.ID
}

type epicDepListJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	DependsOn []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
	} `json:"depends_on"`
	Blocks []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
	} `json:"blocks"`
}

// The dep mutation rides the standard epic envelope: changed names "deps", and
// --list reads both directions with the epic-shaped ref (state, not lane).
func TestEpicDepAddListAndEnvelope(t *testing.T) {
	initStore(t)
	moving := addEpic(t, "moving", "-r", "o/r")
	furniture := addEpic(t, "buy furniture", "-r", "o/r")

	out, code := run(t, "--json", "epic", "dep", furniture, moving)
	if code != 0 {
		t.Fatalf("epic dep exit=%d:\n%s", code, out)
	}
	var env struct {
		Changed []string `json:"changed"`
		After   struct {
			Deps []string `json:"deps"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	if len(env.Changed) != 1 || env.Changed[0] != "deps" {
		t.Errorf("changed = %v, want [deps]", env.Changed)
	}
	if len(env.After.Deps) != 1 || env.After.Deps[0] != moving {
		t.Errorf("after.deps = %v, want [%s]", env.After.Deps, moving)
	}

	out, code = run(t, "--json", "epic", "dep", furniture, "--list")
	if code != 0 {
		t.Fatalf("epic dep --list exit=%d:\n%s", code, out)
	}
	var r epicDepListJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(r.DependsOn) != 1 || r.DependsOn[0].ID != moving || r.DependsOn[0].State != "open" || r.DependsOn[0].Title != "moving" {
		t.Errorf("depends_on must resolve id+title+state, got %+v", r.DependsOn)
	}
	out, code = run(t, "--json", "epic", "dep", moving, "--list")
	if code != 0 {
		t.Fatalf("epic dep --list exit=%d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(r.Blocks) != 1 || r.Blocks[0].ID != furniture {
		t.Errorf("blocks must name the waiting box, got %+v", r.Blocks)
	}
}

func TestEpicDepListEmptyArraysNotNull(t *testing.T) {
	initStore(t)
	lone := addEpic(t, "lonely box")
	out, code := run(t, "--json", "epic", "dep", lone, "--list")
	if code != 0 {
		t.Fatalf("exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, `"depends_on": []`) || !strings.Contains(out, `"blocks": []`) {
		t.Errorf("empty edges must be [] not null:\n%s", out)
	}
}

// A cycle is refused at the door (exit 2), the lint backstop being for merges.
func TestEpicDepRefusesCycle(t *testing.T) {
	initStore(t)
	a := addEpic(t, "box a", "-r", "o/r")
	b := addEpic(t, "box b", "-r", "o/r")
	if _, code := run(t, "epic", "dep", b, a); code != 0 {
		t.Fatal("first edge must land")
	}
	if _, code := run(t, "epic", "dep", a, b); code != 2 {
		t.Error("the closing edge must be exit 2")
	}
}

// Activating a box that still waits on an open dep proceeds, says so on
// stderr, and carries open_deps in the envelope.
func TestEpicActivateOpenDepsWarning(t *testing.T) {
	initStore(t)
	moving := addEpic(t, "moving", "-r", "o/r")
	furniture := addEpic(t, "buy furniture", "-r", "o/f")
	if _, code := run(t, "epic", "dep", furniture, moving); code != 0 {
		t.Fatal("dep")
	}

	out, code := run(t, "--json", "epic", "activate", furniture)
	if code != 0 {
		t.Fatalf("activate must proceed, exit=%d:\n%s", code, out)
	}
	var env struct {
		OpenDeps []string `json:"open_deps"`
		After    struct {
			Active bool `json:"active"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	if !env.After.Active {
		t.Error("the box must be active")
	}
	if len(env.OpenDeps) != 1 || env.OpenDeps[0] != moving {
		t.Errorf("open_deps = %v, want [%s]", env.OpenDeps, moving)
	}

	if _, code := run(t, "epic", "done", moving); code != 0 {
		t.Fatal("done")
	}
	out, code = run(t, "--json", "epic", "activate", furniture) // idempotent re-activate
	if code != 0 {
		t.Fatalf("re-activate exit=%d:\n%s", code, out)
	}
	if strings.Contains(out, "open_deps") {
		t.Errorf("a satisfied graph must not emit open_deps:\n%s", out)
	}
}

// epic ls (human) marks a waiting box, and epic show prints the resolved deps
// line; lint reports the active×open-dep state as epic-dep-open.
func TestEpicDepSurfacesInLsShowAndLint(t *testing.T) {
	initStore(t)
	moving := addEpic(t, "moving", "-r", "o/r")
	furniture := addEpic(t, "buy furniture", "-r", "o/f")
	if _, code := run(t, "epic", "dep", furniture, moving); code != 0 {
		t.Fatal("dep")
	}
	out, code := run(t, "epic", "ls")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "waits: "+moving) {
		t.Errorf("epic ls must mark the waiting box:\n%s", out)
	}
	out, code = run(t, "epic", "show", furniture)
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "deps:") || !strings.Contains(out, moving+" (open)") {
		t.Errorf("epic show must print the resolved deps line:\n%s", out)
	}

	if _, code := run(t, "epic", "activate", furniture); code != 0 {
		t.Fatal("activate")
	}
	out, _ = run(t, "--json", "lint")
	if !strings.Contains(out, "epic-dep-open") {
		t.Errorf("lint must warn epic-dep-open for the active waiting box:\n%s", out)
	}
}
