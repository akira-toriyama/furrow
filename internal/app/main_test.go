package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates every test in this package from the developer's real
// ~/.config/furrow/config.toml, the Claude Code session env, and the ambient
// git config — see gittest.MainIsolated for the three axes.
func TestMain(m *testing.M) { gittest.MainIsolated(m) }
