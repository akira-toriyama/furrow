package config

import (
	"os"
	"path/filepath"
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
