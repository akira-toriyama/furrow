package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

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
