package cli

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates every test in this package the way the app package's is —
// see gittest.MainIsolated.
func TestMain(m *testing.M) { gittest.MainIsolated(m) }
