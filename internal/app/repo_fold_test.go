package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/config"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A repo name compares case-insensitively everywhere a repo is matched; the
// active-slot check compared exactly, so `O/R` and `o/r` held two active
// boxes for one repo — activate accepted the second and next admitted both.
func TestEpicActivateFoldsRepoCase(t *testing.T) {
	a := newApp()
	upper, err := a.EpicAdd("upper", EpicAddOpts{Repos: []string{"O/R"}})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := a.EpicAdd("lower", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.EpicActivate(upper.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = a.EpicActivate(lower.ID, "")
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindEpicActiveClash {
		t.Fatalf("activating o/r beside active O/R = %v; want epic-active-clash", err)
	}
}

// The repo-review clock matches a scope spelled in another case.
func TestUnreviewedReposFoldCase(t *testing.T) {
	a, _ := newAppWith(withCfg(func(c *config.Config) { c.ReviewStaleAfterDays = 1 }))
	if _, err := a.ReviewRepo("o/r", false); err != nil {
		t.Fatal(err)
	}
	later := a.Clock.Now().Add(72 * time.Hour)
	got, err := a.unreviewedRepos(QueryOpts{Repo: "O/R"}, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.EqualFold(got[0].Repo, "o/r") {
		t.Errorf("unreviewed under -r O/R = %+v; want the o/r record", got)
	}
}
