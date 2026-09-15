package gitrepo

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates this package's real-git tests from the developer's ambient
// git config (gpgsign/hooksPath/templateDir flakes) and pins init.defaultBranch
// to main — see internal/gittest.
func TestMain(m *testing.M) { gittest.Main(m) }
