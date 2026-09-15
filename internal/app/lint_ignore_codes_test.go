package app

import (
	"testing"
	"time"
)

// [lint].ignore_codes is documented as suppressing a code on EVERY run, and
// LintErrorCounts as "exactly a.Lint() plus counting", so the sync/brief
// ride-along must not count an error `furrow lint` itself exits 0 on.
func TestLintIgnoreCodesReachesErrorCounts(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	a.Cfg.LintIgnoreCodes = []string{"due-overdue"}
	a.Add("late", AddOpts{Status: "ready", Due: "2026-08-01"}) //nolint:errcheck // asserted via lint below

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if over := problemsWithCode(ps, "due-overdue"); len(over) != 0 {
		t.Errorf("Lint still lists the ignored code: %+v", over)
	}
	sum, err := a.LintErrorCounts()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Codes["due-overdue"] != 0 || sum.Errors != 0 {
		t.Errorf("LintErrorCounts = %+v, want the ignored code absent from the ride-along", sum)
	}
}
