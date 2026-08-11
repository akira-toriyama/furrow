package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// t-z36a, whole stack: ONE wrong-typed value in the board's config.toml used to
// make every board command exit 2 — including `furrow board` (the diagnosis)
// and `furrow config set` (the repair). It now clamps: every command runs, lint
// warns config-clamp with file:line and the dotted key, and config set can
// write the fix.
func TestCLIWrongTypedConfigValueClamps(t *testing.T) {
	initStore(t)
	cfg := filepath.Join(os.Getenv("FURROW_DIR"), "config.toml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	broken := regexp.MustCompile(`(?m)^step = 10$`).ReplaceAll(data, []byte(`step = "10"`))
	if string(broken) == string(data) {
		t.Fatal("fixture rot: config template no longer carries `step = 10`")
	}
	if err := os.WriteFile(cfg, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"board"}, {"ls"}, {"brief"}, {"next"}, {"stats"}, {"lint"},
	} {
		if out, code := run(t, args...); code != 0 {
			t.Errorf("%v must survive a wrong-typed config value, exit %d:\n%s", args, code, out)
		}
	}
	out, code := run(t, "lint", "--json")
	if code != 0 || !strings.Contains(out, "wrong type") || !strings.Contains(out, "priority.step") {
		t.Errorf("lint must carry the clamp warning naming the key, got (exit %d):\n%s", code, out)
	}

	// The repair repairs: config set writes the typed value, lint goes quiet.
	if out, code := run(t, "config", "set", "priority.step", "10"); code != 0 {
		t.Fatalf("config set must survive and fix, exit %d:\n%s", code, out)
	}
	if out, _ := run(t, "lint"); strings.Contains(out, "wrong type") {
		t.Errorf("after the repair the warning must be gone:\n%s", out)
	}
}
