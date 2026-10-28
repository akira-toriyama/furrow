package cli

import (
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
)

// epicSwitchLine shows the UTC activation stamp in local time with its offset,
// as humanTime renders every stored instant: printed bare, a JST operator read
// the switch nine hours early (t-8gmb). A stamp that is no real time is shown
// as written, never dropped.
func TestEpicSwitchLineShowsLocalTime(t *testing.T) {
	pinLocalTZ(t, 9*3600)

	got := epicSwitchLine(app.EpicSwitch{Epic: "e-k3m9p", Title: "box", At: "2026-10-28 08:51", Reason: "why"})
	if want := `epic: activated e-k3m9p "box" 2026-10-28 17:51 +09:00 — why`; got != want {
		t.Errorf("epicSwitchLine = %q, want %q", got, want)
	}
	got = epicSwitchLine(app.EpicSwitch{Epic: "e-k3m9p", Title: "box", At: "2026-13-01 25:00"})
	if want := `epic: activated e-k3m9p "box" 2026-13-01 25:00`; got != want {
		t.Errorf("unparsable stamp: epicSwitchLine = %q, want %q", got, want)
	}
}
