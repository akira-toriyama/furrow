package cli

import (
	"os"
	"testing"
)

// TestMain isolates every test in this package from the developer's real
// ~/.config/furrow/config.toml (see the app package's TestMain for why). The
// `furrow config` commands and lint consult the home config; an empty
// XDG_CONFIG_HOME keeps that a no-op unless a test sets its own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "furrow-xdg-cli-*")
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
