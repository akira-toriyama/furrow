package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/app"
)

// `epic set --title` on a real store: the seeded heading follows the rename
// on disk, a hand-written one is left, and the envelope names `title` both
// times — the body is not a shard field.
func TestEpicSetTitleCarriesTheHeadingOnDisk(t *testing.T) {
	initStore(t)
	box := addEpic(t, "old box", "-r", "o/r") // body seeded "# old box\n"
	bodyPath := filepath.Join(os.Getenv(app.EnvDir), "bodies", box+".md")
	rename := func(title string) {
		t.Helper()
		out, code := run(t, "--json", "epic", "set", box, "--title", title)
		if code != 0 {
			t.Fatalf("epic set --title exit=%d:\n%s", code, out)
		}
		var env struct {
			Changed []string `json:"changed"`
			After   struct {
				Title string `json:"title"`
			} `json:"after"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("parse: %v\n%s", err, out)
		}
		if env.After.Title != title || len(env.Changed) != 1 || env.Changed[0] != "title" {
			t.Errorf("after.title = %q, changed = %v", env.After.Title, env.Changed)
		}
	}
	wantBody := func(want string) {
		t.Helper()
		body, err := os.ReadFile(bodyPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want {
			t.Errorf("body = %q, want %q", string(body), want)
		}
	}

	rename("new box")
	wantBody("# new box\n")

	if err := os.WriteFile(bodyPath, []byte("# short name\n\nprose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rename("newer box")
	wantBody("# short name\n\nprose\n")
}
