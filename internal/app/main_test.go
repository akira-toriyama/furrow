package app

import (
	"os"
	"testing"
)

// TestMain isolates every test in this package from the developer's real
// ~/.config/furrow/config.toml. Pointing XDG_CONFIG_HOME at an empty dir makes
// globalConfigPath resolve to a nonexistent file, so anything that consults the
// home config (discovery's central-board arm, lint's global-config warnings) is
// a clean no-op unless a test opts in with its own t.Setenv("XDG_CONFIG_HOME",…).
// Without this, lint reading the live machine config would be non-deterministic.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "furrow-xdg-app-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
