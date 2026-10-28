package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/config"
)

// zonedClock reports its instant in its own zone, as a Clock other than
// core.SystemClock may.
type zonedClock struct{ t time.Time }

func (c zonedClock) Now() time.Time { return c.t }

// recordSwitch stamps UTC whatever zone the Clock reports or the machine runs
// in: the stamp carries no zone suffix, so one zone on every writer is what
// keeps records from different machines comparable (t-8gmb). The machine zone
// is moved off UTC because CI runners sit in UTC, where a stamp written in
// local time would pass.
func TestRecordSwitchStampsUTC(t *testing.T) {
	orig := time.Local
	time.Local = time.FixedZone("EST", -5*60*60)
	t.Cleanup(func() { time.Local = orig })
	cfg := config.Default()
	a := NewWithStore(newStore(cfg), cfg, zonedClock{t: time.Date(2026, 6, 25, 19, 0, 0, 0, jst)})
	e, err := a.EpicAdd("box", EpicAddOpts{Repos: []string{"me/r1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.EpicActivate(e.ID, ""); err != nil {
		t.Fatal(err)
	}
	body, err := a.Store.LoadBody(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := latestActivation(body); got != "2026-06-25 10:00" {
		t.Errorf("stamp = %q, want the UTC wall clock 2026-06-25 10:00 (19:00 JST)", got)
	}
}

// SwitchStampTime reads a stamp as the UTC instant it records and refuses one
// that is no real time.
func TestSwitchStampTime(t *testing.T) {
	got, ok := SwitchStampTime("2026-10-28 08:51")
	if want := time.Date(2026, 10, 28, 8, 51, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("SwitchStampTime = %v, %v; want %v, true", got, ok, want)
	}
	if _, ok := SwitchStampTime("2026-13-01 25:00"); ok {
		t.Error("SwitchStampTime accepted a stamp that is no real time")
	}
}
