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

// setBoardSchema hand-writes meta.json — the board declaring a layout other than
// the one this binary writes.
func setBoardSchema(t *testing.T, v string) string {
	t.Helper()
	meta := filepath.Join(os.Getenv(app.EnvDir), "meta.json")
	if err := os.WriteFile(meta, []byte("{\n  \"schema_version\": "+v+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return meta
}

// End-to-end version gate: `furrow ls` against a board whose meta.json declares
// a future layout exits 3 (internal — the fix is a newer binary, not new args).
func TestLsRefusesNewerBoard(t *testing.T) {
	initStore(t)
	setBoardSchema(t, "99")

	fe, _ := runErr(t, "ls")
	if fe == nil || fe.Code != core.CodeInternal {
		t.Fatalf("ls on a v99 board: err = %v, want exit %d", fe, core.CodeInternal)
	}
	if fe.ID != "schema-too-new" {
		t.Errorf("id = %q, want schema-too-new (agents branch on the id)", fe.ID)
	}

	// Mutations are gated the same way (Load happens before any write).
	if fe, _ := runErr(t, "add", "nope"); fe == nil || fe.Code != core.CodeInternal {
		t.Errorf("add on a v99 board: err = %v, want exit %d", fe, core.CodeInternal)
	}
}

// The 2026-07-13 contract, end to end: a board one layout BEHIND this binary
// stays fully readable, but every write refuses — with exit 2 (the board is
// stale; an explicit command fixes it), not exit 3 (the binary is stale). The
// two are distinguishable by exit code alone, and both carry the two versions.
func TestReadsSurviveAnOutdatedBoard(t *testing.T) {
	initStore(t)
	if _, code := run(t, "add", "before the bump", "-s", "ready"); code != 0 {
		t.Fatalf("seed add: exit %d", code)
	}
	meta := setBoardSchema(t, "3")
	before, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"ls"}, {"next"}, {"board"}, {"lint"}} {
		if out, code := run(t, args...); code != 0 {
			t.Errorf("%v on an outdated board: exit = %d, want 0 — reads must degrade, not die\n%s", args, code, out)
		}
	}

	fe, _ := runErr(t, "add", "after the bump")
	if fe == nil {
		t.Fatal("add on an outdated board must refuse — it used to silently migrate it")
	}
	if fe.ID != "schema-upgrade-required" || fe.Code != core.CodeValidation {
		t.Errorf("err = {id:%q code:%d}, want {schema-upgrade-required 2}", fe.ID, fe.Code)
	}
	d, ok := fe.Details.(map[string]any)
	if !ok || d["board_schema"] != 3 || d["binary_schema"] != core.SchemaVersion {
		t.Errorf("details = %#v, want board_schema=3 binary_schema=%d", fe.Details, core.SchemaVersion)
	}

	after, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("meta.json = %q, want it untouched (%q) — no write may raise a board's layout", after, before)
	}
}

// `furrow board --json` is CI's pre-flight: it must answer even on a board no
// other command can open, so the fleet gets a one-line diagnosis instead of N
// mysterious "task not found"s.
func TestBoardJSONDiagnosesAnyBoard(t *testing.T) {
	initStore(t)
	for _, tt := range []struct {
		meta      string
		wantState string
		wantVer   float64
	}{
		{"99", "too-new", 99},
		{"3", "outdated", 3},
	} {
		setBoardSchema(t, tt.meta)
		out, code := run(t, "board", "--json")
		if code != 0 {
			t.Fatalf("board --json on a v%s board: exit = %d, want 0 (it is the last thing that still works)", tt.meta, code)
		}
		var b map[string]any
		if err := json.Unmarshal([]byte(out), &b); err != nil {
			t.Fatal(err)
		}
		if b["schema_state"] != tt.wantState || b["schema_version"] != tt.wantVer || b["writable"] != false {
			t.Errorf("board = %v, want schema_state=%s schema_version=%v writable=false", b, tt.wantState, tt.wantVer)
		}
	}
}

