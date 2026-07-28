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

// TestAddStdinErrorsKeepCandidates: a bulk add's closed-vocabulary failure must
// reach the caller in the SAME shape as the single-item one — exit 2 WITH the
// vocabulary in `candidates` — plus the spec context saying which line failed.
// It used to rebuild the message by hand, so `add --stdin -s ghots` gave an
// agent a bare sentence to regex while `add -s ghots` handed it the array.
func TestAddStdinErrorsKeepCandidates(t *testing.T) {
	initStore(t)
	// A box has to exist for an unknown -e to have anything to SUGGEST: epic
	// candidates are the board's ids, not a compiled-in vocabulary, so on an empty
	// board an honest answer is an empty list.
	if _, code := run(t, "epic", "add", "seed box"); code != 0 {
		t.Fatalf("seed epic: exit %d", code)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown lane", []string{"add", "--stdin", "-s", "ghots"}},
		{"unknown epic", []string{"add", "--stdin", "-e", "no-such-box"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe, out := runErrIn(t, "bulk title\n", tc.args...)
			if fe == nil || fe.Code != core.CodeValidation {
				t.Fatalf("%v should exit 2, got %+v:\n%s", tc.args, fe, out)
			}
			if len(fe.Candidates) == 0 {
				t.Errorf("%v must carry the vocabulary in candidates, got %+v", tc.args, fe)
			}
			if !strings.Contains(fe.Msg, `spec 0 ("bulk title")`) {
				t.Errorf("%v must name the failing spec, got %q", tc.args, fe.Msg)
			}
		})
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
