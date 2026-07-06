package cli

import (
	"os"
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates every test in this package from the developer's real
// ~/.config/furrow/config.toml (see the app package's TestMain for why). The
// `furrow config` commands and lint consult the home config; an empty
// XDG_CONFIG_HOME keeps that a no-op unless a test sets its own. It also pins a
// throwaway global git config (gittest.Isolate) so the real-git sync smoke
// tests don't inherit the developer's gpgsign/hooksPath/init.defaultBranch.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "furrow-xdg-cli-*")
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
