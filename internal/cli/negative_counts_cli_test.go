package cli

import (
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A negative count is a typo, not "all": -n and --older-than refuse it
// (t-jty7: `-n -1` listed everything and `--older-than -5` swept "closed
// >-5d ago").
func TestNegativeCountsAreRefused(t *testing.T) {
	initStore(t)
	addTask(t, "x")
	for _, args := range [][]string{
		{"ls", "-n", "-1"},
		{"next", "-n", "-1"},
		{"archive", "--older-than", "-5", "--yes"},
	} {
		fe, _, _ := execCLI(t, "", args...)
		if fe == nil || fe.Kind != core.KindValidation {
			t.Errorf("%v: want validation exit 2, got %+v", args, fe)
		}
	}
}
