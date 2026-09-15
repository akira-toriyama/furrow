package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
)

// A series is expanded in the zone of whichever machine closes it when the
// board declares no [due].timezone — two calendars on a shared board, one of
// them a UTC CI runner. lint is the only place that can say so before the
// first successor lands a day off; a standalone board has one zone and a board
// with no repeating task has nothing that drifts.
func TestLintErrorsARepeatingSeriesWithNoBoardZone(t *testing.T) {
	newApp := func(mode string, loc *time.Location) *App {
		a, _ := newAppWith(at(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)), zone(loc),
			withCfg(func(c *config.Config) { c.Mode = mode }))
		return a
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

// handEditRepeat plants a rule+anchor straight into the shard, bypassing every
// write path that refuses the state. It is the ONLY way these two shapes are
// reachable, which is why lint is the only thing that can report them.
func handEditRepeat(t *testing.T, a *App, id, rule string, anchor *time.Time) {
	t.Helper()
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := idx.Find(id)
	if task == nil {
		t.Fatalf("task %s not on the board", id)
	}
	task.Repeat, task.RepeatAnchor = rule, anchor
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
}

// A close CONSUMES the rule and hands it to the successor, so a closed task
// carrying one has forked the series: the next reopen-then-close mints a second
// successor, and countRepeating (open carriers only) stops reporting it for
// repeat-no-timezone. Only a hand-edited shard can be in that state.
func TestLintErrorsAClosedTaskStillCarryingARepeatRule(t *testing.T) {
	setup := func(t *testing.T, mode string, loc *time.Location) (*App, *core.Task) {
		t.Helper()
		a, _ := newAppWith(at(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)), zone(loc),
			withCfg(func(c *config.Config) { c.Mode = mode }))
		return a, mustAddRepeating(t, a, "water the plants", "2026-03-01", "daily", AddOpts{})
	}

	t.Run("a close that consumed the rule is clean", func(t *testing.T) {
		a, pred := setup(t, config.ModeShared, jst)
		if _, _, err := a.moveOne(pred.ID, a.Cfg.DoneLane); err != nil {
			t.Fatal(err)
		}
		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		if got := problemsWithCode(ps, "repeat-on-closed"); len(got) != 0 {
			t.Fatalf("an ordinary close hands the rule on; got %+v", got)
		}
	})

	t.Run("a rule restored onto the closed predecessor", func(t *testing.T) {
		a, pred := setup(t, config.ModeShared, jst)
		rule, anchor := pred.Repeat, pred.RepeatAnchor
		if _, _, err := a.moveOne(pred.ID, a.Cfg.DoneLane); err != nil {
			t.Fatal(err)
		}
		handEditRepeat(t, a, pred.ID, rule, anchor)

		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		got := problemsWithCode(ps, "repeat-on-closed")
		if len(got) != 1 {
			t.Fatalf("repeat-on-closed findings = %d, want 1: %+v", len(got), got)
		}
		if got[0].Severity != core.SevError || got[0].ID != pred.ID {
			t.Errorf("finding = %+v, want an error on the predecessor", got[0])
		}
		for _, want := range []string{"--clear-repeat", "furrow move " + pred.ID + " " + a.Cfg.DefaultLane} {
			if !strings.Contains(got[0].Msg, want) {
				t.Errorf("message must name the fix %q: %s", want, got[0].Msg)
			}
		}
		// The successor is untouched: it is the one legitimate live occurrence.
		if n := len(problemsWithCode(ps, "repeat-invalid")); n != 0 {
			t.Errorf("a well-formed rule is not repeat-invalid; got %d", n)
		}
	})

	t.Run("it reddens the board the same hand-edit silenced", func(t *testing.T) {
		// No successor: the rule is planted back on the ONLY occurrence after it
		// was closed, so the board has zero open carriers and repeat-no-timezone —
		// which counts open carriers — goes quiet on a board that should be red.
		a, task := setup(t, config.ModeShared, nil)
		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		if n := len(problemsWithCode(ps, "repeat-no-timezone")); n != 1 {
			t.Fatalf("precondition: an open carrier on a zoneless shared board must error; got %d", n)
		}
		idx, err := a.Store.Load()
		if err != nil {
			t.Fatal(err)
		}
		closed := a.Clock.Now()
		found, _ := idx.Find(task.ID)
		found.Status, found.Closed = a.Cfg.DoneLane, &closed
		if err := a.Store.Save(idx); err != nil {
			t.Fatal(err)
		}

		ps, err = a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		if n := len(problemsWithCode(ps, "repeat-no-timezone")); n != 0 {
			t.Fatalf("countRepeating counts open carriers only; got %d", n)
		}
		if n := len(problemsWithCode(ps, "repeat-on-closed")); n != 1 {
			t.Fatalf("repeat-on-closed findings = %d, want 1: %+v", n, ps)
		}
		sum, err := a.LintErrorCounts()
		if err != nil {
			t.Fatal(err)
		}
		if sum.Codes["repeat-on-closed"] != 1 {
			t.Errorf("the sync ride-along must carry the error too: %+v", sum)
		}
	})
}

