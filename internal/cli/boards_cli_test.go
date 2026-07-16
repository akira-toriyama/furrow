package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// initGlobalConfig points XDG_CONFIG_HOME at a fresh dir, creates one real
// board, and writes a user-level config naming it plus one missing board.
// Returns the real store path.
func initGlobalConfig(t *testing.T) string {
	t.Helper()
	boardParent := t.TempDir()
	if _, err := app.Init(boardParent); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(boardParent, app.DirName)

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "furrow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `
[[board]]
path   = "` + real + `"
scopes = ["` + boardParent + `"]
repo   = "auto"

[[board]]
path   = "` + filepath.Join(boardParent, "gone", ".furrow") + `"
scopes = ["/somewhere"]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return real
}

func TestBoardsCLIListsAndSharesBoardKeys(t *testing.T) {
	real := initGlobalConfig(t)

	got, code := run(t, "--json", "boards")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, got)
	}
	var list struct {
		Config string           `json:"config"`
		Boards []map[string]any `json:"boards"`
	}
	if err := json.Unmarshal([]byte(got), &list); err != nil {
		t.Fatal(err)
	}
	if list.Config == "" || len(list.Boards) != 2 {
		t.Fatalf("want config + 2 boards, got: %s", got)
	}
	live := list.Boards[0]
	if live["store"] != real || live["exists"] != true {
		t.Errorf("live entry store/exists wrong: %v", live)
	}
	// The vocabulary and schema keys are `board --json`'s own — flat, same
	// names — so one parser reads both views.
	for _, k := range []string{"lanes", "next_lanes", "default_lane", "done_lane", "terminal", "types", "schema_version", "binary_schema_version", "schema_state", "writable"} {
		if _, ok := live[k]; !ok {
			t.Errorf("boards entry missing shared board key %q", k)
		}
	}
	dead := list.Boards[1]
	if dead["exists"] != false || dead["schema_state"] != app.SchemaUnreadable {
		t.Errorf("missing board must read exists=false + unreadable: %v", dead)
	}

	// Human mode names the config and each store.
	human, code := run(t, "boards")
	if code != 0 || !strings.Contains(human, "config: ") || !strings.Contains(human, real) || !strings.Contains(human, "missing (not on disk)") {
		t.Errorf("human output incomplete (exit %d):\n%s", code, human)
	}

	// --ndjson: a single-object command prints one compact line.
	nd, code := run(t, "--ndjson", "boards")
	if code != 0 || strings.Count(strings.TrimRight(nd, "\n"), "\n") != 0 {
		t.Errorf("--ndjson must be one compact line (exit %d): %q", code, nd)
	}
}

func TestBoardsCLIWithNoConfigExitsZero(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got, code := run(t, "--json", "boards")
	if code != 0 {
		t.Fatalf("no config must be an empty listing, not a failure: exit %d: %s", code, got)
	}
	var list struct {
		Boards []any `json:"boards"`
	}
	if err := json.Unmarshal([]byte(got), &list); err != nil {
		t.Fatal(err)
	}
	if list.Boards == nil || len(list.Boards) != 0 {
		t.Errorf("boards = %#v, want empty non-null array", list.Boards)
	}
}