// `furrow upgrade` is the only raiser, and it previews first (the `archive`
// guard) — a flag day is never one keystroke away.
func TestUpgradeCLIPreviewsThenApplies(t *testing.T) {
	initStore(t)
	meta := setBoardSchema(t, "3")

	out, code := run(t, "upgrade")
	if code != 0 {
		t.Fatalf("upgrade preview: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "FLAG DAY") || !strings.Contains(out, "--yes") {
		t.Errorf("the preview must state the flag day and how to apply it:\n%s", out)
	}
	if b, _ := os.ReadFile(meta); !strings.Contains(string(b), "\"schema_version\": 3") {
		t.Errorf("the preview wrote to the board: %s", b)
	}

	out, code = run(t, "upgrade", "--yes", "--json")
	if code != 0 {
		t.Fatalf("upgrade --yes: exit %d\n%s", code, out)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep["changed"] != true || rep["applied"] != true {
		t.Errorf("report = %v, want changed:true applied:true", rep)
	}
	if _, code := run(t, "add", "now allowed"); code != 0 {
		t.Errorf("after the upgrade, writes must work: exit %d", code)
	}

	// Idempotent — safe to run any time.
	out, code = run(t, "upgrade", "--json")
	if code != 0 {
		t.Fatalf("upgrade on a current board: exit %d", code)
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep["changed"] != false {
		t.Errorf("a current board = %v, want changed:false", rep)
	}
}

// setStandalone marks the active board standalone by hand-writing its
// config.toml — the same one-line edit the docs tell a single-machine operator
// to make. Clamp-don't-reject fills every other key with its default.
func setStandalone(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(os.Getenv(app.EnvDir), "config.toml")
	if err := os.WriteFile(cfg, []byte("standalone = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// On a standalone board `furrow upgrade` drops the shared-board flag-day
// checklist and the `furrow sync` publish line: a single-machine board has no
// pinned CI to coordinate and no remote to publish to, so that guidance only
// misdirects. The gate itself is unchanged — the wording is all that differs.
func TestStandaloneUpgradeDropsFlagDay(t *testing.T) {
	initStore(t)
	setStandalone(t)
	setBoardSchema(t, "3")

	out, code := run(t, "upgrade")
	if code != 0 {
		t.Fatalf("standalone upgrade preview: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "FLAG DAY") || strings.Contains(out, "sync-task-status.yml") {
		t.Errorf("a standalone board has no flag day / CI pins to coordinate:\n%s", out)
	}
	if !strings.Contains(out, "standalone board") || !strings.Contains(out, "--yes") {
		t.Errorf("standalone preview must say it is standalone and how to apply:\n%s", out)
	}

	// Completion (human) drops the `furrow sync` publish line — nothing to publish.
	out, code = run(t, "upgrade", "--yes")
	if code != 0 {
		t.Fatalf("standalone upgrade --yes: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "furrow sync") {
		t.Errorf("a standalone board has nothing to publish:\n%s", out)
	}
	if !strings.Contains(out, "upgraded") {
		t.Errorf("completion must confirm the upgrade:\n%s", out)
	}
}

// The write-block error (schema-upgrade-required) is now CI-agnostic: it no
// longer carries the sync-task-status.yml / flag-day narrative that misled a
// standalone operator (who has no CI to coordinate). It simply points at `furrow
// upgrade`, which is where the board-type guidance — shared flag day vs
// standalone — now lives (see TestStandaloneUpgradeDropsFlagDay). This holds
// regardless of board type; only `furrow upgrade`'s own output differs.
func TestSchemaBlockIsCIAgnostic(t *testing.T) {
	initStore(t)
	setBoardSchema(t, "3")

	fe, _ := runErr(t, "add", "nope")
	if fe == nil || fe.ID != "schema-upgrade-required" {
		t.Fatalf("want schema-upgrade-required, got %+v", fe)
	}
	if !strings.Contains(fe.Msg, "furrow upgrade") {
		t.Errorf("the block must point at the fix (`furrow upgrade`): %q", fe.Msg)
	}
	if strings.Contains(fe.Msg, "sync-task-status.yml") || strings.Contains(fe.Msg, "flag day") {
		t.Errorf("the block must not carry CI / flag-day prose (it misled standalone boards): %q", fe.Msg)
	}
}
