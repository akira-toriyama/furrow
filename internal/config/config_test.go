package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintArchiveDoneParsing pins t-0051: [lint].archive_done parses, defaults to
// 0 (disabled), and a negative value clamps to 0 with a warning.
func TestLintArchiveDoneParsing(t *testing.T) {
	cfg, _, err := Load(writeTOML(t, "[lint]\narchive_done = 25\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LintArchiveDone != 25 {
		t.Errorf("archive_done = %d, want 25", cfg.LintArchiveDone)
	}
	if Default().LintArchiveDone != 0 {
		t.Errorf("default archive_done should be 0 (disabled), got %d", Default().LintArchiveDone)
	}
	cfg, warn, _ := Load(writeTOML(t, "[lint]\narchive_done = -3\n"))
	if cfg.LintArchiveDone != 0 {
		t.Errorf("a negative archive_done should clamp to 0, got %d", cfg.LintArchiveDone)
	}
	if len(warn) == 0 {
		t.Error("a negative archive_done should warn")
	}
}

// TestAliasParsing pins t-awsb: the board [alias] table parses, and a
// blank-value entry drops with a clamp warning (clamp-don't-reject).
func TestAliasParsing(t *testing.T) {
	path := writeTOML(t, "[alias]\ntriage = \"ls -s inbox,backlog\"\nempty = \"\"\n")
	cfg, warn, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Alias["triage"] != "ls -s inbox,backlog" {
		t.Errorf("triage alias should parse: %v", cfg.Alias)
	}
	if _, ok := cfg.Alias["empty"]; ok {
		t.Errorf("an empty-value alias should be dropped: %v", cfg.Alias)
	}
	joined := strings.Join(warn, "\n")
	if !strings.Contains(joined, "empty") {
		t.Errorf("an empty-value alias should warn: %v", warn)
	}
}

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

func TestIDPatternAcceptsLegacyAndRandom(t *testing.T) {
	c, _, _ := Load(filepath.Join(t.TempDir(), "absent.toml")) // default prefix "t-"
	re := c.IDPattern()
	for _, ok := range []string{"t-0042", "t-0001", "t-k3m9p"} { // legacy numeric + new random
		if !re.MatchString(ok) {
			t.Errorf("%q should match the id pattern", ok)
		}
	}
	for _, bad := range []string{"t-K3M9P", "x-0042", "t-", "t-ab cd"} {
		if re.MatchString(bad) {
			t.Errorf("%q should NOT match the id pattern", bad)
		}
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

func TestWaitingLaneDefault(t *testing.T) {
	c := Default()
	if !c.IsLane("waiting") {
		t.Fatal("default config should include a waiting lane")
	}
	if !c.IsTerminal("waiting") {
		t.Error("waiting should be terminal (excluded from next, parked not done)")
	}
	if c.IsNextLane("waiting") {
		t.Error("waiting must not be a next-lane")
	}
	// it sorts between in-progress and done.
	inProg, _ := c.LaneRank("in-progress")
	wait, _ := c.LaneRank("waiting")
	done, _ := c.LaneRank("done")
	if inProg >= wait || wait >= done {
		t.Errorf("waiting should sort between in-progress and done, got %d/%d/%d", inProg, wait, done)
	}
	// adding it must not change the default next set.
	if !c.IsNextLane("ready") || !c.IsNextLane("in-progress") {
		t.Error("default next should remain ready + in-progress")
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
