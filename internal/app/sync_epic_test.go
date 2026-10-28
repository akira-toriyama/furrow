package app

import (
	"context"
	"testing"
	"time"
)

// A sync publishing an `epic activate` reports the switch in its progress —
// the record's exit point. Real-git e2e (the setupClones harness), because the
// capture reads the unpushed commits (@{upstream}..HEAD), which no memstore
// can fake. The clock is stopped at a known UTC instant and the machine zone
// moved off UTC, so `at` must come back as the recorded UTC stamp, which
// `sync --help` promises, not a local rendering of it.
func TestSyncReportsPublishedEpicSwitches(t *testing.T) {
	orig := time.Local
	time.Local = jst
	t.Cleanup(func() { time.Local = orig })
	_, cloneA, _ := setupClones(t)
	a := openBoard(t, cloneA)
	a.Clock = &fixedClock{t: time.Date(2026, 10, 28, 8, 51, 0, 0, time.UTC)}
	id := mustEpic(t, a, "focus box", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, _, err := a.EpicActivate(id, "session start"); err != nil {
		t.Fatal(err)
	}

	p, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("sync: %v (progress %+v)", err, p)
	}
	if len(p.Switches) != 1 {
		t.Fatalf("switches = %+v, want exactly the one activation", p.Switches)
	}
	sw := p.Switches[0]
	if sw.Epic != id || sw.Title != "focus box" || sw.Reason != "session start" || sw.At != "2026-10-28 08:51" {
		t.Errorf("switch = %+v, want epic %s titled, the recorded reason, and the UTC stamp 2026-10-28 08:51", sw, id)
	}

	// Once published, the record must not re-report on every later sync — it
	// belongs to the session that made it.
	p2, err := a.Sync(context.Background(), SyncOpts{})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(p2.Switches) != 0 {
		t.Errorf("an already-published switch re-reported: %+v", p2.Switches)
	}
}

// LintErrorCounts is a.Lint() plus counting: errors only, keyed by code — the
// numbers the sync summary prints. A participating board (>=1 epic) with an
// open unfiled task must show epic-required here, because surfacing that state
// in the loop that always runs is the whole point of the ride-along.
func TestLintErrorCountsCountsErrorsByCode(t *testing.T) {
	a := newApp()
	mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	if _, err := a.Add("open and unfiled", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	sum, err := a.LintErrorCounts()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Codes["epic-required"] == 0 {
		t.Errorf("counts = %+v, want epic-required counted as an error", sum)
	}
	if sum.Errors == 0 {
		t.Errorf("errors = 0, want the total to include every counted code")
	}
}
