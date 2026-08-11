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

// standalone is the local single-machine opt-in: absent -> false (shared board,
// the default), and a bool has no out-of-range value so it never warns.
func TestStandaloneParsing(t *testing.T) {
	if c, _, err := Load(writeTOML(t, "")); err != nil {
		t.Fatal(err)
	} else if c.Standalone {
		t.Errorf("absent standalone must default to false (shared board)")
	}

	c, warn, err := Load(writeTOML(t, "standalone = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Standalone {
		t.Errorf("standalone = true must parse as true")
	}
	if len(warn) != 0 {
		t.Errorf("a valid standalone bool must not warn: %v", warn)
	}

	if c, _, _ := Load(writeTOML(t, "standalone = false\n")); c.Standalone {
		t.Errorf("standalone = false must parse as false")
	}
}

// default_repo is the board's own scope declaration. This layer carries it
// VERBATIM (minus surrounding space) and never judges the shape — config is
// core-free, so "is this owner/repo?" is app.applyBoardScope's call, exactly as
// [lint].ignore_codes defers the lint-code vocabulary. So: no warning here, ever.
func TestDefaultRepoParsing(t *testing.T) {
	if c, _, err := Load(writeTOML(t, "")); err != nil {
		t.Fatal(err)
	} else if c.DefaultRepo != "" {
		t.Errorf("absent default_repo = %q, want empty (no scope declared)", c.DefaultRepo)
	}

	c, warn, err := Load(writeTOML(t, "default_repo = \"  me/app  \"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultRepo != "me/app" {
		t.Errorf("default_repo = %q, want the trimmed literal", c.DefaultRepo)
	}
	if len(warn) != 0 {
		t.Errorf("config never warns about default_repo — the app layer clamps it: %v", warn)
	}

	// Even a value the app will refuse passes through untouched here.
	if c, warn, _ := Load(writeTOML(t, "default_repo = \"auto\"\n")); c.DefaultRepo != "auto" || len(warn) != 0 {
		t.Errorf("default_repo/warn = %q/%v, want %q carried verbatim with no warning", c.DefaultRepo, warn, "auto")
	}

	// A bare key AFTER a table belongs to that table — the TOML rule the template
	// puts both top-level switches above [lanes] for. Pinned so the docs can
	// promise it. Strict decode turns the swallowed key into a WARNING (it is
	// lanes.default_repo, which the parser does not know) — the silent vanish
	// this used to pin is exactly what the warning now prevents.
	c2, warn2, _ := Load(writeTOML(t, "[lanes]\ndefault = \"inbox\"\ndefault_repo = \"me/app\"\n"))
	if c2.DefaultRepo != "" {
		t.Errorf("default_repo = %q after [lanes]; a bare key under a table is lanes.default_repo, not top-level", c2.DefaultRepo)
	}
	if len(warn2) != 1 || !strings.Contains(warn2[0], "lanes.default_repo") {
		t.Errorf("the swallowed key should warn as unknown lanes.default_repo, got %v", warn2)
	}
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
	if c.IDPrefix != "F-" || c.IDWidth != 3 {
		t.Errorf("ids wrong: %+v", c)
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
unknown_key = 42           # unknown -> ignored + warn, no error
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
	if len(warn) < 4 {
		t.Errorf("expected >=4 clamp warnings, got %d: %v", len(warn), warn)
	}
}

// TestUnknownKeyWarnsWithLine pins the strict-decode half of clamp-don't-reject:
// a key the parser does not know — a typo'd section, a retired key, a stray
// top-level name — is IGNORED (never an error) and WARNED about, naming the
// file, the line, and the full dotted key. This is what makes the template's
// "`furrow lint` reports what it clamped" true: lenient decode made an ignored
// key invisible, so a `[lanse]` typo silently reset the whole lane vocabulary.
func TestUnknownKeyWarnsWithLine(t *testing.T) {
	p := writeTOML(t, `[lanse]
order = ["a"]

[ids]
prefix = "t-"

[ui]
theme = "auto"
`)
	c, warn, err := Load(p)
	if err != nil {
		t.Fatalf("unknown keys must not error: %v", err)
	}
	// The typo'd section fell back to defaults; the known section still decoded.
	if len(c.Lanes) != len(DefaultLanes) {
		t.Errorf("lanes should be the defaults (the [lanse] typo is ignored), got %v", c.Lanes)
	}
	if c.IDPrefix != "t-" {
		t.Errorf("known keys must survive alongside unknown ones, got prefix %q", c.IDPrefix)
	}
	joined := strings.Join(warn, "\n")
	// An unknown TABLE is reported once by its name ([ui] -> "ui", the retired
	// section this rework deleted); an unknown key inside a KNOWN table is
	// reported with its dotted path (see TestClampDontReject's ids.unknown_key).
	for _, want := range []string{`"lanse"`, `"ui"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should name unknown key %s, got %v", want, warn)
		}
	}
	// Line numbers: the file path and a line make the warning actionable.
	if !strings.Contains(joined, p+":1:") {
		t.Errorf("warnings should carry file:line (want %s:1: for [lanse]), got %v", p, warn)
	}
}

func TestMalformedIsError(t *testing.T) {
	p := writeTOML(t, "this is = = not toml [[[")
	if _, _, err := Load(p); err == nil {
		t.Error("malformed TOML should error")
	}
}

// t-z36a: a wrong-typed VALUE follows clamp-don't-reject exactly like an
// unknown key — the key falls back to its default with a file:line warning,
// everything else keeps its written value, and go-toml's struct-tag prose
// never reaches the user. Only malformed TOML stays fatal.
func TestLoadBytesSalvagesWrongTypedKeys(t *testing.T) {
	doc := []byte("[priority]\nstep = \"10\"\ndefault = 300\n\n[ids]\nwidth = 7\n")
	c, warn, err := LoadBytes(doc, "config.toml")
	if err != nil {
		t.Fatalf("a wrong-typed value must not be fatal: %v", err)
	}
	if c.PriorityStep != Default().PriorityStep {
		t.Errorf("the bad key must fall back to its default, got %d", c.PriorityStep)
	}
	if c.PriorityDefault != 300 || c.IDWidth != 7 {
		t.Errorf("healthy keys must keep their written values, got default=%d width=%d", c.PriorityDefault, c.IDWidth)
	}
	found := false
	for _, w := range warn {
		if strings.Contains(w, "config.toml:2:") && strings.Contains(w, `"priority.step"`) {
			found = true
			if strings.Contains(w, "toml:\"") || strings.Contains(w, "struct field") {
				t.Errorf("struct internals must not leak into the warning: %q", w)
			}
		}
	}
	if !found {
		t.Errorf("want a file:line warning naming priority.step, got %v", warn)
	}

	// Two bad keys: both salvaged, both warned with their ORIGINAL lines.
	doc = []byte("[priority]\nstep = \"10\"\n\n[ids]\nwidth = \"seven\"\n")
	_, warn, err = LoadBytes(doc, "config.toml")
	if err != nil {
		t.Fatalf("two wrong-typed values must not be fatal: %v", err)
	}
	var hits int
	for _, w := range warn {
		if strings.Contains(w, ":2:") && strings.Contains(w, "priority.step") {
			hits++
		}
		if strings.Contains(w, ":5:") && strings.Contains(w, "ids.width") {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("both keys must warn on their original lines, got %v", warn)
	}

	// Malformed TOML is not a type error: still fatal.
	if _, _, err := LoadBytes([]byte("[priority\nstep = 10\n"), "config.toml"); err == nil {
		t.Error("malformed TOML must stay a hard error")
	}
}
