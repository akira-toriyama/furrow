package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// `furrow --version` and `furrow version` render the identical human line, so
// there is one version string no matter how it is asked for.
func TestVersionFlagMatchesSubcommand(t *testing.T) {
	flagOut, fcode := run(t, "--version")
	if fcode != 0 {
		t.Fatalf("--version exit = %d, want 0", fcode)
	}
	cmdOut, ccode := run(t, "version")
	if ccode != 0 {
		t.Fatalf("version exit = %d, want 0", ccode)
	}
	if !strings.HasPrefix(flagOut, "furrow ") {
		t.Errorf("--version output = %q, want it to start with %q", flagOut, "furrow ")
	}
	if flagOut != cmdOut {
		t.Errorf("--version (%q) and version (%q) disagree", flagOut, cmdOut)
	}
}

// `furrow version --json` emits the build identity with stable keys so an agent
// can branch on them without parsing the human string.
func TestVersionJSON(t *testing.T) {
	out, code := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("version --json exit = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("version --json is not valid JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"version", "commit", "date", "modified"} {
		if _, ok := got[k]; !ok {
			t.Errorf("version --json missing key %q:\n%s", k, out)
		}
	}
	if got["version"] != "dev" {
		t.Errorf("version = %v, want \"dev\" (un-stamped test binary)", got["version"])
	}
}

// Every high-traffic command leads with a copy-pasteable Examples: block in
// --help (clig.dev "Lead with examples").
func TestHighTrafficCommandsHaveExamples(t *testing.T) {
	for _, name := range []string{"add", "ls", "next", "move", "done", "sync", "repo"} {
		out, code := run(t, name, "--help")
		if code != 0 {
			t.Fatalf("%s --help exit = %d, want 0", name, code)
		}
		if !strings.Contains(out, "Examples:") {
			t.Errorf("%s --help has no Examples: section:\n%s", name, out)
		}
	}
}
