package app

import (
	"os"
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates every test in this package on two axes. First, the
// developer's real ~/.config/furrow/config.toml: pointing XDG_CONFIG_HOME at an
// empty dir makes globalConfigPath resolve to a nonexistent file, so anything
// that consults the home config (discovery's central-board arm, lint's
// global-config warnings) is a clean no-op unless a test opts in with its own
// t.Setenv("XDG_CONFIG_HOME",…). Second, the developer's ambient git config:
// App.Sync's git subprocesses inherit os.Environ, so gittest.Isolate pins a
// throwaway global (init.defaultBranch=main, gpgsign off) — otherwise a
// developer's gpgsign/hooksPath would flake the sync tests. Without either,
// tests reading the live machine state would be non-deterministic.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "furrow-xdg-app-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	restoreGit, err := gittest.Isolate()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	restoreGit()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
