package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// parseEnvelope decodes a single {before,after,changed,...} mutation envelope.
func parseEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	return env
}

// TestEditBodyDashReplacesFromStdin covers t-8q8c: `edit <id> --body -` is the
// non-interactive REPLACEMENT path — the whole body is substituted (not
// appended) and the shard's updated advances, which a direct edit of the file
// EditPath prints can never do.
func TestEditBodyDashReplacesFromStdin(t *testing.T) {
	initStore(t)
	id := addTask(t, "seeded title")

	out, code := runIn(t, "# rewritten\n\nnew plan\n", "--json", "edit", id, "--body", "-")
	if code != 0 {
		t.Fatalf("edit --body - exit = %d:\n%s", code, out)
	}
	body := readTaskBody(t, id)
	if body != "# rewritten\n\nnew plan\n" {
		t.Errorf("body = %q, want the stdin content verbatim", body)
	}
	if strings.Contains(body, "seeded title") {
		t.Errorf("replacement must not keep the old body:\n%s", body)
	}

	// `changed` tracks metadata only, so the envelope surfaces the effect as
	// replaced_bytes — and the count must match the file on disk.
	env := parseEnvelope(t, out)
	rb, ok := env["replaced_bytes"].(float64)
	if !ok {
		t.Fatalf("envelope missing replaced_bytes:\n%s", out)
	}
	if int(rb) != len(body) {
		t.Errorf("replaced_bytes = %d, want the on-disk length %d", int(rb), len(body))
	}
}

// TestEditBodyLiteral: a non-dash --body value replaces verbatim (plus the
// normalized trailing newline).
func TestEditBodyLiteral(t *testing.T) {
	initStore(t)
	id := addTask(t, "lit")
	if out, code := run(t, "edit", id, "--body", "replaced"); code != 0 {
		t.Fatalf("edit --body literal exit = %d:\n%s", code, out)
	}
	if got := readTaskBody(t, id); got != "replaced\n" {
		t.Errorf("body = %q, want %q", got, "replaced\n")
	}
}

// TestEditBodyEmptyIsExit2: an empty replacement (empty stdin, or an
// interpolated-empty --body ”) is refused — a body is never cleared silently —
// and the existing body survives.
func TestEditBodyEmptyIsExit2(t *testing.T) {
	initStore(t)
	id := addTask(t, "keep me")
	for name, argsIn := range map[string]struct {
		stdin string
		args  []string
	}{
		"empty stdin":   {"", []string{"edit", id, "--body", "-"}},
		"empty literal": {"", []string{"edit", id, "--body", ""}},
	} {
		out, code := runIn(t, argsIn.stdin, argsIn.args...)
		if code != int(core.CodeValidation) {
			t.Errorf("%s: want exit 2, got %d:\n%s", name, code, out)
		}
	}
	if got := readTaskBody(t, id); !strings.Contains(got, "keep me") {
		t.Errorf("refused replacement must leave the body intact, got %q", got)
	}
}

// TestEditBodyRoutesToEpic: membership routes `edit --body` exactly as it does
// `note` — an epic ref replaces the BOX's body and the epic envelope comes back.
func TestEditBodyRoutesToEpic(t *testing.T) {
	initStore(t)
	eid := addEpic(t, "a box")

	out, code := runIn(t, "# box plan\n", "--json", "edit", eid, "--body", "-")
	if code != 0 {
		t.Fatalf("edit epic --body exit = %d:\n%s", code, out)
	}
	if got := readTaskBody(t, eid); got != "# box plan\n" {
		t.Errorf("epic body = %q, want %q", got, "# box plan\n")
	}
	env := parseEnvelope(t, out)
	after, _ := env["after"].(map[string]any)
	if after == nil || after["id"] != eid {
		t.Errorf("envelope after should be the epic %s:\n%s", eid, out)
	}
}

// TestEditBodyUnknownIds: the miss falls on the side the ref suggests — task
// exit 1, epic-shaped exit 2 (the note/edit routing contract).
func TestEditBodyUnknownIds(t *testing.T) {
	initStore(t)
	if fe, out := runErrIn(t, "x\n", "edit", "t-nope0", "--body", "-"); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("unknown task id want exit 1, got %+v:\n%s", fe, out)
	}
	if fe, out := runErrIn(t, "x\n", "edit", "e-nope0", "--body", "-"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("unknown epic-shaped id want exit 2, got %+v:\n%s", fe, out)
	}
}

// TestEditExpectUpdatedNeedsBody: the stale-read guard describes a write; the
// editor/path arm never writes the shard, so a set flag there is exit 2 rather
// than silently meaningless.
func TestEditExpectUpdatedNeedsBody(t *testing.T) {
	initStore(t)
	id := addTask(t, "guarded")
	out, code := run(t, "edit", id, "--expect-updated", "2026-08-10T00:00:00Z")
	if code != int(core.CodeValidation) {
		t.Errorf("edit --expect-updated without --body want exit 2, got %d:\n%s", code, out)
	}
}

// TestEditBodyStaleReadGuard: --expect-updated rides the replacement write and
// warns via stale_read when the entity moved since the caller's read.
func TestEditBodyStaleReadGuard(t *testing.T) {
	initStore(t)
	id := addTask(t, "raced")
	out, code := run(t, "--json", "edit", id, "--body", "v2", "--expect-updated", "2000-01-01T00:00:00Z")
	if code != 0 {
		t.Fatalf("guarded edit --body exit = %d:\n%s", code, out)
	}
	env := parseEnvelope(t, out)
	if _, ok := env["stale_read"]; !ok {
		t.Errorf("a mismatched --expect-updated must surface stale_read:\n%s", out)
	}
}