// repeat_anchor with no rule is INERT inside furrow (every reader short-circuits
// on an empty rule, and a rebind re-derives the anchor from the due), but the
// published shard schema says the anchor is present iff the rule is — so it is a
// broken promise to an external reader, and a warn rather than an error.
func TestLintWarnsAnOrphanRepeatAnchor(t *testing.T) {
	newBoard := func(t *testing.T) *App {
		t.Helper()
		return newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	}

	t.Run("a live series carries both and is clean", func(t *testing.T) {
		a := newBoard(t)
		mustAddRepeating(t, a, "water the plants", "2026-03-01", "daily", AddOpts{})
		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		if got := problemsWithCode(ps, "repeat-orphan-anchor"); len(got) != 0 {
			t.Fatalf("a bound anchor is not an orphan; got %+v", got)
		}
	})

	t.Run("an anchor with no rule warns and `set --clear-repeat` clears it", func(t *testing.T) {
		a := newBoard(t)
		task, err := a.Add("plain", AddOpts{Due: "2026-03-01"})
		if err != nil {
			t.Fatal(err)
		}
		anchor := *task.Due
		handEditRepeat(t, a, task.ID, "", &anchor)

		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		got := problemsWithCode(ps, "repeat-orphan-anchor")
		if len(got) != 1 {
			t.Fatalf("repeat-orphan-anchor findings = %d, want 1: %+v", len(got), ps)
		}
		if got[0].Severity != core.SevWarn || got[0].ID != task.ID {
			t.Errorf("finding = %+v, want a warn on the task", got[0])
		}
		if !strings.Contains(got[0].Msg, "furrow set "+task.ID+" --clear-repeat") {
			t.Errorf("message must name the fix: %s", got[0].Msg)
		}
		// The named fix has to actually work: --clear-repeat nils the anchor even
		// with no rule to drop, and lint goes quiet.
		if _, _, err := a.SetMany([]string{task.ID}, SetOpts{ClearRepeat: true}); err != nil {
			t.Fatalf("the fix the message names: %v", err)
		}
		ps, err = a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		if got := problemsWithCode(ps, "repeat-orphan-anchor"); len(got) != 0 {
			t.Fatalf("`set --clear-repeat` must clear the stray anchor; got %+v", got)
		}
	})
}

// A DTSTART cannot get in through `add`/`set` any more, so only a hand-edit or a
// foreign writer can put one on a shard — and the expander then ignores it
// forever while the shard keeps claiming it. lint is the only thing that can
// say so, which is why recur.Valid reads the stored LINE and not the rule the
// expander built from it.
func TestLintErrorsAStoredRuleCarryingADtstart(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC))
	task := mustAddRepeating(t, a, "watering", "2026-03-01", "daily", AddOpts{})

	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	bt, _ := idx.Find(task.ID)
	bt.Repeat = "FREQ=DAILY;DTSTART=20200101T000000Z" // a shard furrow would not write
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := problemsWithCode(ps, "repeat-invalid")
	if len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("repeat-invalid findings = %+v, want exactly one on %s", got, task.ID)
	}
	if got[0].Severity != core.SevError || !strings.Contains(got[0].Msg, "DTSTART") {
		t.Errorf("finding = %+v, want an error naming the DTSTART", got[0])
	}
}
