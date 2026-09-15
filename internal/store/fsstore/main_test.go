package fsstore

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates this package's real-git tests (conflict_test.go drives real
// merges) from the developer's ambient git config — see internal/gittest.
func TestMain(m *testing.M) { gittest.Main(m) }
