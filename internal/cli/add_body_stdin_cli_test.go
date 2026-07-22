package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// readTaskBody returns the on-disk bodies/<id>.md contents.
func readTaskBody(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv(app.EnvDir), "bodies", id+".md"))
	if err != nil {
		t.Fatalf("read body %s: %v", id, err)
	}
	return string(b)
}

// parseAddID pulls the created id out of `add --json` output.
func parseAddID(t *testing.T, out string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("parse add --json: %v\n%s", err, out)
	}
	if task.ID == "" {
		t.Fatalf("add --json produced no id:\n%s", out)
	}
	return task.ID
}

// TestAddBodyDashReadsStdin covers the t-gkp4 fix: `add --body -` reads the
// body from stdin (the shared `-`=stdin convention), instead of silently
// storing the literal string "-" and dropping the piped content.
func TestAddBodyDashReadsStdin(t *testing.T) {
	initStore(t)
	out, code := runIn(t, "x\ny\n", "--json", "add", "stdin-body", "--body", "-")
	if code != 0 {
		t.Fatalf("add --body - exit = %d:\n%s", code, out)
	}
	id := parseAddID(t, out)
	if got := strings.TrimRight(readTaskBody(t, id), "\n"); got != "x\ny" {
		t.Errorf("body from stdin = %q, want %q (must not be the literal \"-\")", got, "x\ny")
	}
}

// TestAddBodyDashRejectsStdinFlag: `--stdin` (bulk titles) and `--body -` both
// consume stdin, which has a single stream, so combining them is exit 2 rather
// than a silent wrong result.
func TestAddBodyDashRejectsStdinFlag(t *testing.T) {
	initStore(t)
	out, code := runIn(t, "a\nb\n", "add", "--stdin", "--body", "-")
	if code != int(core.CodeValidation) {
		t.Fatalf("add --stdin --body - should exit 2, got %d:\n%s", code, out)
	}
}

// TestAddBodyLiteralUnchanged: a non-dash --body value is stored verbatim.
func TestAddBodyLiteralUnchanged(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "add", "lit", "--body", "hello world")
	if code != 0 {
		t.Fatalf("add --body literal exit = %d:\n%s", code, out)
	}
	id := parseAddID(t, out)
	if got := strings.TrimRight(readTaskBody(t, id), "\n"); got != "hello world" {
		t.Errorf("literal body = %q, want %q", got, "hello world")
	}
}

// TestAddNoBodyKeepsDefaultHeading: an unset --body still seeds the title as a
// heading (readTextArg("") returns "" unchanged, so the default path is intact).
func TestAddNoBodyKeepsDefaultHeading(t *testing.T) {
	initStore(t)
	id := addTask(t, "My Title")
	if got := readTaskBody(t, id); !strings.HasPrefix(got, "# My Title") {
		t.Errorf("default body should start with a heading, got %q", got)
	}
}
