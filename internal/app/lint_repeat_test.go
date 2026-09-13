package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// A series is expanded in the zone of whichever machine closes it when the
// board declares no [due].timezone — two calendars on a shared board, one of
// them a UTC CI runner. lint is the only place that can say so before the
// first successor lands a day off; a standalone board has one zone and a board
// with no repeating task has nothing that drifts.
func TestLintErrorsARepeatingSeriesWithNoBoardZone(t *testing.T) {
	newApp := func(mode string, loc *time.Location) *App {
		cfg := config.Default()
		cfg.Mode = mode
		cfg.DueTimezone = loc
		st := memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)
		return NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)})
	}
	cases := []struct {
		name      string
		mode      string
		loc       *time.Location
		repeating bool
		want      int
	}{
		{"shared, undeclared, repeating", config.ModeShared, nil, true, 1},
		{"shared, declared", config.ModeShared, jst, true, 0},
		{"standalone, undeclared", config.ModeStandalone, nil, true, 0},
		{"shared, undeclared, nothing repeats", config.ModeShared, nil, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(c.mode, c.loc)
			if c.repeating {
				mustAddRepeating(t, a, "水やり", "2026-03-01", "daily", AddOpts{})
			} else if _, err := a.Add("plain", AddOpts{Due: "2026-03-01"}); err != nil {
				t.Fatal(err)
			}
			ps, err := a.Lint()
			if err != nil {
				t.Fatal(err)
			}
			got := problemsWithCode(ps, "repeat-no-timezone")
			if len(got) != c.want {
				t.Fatalf("repeat-no-timezone findings = %d, want %d: %+v", len(got), c.want, got)
			}
			if c.want == 1 && (got[0].Severity != core.SevError || got[0].ID != "config" || !strings.Contains(got[0].Msg, "due.timezone")) {
				t.Errorf("finding = %+v, want an error on the config naming the fix", got[0])
			}
		})
	}

	t.Run("the live occurrence is what counts", func(t *testing.T) {
		a := newApp(config.ModeShared, nil)
		task := mustAddRepeating(t, a, "水やり", "2026-03-01", "daily", AddOpts{})
		if _, _, err := a.moveOne(task.ID, a.Cfg.DoneLane); err != nil {
			t.Fatal(err)
		}
		ps, _ := a.Lint()
		if got := problemsWithCode(ps, "repeat-no-timezone"); len(got) != 1 || !strings.HasPrefix(got[0].Msg, "1 repeating") {
			t.Errorf("after a close the successor is the one live occurrence; got %+v", got)
		}
	})
}
