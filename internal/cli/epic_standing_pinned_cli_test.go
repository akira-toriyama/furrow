package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The two v7 declarations ride epic set: the envelope names what changed, an
// explicit =false clears, and the displays (ls / show / brief / next) follow.
func TestEpicSetStandingPinned(t *testing.T) {
	initStore(t)
	mandate := addEpic(t, "mandate box", "-r", "o/r")

	out, code := run(t, "--json", "epic", "set", mandate, "--standing", "--pinned")
	if code != 0 {
		t.Fatalf("epic set exit=%d:\n%s", code, out)
	}
	var env struct {
		Changed []string `json:"changed"`
		After   struct {
			Standing bool `json:"standing"`
			Pinned   bool `json:"pinned"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if !env.After.Standing || !env.After.Pinned {
		t.Errorf("flags not stored: %+v", env.After)
	}
	if len(env.Changed) != 2 || env.Changed[0] != "standing" || env.Changed[1] != "pinned" {
		t.Errorf("changed = %v, want [standing pinned]", env.Changed)
	}

	out, code = run(t, "epic", "ls")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "📌 pinned") || !strings.Contains(out, "[standing]") {
		t.Errorf("epic ls must mark the declarations:\n%s", out)
	}
	out, code = run(t, "epic", "show", mandate)
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "state:    open, pinned, standing") {
		t.Errorf("epic show must state the declarations:\n%s", out)
	}

	// Explicit =false clears; a bare re-run with no flag touches nothing.
	out, code = run(t, "--json", "epic", "set", mandate, "--pinned=false")
	if code != 0 {
		t.Fatalf("clear exit=%d:\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if env.After.Pinned || !env.After.Standing {
		t.Errorf("--pinned=false must clear only pinned: %+v", env.After)
	}
}

// The pass-through, end to end: with another box active, the pinned box's task
// leads `next` and the brief header names the channel.
func TestNextAndBriefSurfacePinnedChannel(t *testing.T) {
	initStore(t)
	focus := addEpic(t, "the focus", "-r", "o/r")
	mandate := addEpic(t, "mandate box", "-r", "o/r")
	if _, code := run(t, "epic", "set", mandate, "--standing", "--pinned"); code != 0 {
		t.Fatal("set")
	}
	if _, code := run(t, "epic", "activate", focus); code != 0 {
		t.Fatal("activate")
	}
	focusTask := addTask(t, "focus work", "-e", focus, "-s", "ready")
	order := addTask(t, "an instruction", "-e", mandate, "-s", "ready")

	out, code := run(t, "next")
	if code != 0 {
		t.Fatal(out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// lines[0] is the table header.
	if len(lines) < 3 || !strings.Contains(lines[1], order) || !strings.Contains(lines[2], focusTask) {
		t.Errorf("the pinned task must lead next:\n%s", out)
	}

	out, code = run(t, "brief")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "epic: 📌 "+mandate) {
		t.Errorf("brief must name the pinned channel:\n%s", out)
	}

	// Deactivate everything: the pinned band still shows (the "deliberately
	// empty" contract now excludes pinned).
	if _, code := run(t, "epic", "deactivate", focus); code != 0 {
		t.Fatal("deactivate")
	}
	out, code = run(t, "next")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, order) || strings.Contains(out, focusTask) {
		t.Errorf("with nothing active only the pinned band may show:\n%s", out)
	}
}
