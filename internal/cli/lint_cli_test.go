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

// End-to-end asset-lint over a real on-disk fsstore board: exercises
// fsstore.ListAssets + the assets/ ref scan through the full lint --json path.

func TestCLILintFlagsDanglingAsset(t *testing.T) {
	initStore(t)
	id := addTask(t, "refs a missing asset")

	// Point the body at an asset that was never attached (nothing on disk).
	bodyPath := filepath.Join(os.Getenv(app.EnvDir), "bodies", id+".md")
	body := "# t\n\n![shot](assets/" + id + "-missing.png)\n"
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, "--json", "lint")
	if code != int(core.CodeOK) {
		t.Fatalf("a dangling asset ref is warn-only; want exit 0, got %d:\n%s", code, out)
	}
	var ps []core.Problem
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("parse lint --json: %v\n%s", err, out)
	}
	found := false
	for _, p := range ps {
		if p.Severity == core.SevWarn && strings.Contains(p.Msg, id+"-missing.png") && strings.Contains(p.Msg, "is missing") {
			found = true
			// t-kx76 (d): every problem carries a stable kebab-case code for
			// machine triage, so an agent branches on the array, not the prose.
			if p.Code != "asset-missing" {
				t.Errorf("dangling-asset problem should have code=asset-missing, got %q", p.Code)
			}
		}
	}
	if !found {
		t.Errorf("lint --json did not surface the dangling-asset warn:\n%s", out)
	}
}

// A body left half-merged is broken data on the board, so lint FAILS on it (exit
// 2) — the whole point being that the last time this happened, a marker-carrying
// body was committed and nobody found out.
func TestCLILintFailsOnConflictMarkerBody(t *testing.T) {
	initStore(t)
	id := addTask(t, "half-merged body")

	bodyPath := filepath.Join(os.Getenv(app.EnvDir), "bodies", id+".md")
	body := "# t\n\n<<<<<<< Updated upstream\n- [x] shipped\n=======\n- [ ] still writing\n>>>>>>> Stashed changes\n"
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, "--json", "lint")
	if code != int(core.CodeValidation) {
		t.Fatalf("a conflict-marker body must fail lint (exit 2), got %d:\n%s", code, out)
	}
	var ps []core.Problem
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("parse lint --json: %v\n%s", err, out)
	}
	found := false
	for _, p := range ps {
		if p.Code == "conflict-marker" {
			found = true
			if p.Severity != core.SevError || p.ID != id {
				t.Errorf("want an error blamed on %s, got %+v", id, p)
			}
		}
	}
	if !found {
		t.Errorf("lint --json did not surface conflict-marker:\n%s", out)
	}
}

// TestCLILintRuleCode pins that lint problems carry a stable kebab-case `code`
// (t-kx76 d): induce a dangling [[id]] link and assert code=dangling-link, so
// agent triage branches on the code, not an English regex.
func TestCLILintRuleCode(t *testing.T) {
	initStore(t)
	addTask(t, "haslink", "--body", "see [[t-zzzzz]]")

	out, code := run(t, "--json", "lint")
	if code != int(core.CodeOK) {
		t.Fatalf("a dangling link is warn-only; want exit 0, got %d:\n%s", code, out)
	}
	var ps []core.Problem
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("parse lint --json: %v\n%s", err, out)
	}
	found := false
	for _, p := range ps {
		if p.Code == "dangling-link" {
			found = true
			if !strings.Contains(p.Msg, "t-zzzzz") {
				t.Errorf("dangling-link message should name the target: %q", p.Msg)
			}
		}
	}
	if !found {
		t.Errorf("lint --json should carry a dangling-link code:\n%s", out)
	}
}

func TestCLILintCleanAfterAttach(t *testing.T) {
	initStore(t)
	id := addTask(t, "clean attach")

	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, "attach", id, src); code != 0 {
		t.Fatalf("attach exit = %d:\n%s", code, out)
	}

	out, code := run(t, "--json", "lint")
	if code != int(core.CodeOK) {
		t.Fatalf("a small referenced asset must keep lint at exit 0, got %d:\n%s", code, out)
	}
	var ps []core.Problem
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("parse lint --json: %v\n%s", err, out)
	}
	for _, p := range ps {
		if strings.Contains(p.Msg, "asset") {
			t.Errorf("a cleanly-attached asset must not be flagged: %q", p.Msg)
		}
	}
}
