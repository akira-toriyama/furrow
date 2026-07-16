package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// initHealthyMachine writes a user config with ONE live board (house layout:
// the board in a child checkout of the scope) and returns (scope, store).
func initHealthyMachine(t *testing.T) (scope, store string) {
	t.Helper()
	root := t.TempDir()
	scope = filepath.Join(root, "org")
	if _, err := app.Init(filepath.Join(scope, "projects")); err != nil {
		t.Fatal(err)
	}
	store = filepath.Join(scope, "projects", app.DirName)

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "furrow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[[board]]\npath = \"" + store + "\"\nscopes = [\"" + scope + "\"]\nrepo = \"auto\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return scope, store
}

func TestDoctorCLIHealthyMachineExitsZero(t *testing.T) {
	scope, store := initHealthyMachine(t)
	checkout := filepath.Join(scope, "repo1")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)

	got, code := run(t, "--json", "doctor")
	if code != 0 {
		t.Fatalf("a healthy machine must exit 0, got %d: %s", code, got)
	}
	var rep struct {
		Config      string           `json:"config"`
		EnvDir      *string          `json:"env_furrow_dir"`
		EnvBoard    *string          `json:"env_furrow_board"`
		Boards      []map[string]any `json:"boards"`
		Resolutions []map[string]any `json:"resolutions"`
		Problems    []map[string]any `json:"problems"`
		Healthy     *bool            `json:"healthy"`
	}
	if err := json.Unmarshal([]byte(got), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Config == "" || rep.EnvDir == nil || rep.EnvBoard == nil || rep.Healthy == nil || !*rep.Healthy {
		t.Fatalf("report must carry every key and healthy=true: %s", got)
	}
	if len(rep.Boards) != 1 || rep.Boards[0]["store"] != store {
		t.Fatalf("boards = %v, want the one live board", rep.Boards)
	}
	if _, ok := rep.Boards[0]["git"]; !ok {
		t.Errorf("each board must carry the git column: %v", rep.Boards[0])
	}
	if len(rep.Resolutions) != 1 || rep.Resolutions[0]["resolved"] != true || rep.Resolutions[0]["source"] != "user-config" {
		t.Errorf("the cwd probe must resolve through user-config: %v", rep.Resolutions)
	}

	// Human mode names the store and says ok (info-only findings included).
	human, code := run(t, "doctor")
	if code != 0 || !strings.Contains(human, store) || !strings.Contains(human, "ok — ") {
		t.Errorf("human output incomplete (exit %d):\n%s", code, human)
	}

	// --ndjson: a report is one object, so one compact line.
	nd, code := run(t, "--ndjson", "doctor")
	if code != 0 || strings.Count(strings.TrimRight(nd, "\n"), "\n") != 0 {
		t.Errorf("--ndjson must be one compact line (exit %d): %q", code, nd)
	}
}

func TestDoctorCLIUnhealthyExitsOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config at all -> no-boards warn

	got, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("problems found must exit 1 (the health-check contract), got %d: %s", code, got)
	}
	if !strings.Contains(got, "no-boards") || !strings.Contains(got, "furrow config init") {
		t.Errorf("the finding and its fix must be printed before the exit: %s", got)
	}
}

func TestDoctorCLIAssertedDir(t *testing.T) {
	scope, _ := initHealthyMachine(t)
	outside := t.TempDir()

	// A dir inside the scope passes; the machine stays healthy.
	inside := filepath.Join(scope, "repo1")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, code := run(t, "doctor", inside); code != 0 {
		t.Fatalf("an in-scope dir must pass, got %d: %s", code, got)
	}

	// A dir outside every scope fails the assertion: exit 1 + dir-unresolved.
	got, code := run(t, "--json", "doctor", outside)
	if code != 1 || !strings.Contains(got, "dir-unresolved") {
		t.Fatalf("an unresolvable asserted dir must exit 1 with dir-unresolved, got %d: %s", code, got)
	}

	// A nonexistent dir argument is bad USAGE (exit 2), not a finding.
	if got, code := run(t, "doctor", filepath.Join(outside, "nope")); code != 2 {
		t.Fatalf("a typo'd dir is exit 2, got %d: %s", code, got)
	}
}
