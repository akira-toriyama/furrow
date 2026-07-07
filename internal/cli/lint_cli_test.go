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
		}
	}
	if !found {
		t.Errorf("lint --json did not surface the dangling-asset warn:\n%s", out)
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
