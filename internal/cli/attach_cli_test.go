package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// TestCLIArchiveMovesAssets pins t-j2e8: `furrow attach`ed media travels with its
// task into .furrow/archive/ instead of being orphaned in the hot store.
func TestCLIArchiveMovesAssets(t *testing.T) {
	initStore(t)
	id := addTask(t, "has media", "-s", "ready")
	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte{0x89, 'P', 'N', 'G', 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, "attach", id, src); code != 0 {
		t.Fatal("attach failed")
	}

	dir := os.Getenv(app.EnvDir)
	name := id + "-shot.png"
	hot := filepath.Join(dir, "bodies", "assets", name)
	arc := filepath.Join(dir, "archive", "bodies", "assets", name)
	if _, err := os.Stat(hot); err != nil {
		t.Fatalf("asset should exist in the hot store before archive: %v", err)
	}

	run(t, "done", id)
	if _, code := run(t, "archive", id, "--yes"); code != 0 {
		t.Fatal("archive by id failed")
	}

	if _, err := os.Stat(hot); !os.IsNotExist(err) {
		t.Errorf("asset should be gone from the hot store after archive, err = %v", err)
	}
	if _, err := os.Stat(arc); err != nil {
		t.Errorf("asset should be moved into archive/, err = %v", err)
	}
	// the whole point: lint no longer flags an orphan asset.
	out, _ := run(t, "--json", "lint")
	if strings.Contains(out, "orphan-asset") {
		t.Errorf("lint must not flag an orphan asset after the move:\n%s", out)
	}
}

func TestCLIAttachUpdatesBodyAndJSON(t *testing.T) {
	initStore(t)
	id := addTask(t, "bug with a picture", "-s", "ready")

	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte{0x89, 'P', 'N', 'G', 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, "--json", "attach", id, src)
	if code != 0 {
		t.Fatalf("attach exit = %d:\n%s", code, out)
	}
	var res struct {
		ID    string `json:"id"`
		Asset string `json:"asset"`
		Ref   string `json:"ref"`
		Line  string `json:"line"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse attach --json: %v\n%s", err, out)
	}
	wantName := id + "-shot.png"
	if res.ID != id {
		t.Errorf("id = %q, want %q", res.ID, id)
	}
	if res.Asset != "bodies/assets/"+wantName {
		t.Errorf("asset = %q, want bodies/assets/%s", res.Asset, wantName)
	}
	if res.Ref != "assets/"+wantName {
		t.Errorf("ref = %q, want assets/%s", res.Ref, wantName)
	}
	if res.Line != "![shot.png](assets/"+wantName+")" {
		t.Errorf("line = %q", res.Line)
	}

	// the reference actually landed in the body
	show, code := run(t, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, show)
	}
	if !strings.Contains(show, res.Ref) {
		t.Errorf("body does not reference the asset:\n%s", show)
	}

	// the asset file exists on disk under the store
	assetFile := filepath.Join(os.Getenv(app.EnvDir), "bodies", "assets", wantName)
	if _, err := os.Stat(assetFile); err != nil {
		t.Errorf("asset file missing on disk: %v", err)
	}
}

func TestCLIAttachErrors(t *testing.T) {
	initStore(t)
	id := addTask(t, "real task")
	src := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// unknown id -> not found (exit 1)
	if _, code := run(t, "attach", "t-99999", src); code != 1 {
		t.Errorf("unknown id exit = %d, want 1", code)
	}
	// missing source file -> validation (exit 2)
	if _, code := run(t, "attach", id, filepath.Join(t.TempDir(), "nope.png")); code != 2 {
		t.Errorf("missing file exit = %d, want 2", code)
	}
}
