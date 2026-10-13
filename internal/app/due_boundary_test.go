package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// The shard's RFC3339 stamp holds years 1..9999 in UTC: a board-zone day at
// the edge (New York's 9999-12-31 ends in UTC's year 10000) is refused as the
// operator's input, not surfaced from the marshaller as internal (t-jty7).
func TestDueAtTheYearEdgeIsValidationInEveryZone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	a, _ := newAppWith(zone(ny))
	_, err = a.Add("edge", AddOpts{Due: "9999-12-31"})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindValidation {
		t.Fatalf("want validation, got %v", err)
	}
	ok, err := a.Add("inside", AddOpts{Due: "9999-12-30"})
	if err != nil {
		t.Fatalf("a due inside the range must bind: %v", err)
	}
	shift := "+1d"
	if _, _, _, err := a.Set(ok.ID, SetOpts{DueShift: &shift}); core.AsError(err) == nil || core.AsError(err).Kind != core.KindValidation {
		t.Fatalf("a shift past the edge must be validation, got %v", err)
	}
}

// Anchor day arithmetic counts calendar days, not time.Duration nanoseconds,
// which saturate at ±106751 days (~292 years).
func TestAnchorDaysDoNotSaturate(t *testing.T) {
	days, err := core.AnchorDays("1700-01-01", "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if days != 119069 {
		t.Fatalf("AnchorDays(1700-01-01, 2026-01-01) = %d, want 119069", days)
	}
	due := time.Date(2026, 1, 15, 23, 59, 59, 0, time.UTC)
	off, err := core.AnchorOffsetDays("1700-01-01", due, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if off != 119083 {
		t.Fatalf("AnchorOffsetDays = %d, want 119083", off)
	}
}
