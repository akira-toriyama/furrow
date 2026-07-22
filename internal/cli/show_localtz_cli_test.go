package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// pinLocalTZ fixes the display zone so local-TZ assertions are deterministic
// regardless of the host (the suite has no other TZ-pinning mechanism, and
// humanTime renders via time.Local).
func pinLocalTZ(t *testing.T, offsetSecs int) {
	t.Helper()
	orig := time.Local
	time.Local = time.FixedZone("TST", offsetSecs)
	t.Cleanup(func() { time.Local = orig })
}

// TestShowHumanTimeIsLocalTZ is the t-1x7s fix: `show`'s human created/updated
// render in the viewer's local TZ with an explicit offset (so they line up with
// git log), not a bare UTC time that reads as a phantom midnight.
func TestShowHumanTimeIsLocalTZ(t *testing.T) {
	pinLocalTZ(t, 9*3600) // +09:00
	initStore(t)
	id := addTask(t, "tz task")

	out, code := run(t, "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "created:") || !strings.Contains(out, "+09:00") {
		t.Errorf("human show should render created/updated in local TZ with offset (+09:00):\n%s", out)
	}
}

// TestShowJSONTimeStaysUTC pins that the machine contract is untouched: --json
// created stays UTC RFC3339 (Z), independent of the local display zone.
func TestShowJSONTimeStaysUTC(t *testing.T) {
	pinLocalTZ(t, 9*3600)
	initStore(t)
	id := addTask(t, "utc task")

	out, code := run(t, "--json", "show", id)
	if code != 0 {
		t.Fatalf("show --json exit = %d:\n%s", code, out)
	}
	var task struct {
		Created string `json:"created"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("parse show --json: %v\n%s", err, out)
	}
	if !strings.HasSuffix(task.Created, "Z") {
		t.Errorf("--json created must stay UTC (RFC3339 Z), got %q", task.Created)
	}
}
