package app

import (
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// The one way an app test builds its App. Twenty constructors used to spell
// the same three lines with a different clock, zone or config edit, and
// nineteen of them hard-coded the epic id prefix instead of reading it from
// the config — a test changing [ids].epic_prefix would have built a store
// that disagreed with its config.

// fixedClock is the Clock every app test runs under; tests advance it through
// the pointer newAppWith returns.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

// jst is the operator zone the due/repeat tests assume (UTC+9, no DST).
var jst = time.FixedZone("JST", 9*60*60)

// defaultNow is the clock a test gets unless it asks for another: a weekday
// noon, so a bare-date due neither starts out overdue nor lands on today.
var defaultNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

// appOpt tunes newAppWith.
type appOpt func(*appSpec)

type appSpec struct {
	now  time.Time
	cfg  []func(*config.Config)
	loc  *time.Location
	seed bool
}

// at sets the clock's starting instant.
func at(now time.Time) appOpt { return func(s *appSpec) { s.now = now } }

// zone sets the board calendar ([due].timezone).
func zone(loc *time.Location) appOpt { return withCfg(func(c *config.Config) { c.DueTimezone = loc }) }

// machineZone sets App.Loc — the zone of the machine, which a board that
// declares none falls back to.
func machineZone(loc *time.Location) appOpt { return func(s *appSpec) { s.loc = loc } }

// withCfg edits the shipped config before the App and its store are built.
func withCfg(edit func(*config.Config)) appOpt {
	return func(s *appSpec) { s.cfg = append(s.cfg, edit) }
}

// seeded makes the store mint sequential ids (t-0001, …) so a test can name
// them in advance.
func seeded() appOpt { return func(s *appSpec) { s.seed = true } }

// newStore is the memstore every app test writes to, built from the config's
// own id settings so the two can never disagree.
func newStore(cfg *config.Config) *memstore.Store {
	return memstore.New(cfg.IDPrefix, cfg.EpicIDPrefix, cfg.IDWidth)
}

// newAppWith builds a memstore App from the shipped config plus opts and
// returns the clock it runs under.
func newAppWith(opts ...appOpt) (*App, *fixedClock) {
	spec := appSpec{now: defaultNow}
	for _, o := range opts {
		o(&spec)
	}
	cfg := config.Default()
	for _, edit := range spec.cfg {
		edit(cfg)
	}
	st := newStore(cfg)
	if spec.seed {
		st.SeedSequentialIDs()
	}
	clk := &fixedClock{t: spec.now}
	a := NewWithStore(st, cfg, clk)
	if spec.loc != nil {
		a.Loc = spec.loc
	}
	return a, clk
}

// newApp is the plain fixture: shipped config, default clock.
func newApp() *App { a, _ := newAppWith(); return a }

// newDueApp runs at now on a JST machine with no board zone declared — the
// due tests' fixture.
func newDueApp(now time.Time) *App { a, _ := newAppWith(at(now), machineZone(jst)); return a }

// newRepeatApp runs at now on a board whose calendar is JST — the repeat
// tests' fixture (a shared board must declare its zone).
func newRepeatApp(now time.Time) *App { a, _ := newAppWith(at(now), zone(jst)); return a }

// newSeededApp mints sequential ids — the apply tests name ids in advance.
func newSeededApp() *App { a, _ := newAppWith(seeded()); return a }

// revisitApp starts on 2026-01-01 so the staleness clocks have room to run.
func revisitApp() (*App, *fixedClock) {
	return newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)))
}

// ptr is the pointer literal every optional field takes (SetOpts.Value,
// EpicSetOpts.Standing, a *time.Time, …).
func ptr[T any](v T) *T { return &v }

// idsOf projects any task-bearing read result to its ids, in order. A tree
// group with no epic (the unfiled group) reads as "(none)".
func idsOf[T any](xs []T) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		switch v := any(x).(type) {
		case core.Task:
			out[i] = v.ID
		case ListItem:
			out[i] = v.Task.ID
		case ShowItem:
			out[i] = v.Task.ID
		case TreeGroup:
			if v.Epic == nil {
				out[i] = "(none)"
			} else {
				out[i] = v.Epic.ID
			}
		default:
			panic("idsOf: unsupported element type")
		}
	}
	return out
}
