package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingIsDefault(t *testing.T) {
	c, warn, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 0 {
		t.Errorf("missing file should warn nothing, got %v", warn)
	}
	if c.DefaultLane != "inbox" || c.PriorityStep != 10 || c.IDPrefix != "t-" {
		t.Errorf("missing file did not yield defaults: %+v", c)
	}
}

func TestLoadValid(t *testing.T) {
	p := writeTOML(t, `
[lanes]
order = ["todo", "doing", "done"]
default = "todo"
done = "done"
terminal = ["done"]

[priority]
step = 5
default = 50

[ids]
prefix = "F-"
width = 3

[ui]
theme = "dark"
`)
	c, warn, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(warn) != 0 {
		t.Errorf("valid config warned: %v", warn)
	}
	if len(c.Lanes) != 3 || c.Lanes[1] != "doing" {
		t.Errorf("lanes = %v", c.Lanes)
	}
	if c.DefaultLane != "todo" || c.DoneLane != "done" || !c.IsTerminal("done") {
		t.Errorf("lane config wrong: %+v", c)
	}
	if c.PriorityStep != 5 || c.PriorityDefault != 50 {
		t.Errorf("priority wrong: %+v", c)
	}
	if c.IDPrefix != "F-" || c.IDWidth != 3 || c.UITheme != "dark" {
		t.Errorf("ids/ui wrong: %+v", c)
	}
	if !c.IDPattern().MatchString("F-007") || c.IDPattern().MatchString("t-007") {
		t.Errorf("id pattern wrong for prefix %q", c.IDPrefix)
	}
}

func TestNextLanes(t *testing.T) {
	// default (no [next]) -> ready + in-progress.
	c, _, _ := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if !c.IsNextLane("ready") || !c.IsNextLane("in-progress") {
		t.Errorf("default next lanes should be ready+in-progress, got %v", c.NextLanes)
	}
	if c.IsNextLane("inbox") || c.IsNextLane("backlog") {
		t.Errorf("default next lanes must exclude inbox/backlog, got %v", c.NextLanes)
	}

	// explicit [next].lanes, with a bogus entry dropped + a warning.
	p := writeTOML(t, `
[lanes]
order = ["inbox", "ready", "done"]
[next]
lanes = ["ready", "ghost"]
`)
	c, warn, _ := Load(p)
	if len(c.NextLanes) != 1 || c.NextLanes[0] != "ready" {
		t.Errorf("next.lanes should keep only real lanes, got %v", c.NextLanes)
	}
	if !anyHas(warn, "ghost") {
		t.Errorf("expected a warning about the bogus next lane, got %v", warn)
	}

	// custom scheme without ready/in-progress -> falls back to all non-terminal.
	p2 := writeTOML(t, `
[lanes]
order = ["todo", "doing", "done"]
terminal = ["done"]
`)
	c2, _, _ := Load(p2)
	if !c2.IsNextLane("todo") || !c2.IsNextLane("doing") || c2.IsNextLane("done") {
		t.Errorf("custom-scheme next fallback should be all non-terminal lanes, got %v", c2.NextLanes)
	}
}

func anyHas(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestLabelsRequired(t *testing.T) {
	// default: not required.
	c, _, _ := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if c.LabelsRequired {
		t.Error("labels.required should default to false")
	}
	// explicit true.
	p := writeTOML(t, "[labels]\nrequired = true\n")
	c, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.LabelsRequired {
		t.Error("labels.required = true should parse as true")
	}
}

func TestClampDontReject(t *testing.T) {
	p := writeTOML(t, `
[lanes]
order = ["a", "b"]
default = "ghost"          # not in order -> clamp to a + warn
terminal = ["b", "ghost"]  # ghost dropped + warn

[priority]
step = 0                   # invalid -> default + warn

[ids]
width = -2                 # invalid -> default + warn

[ui]
theme = "neon"             # invalid -> auto + warn

unknown_key = 42           # ignored, no error
`)
	c, warn, err := Load(p)
	if err != nil {
		t.Fatalf("clampable config must not error: %v", err)
	}
	if c.DefaultLane != "a" {
		t.Errorf("default lane should clamp to first lane, got %q", c.DefaultLane)
	}
	if c.IsTerminal("ghost") || !c.IsTerminal("b") {
		t.Errorf("terminal should drop ghost, keep b: %+v", c.Terminal)
	}
	if c.PriorityStep != DefaultPriorityStep || c.IDWidth != DefaultIDWidth {
		t.Errorf("invalid numerics should clamp: step=%d width=%d", c.PriorityStep, c.IDWidth)
	}
	if c.UITheme != "auto" {
		t.Errorf("invalid theme should clamp to auto, got %q", c.UITheme)
	}
	if len(warn) < 4 {
		t.Errorf("expected >=4 clamp warnings, got %d: %v", len(warn), warn)
	}
}

func TestMalformedIsError(t *testing.T) {
	p := writeTOML(t, "this is = = not toml [[[")
	if _, _, err := Load(p); err == nil {
		t.Error("malformed TOML should error")
	}
}
