package fsstore

import (
	"os"
	"testing"

	"github.com/akira-toriyama/furrow/internal/gittest"
)

// TestMain isolates this package's real-git tests (conflict_test.go drives real
// merges) from the developer's ambient git config and pins init.defaultBranch to
// main. The isolation has to happen at the process-env level because the git
// subprocesses inherit os.Environ — see internal/gittest.
func TestMain(m *testing.M) {
	restore, err := gittest.Isolate()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
