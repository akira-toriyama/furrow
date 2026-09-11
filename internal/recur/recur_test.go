package recur

import (
	"strings"
	"testing"
	"time"
)

var jst = time.FixedZone("JST", 9*3600)

// A fixed resolver, so the grammar tests never depend on furrow's date parser
// (that seam is the caller's — see Compile's doc).
func fixedDate(t time.Time) func(string) (time.Time, error) {
	return func(string) (time.Time, error) { return t, nil }
}

func TestCompileSpellings(t *testing.T) {
	cases := []struct {
		spec string
		want string
	}{
		{"daily", "FREQ=DAILY"},
		{"every 3 days", "FREQ=DAILY;INTERVAL=3"},
		{"weekly", "FREQ=WEEKLY"},
		{"weekly on mon,thu", "FREQ=WEEKLY;BYDAY=MO,TH"},
		{"every 2 weeks on mon,thu", "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH"},
		{"monthly", "FREQ=MONTHLY"},
		{"monthly on 15", "FREQ=MONTHLY;BYMONTHDAY=15"},
		{"monthly on 31", "FREQ=MONTHLY;BYMONTHDAY=31"},
		{"monthly on last", "FREQ=MONTHLY;BYMONTHDAY=-1"},
		{"monthly on 2nd tue", "FREQ=MONTHLY;BYDAY=+2TU"},
		{"monthly on last fri", "FREQ=MONTHLY;BYDAY=-1FR"},
		{"every 3 months on 15", "FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=15"},
		{"yearly", "FREQ=YEARLY"},
		{"every 2 years", "FREQ=YEARLY;INTERVAL=2"},
		{"daily for 3 times", "FREQ=DAILY;COUNT=3"},
		// Case and surrounding space are the operator's, not the format's.
		{"  MONTHLY on Last Fri  ", "FREQ=MONTHLY;BYDAY=-1FR"},
		// The second accepted form: a raw RRULE line for what the short
		// grammar cannot say.
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29"},
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			got, err := Compile(c.spec, nil)
			if err != nil {
				t.Fatalf("Compile(%q): %v", c.spec, err)
			}
			if got != c.want {
				t.Errorf("Compile(%q) = %q, want %q", c.spec, got, c.want)
			}
		})
	}
}

func TestCompileUntil(t *testing.T) {
	until := time.Date(2026, 12, 31, 23, 59, 59, 0, jst)
	got, err := Compile("monthly until 2026-12-31", fixedDate(until))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.HasPrefix(got, "FREQ=MONTHLY;") || !strings.Contains(got, "UNTIL=20261231T145959Z") {
		t.Errorf("Compile = %q, want a monthly rule ending at the resolved instant in UTC", got)
	}
}

func TestCompileRefusals(t *testing.T) {
	cases := []struct {
		name string
		spec string
		has  string
	}{
		{"until and for together", "monthly until 2026-12-31 for 3 times", "never both"},
		{"empty", "   ", "empty"},
		{"unknown spelling", "fortnightly", "unknown recurrence spelling"},
		{"bad weekday", "weekly on funday", "not a weekday"},
		{"bad ordinal", "monthly on 9th tue", "not an ordinal"},
		{"day out of range", "monthly on 32", "not a day of the month"},
		{"zero interval", "every 0 days", "positive whole number"},
		{"count not a number", "daily for many times", "positive whole number"},
		{"count spelled wrong", "daily for 3", "`for <n> times`"},
		{"on with a daily rule", "daily on mon", "only meaningful with a weekly or monthly"},
		{"raw rule that does not parse", "FREQ=NEVER", "not a usable RRULE line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Compile(c.spec, fixedDate(time.Now()))
			if err == nil {
				t.Fatalf("Compile(%q) = %q, want an error", c.spec, got)
			}
			if !strings.Contains(err.Error(), c.has) {
				t.Errorf("Compile(%q) error = %q, want it to mention %q", c.spec, err, c.has)
			}
		})
	}
}

// RFC 5545 §3.3.10: a BYMONTHDAY the month does not have is SKIPPED, not
// clamped. That is the decision furrow adopted, so it is pinned here — a
// library that started rounding 31 down to 28 would change what a board means.
func TestMonthlyOn31SkipsShortMonths(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	line, err := Compile("monthly on 31", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2026-03-31T23:59:59+09:00", // February skipped
		"2026-05-31T23:59:59+09:00", // April skipped
		"2026-07-31T23:59:59+09:00", // June skipped
		"2026-08-31T23:59:59+09:00",
	}
	cur := anchor
	for _, w := range want {
		next, ok, err := Next(line, anchor, cur)
		if err != nil || !ok {
			t.Fatalf("Next after %s: ok=%v err=%v", cur, ok, err)
		}
		if got := next.Format(time.RFC3339); got != w {
			t.Fatalf("next = %s, want %s", got, w)
		}
		cur = next
	}

	if day, ok := SkipsMonths(line); !ok || day != 31 {
		t.Errorf("SkipsMonths = (%d, %v), want (31, true) so the CLI can say so at bind time", day, ok)
	}
	lastLine, _ := Compile("monthly on last", nil)
	if _, ok := SkipsMonths(lastLine); ok {
		t.Error("`monthly on last` always lands; it must not be reported as skipping")
	}
}

// The library reports an exhausted series with a ZERO time and no error, which
// is the trap every caller has to know about: ok, not err, is the stop signal.
func TestSeriesEnds(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)

	t.Run("count is spent (the anchor is occurrence 1)", func(t *testing.T) {
		line, err := Compile("daily for 3 times", nil)
		if err != nil {
			t.Fatal(err)
		}
		cur := anchor
		for i := 0; i < 2; i++ {
			next, ok, err := Next(line, anchor, cur)
			if err != nil || !ok {
				t.Fatalf("occurrence %d: ok=%v err=%v", i+2, ok, err)
			}
			cur = next
		}
		next, ok, err := Next(line, anchor, cur)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Errorf("a COUNT=3 series handed out a 4th occurrence (%s)", next)
		}
	})

	t.Run("until has passed", func(t *testing.T) {
		until := anchor.AddDate(0, 0, 3)
		line, err := Compile("daily until whenever", fixedDate(until))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := Next(line, anchor, until.AddDate(0, 0, 1)); ok || err != nil {
			t.Errorf("past UNTIL: ok=%v err=%v, want (false, nil)", ok, err)
		}
	})

	t.Run("a rule that can never match again", func(t *testing.T) {
		// 30 February: legal to write, impossible to reach.
		line, err := Compile("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := Next(line, anchor, anchor); ok || err != nil {
			t.Errorf("impossible rule: ok=%v err=%v, want (false, nil) — never an error", ok, err)
		}
	})
}

// Occurrences keep the ANCHOR's wall clock. Expanding in UTC instead would slide
// a US-zoned board's tasks by an hour every March.
func TestOccurrencesKeepTheAnchorsWallClock(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata for America/New_York: %v", err)
	}
	anchor := time.Date(2026, 2, 15, 23, 59, 59, 0, ny) // -05:00
	line, err := Compile("monthly", nil)
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := Next(line, anchor, anchor.AddDate(0, 0, 20))
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if got := next.Format(time.RFC3339); got != "2026-03-15T23:59:59-04:00" {
		t.Errorf("next = %s, want 2026-03-15T23:59:59-04:00 (same wall clock, DST offset moved)", got)
	}
}

// A late close is honest about the cycles it lapsed.
func TestCountBetweenReportsSkippedCycles(t *testing.T) {
	anchor := time.Date(2026, 3, 1, 23, 59, 59, 0, jst)
	line, err := Compile("monthly", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Promised for 1 March, closed on 15 May: 1 April and 1 May went by.
	closedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, jst)
	next, ok, err := Next(line, anchor, closedAt)
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if got := next.Format(time.RFC3339); got != "2026-06-01T23:59:59+09:00" {
		t.Fatalf("next = %s, want 2026-06-01T23:59:59+09:00", got)
	}
	n, err := CountBetween(line, anchor, anchor, next)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("skipped = %d, want 2 (1 April and 1 May)", n)
	}
}

func TestValidRejectsAStoredRuleThatStoppedParsing(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Valid("FREQ=MONTHLY;BYMONTHDAY=15", anchor); err != nil {
		t.Errorf("a good rule reported invalid: %v", err)
	}
	if err := Valid("FREQ=SOMETIMES", anchor); err == nil {
		t.Error("a rule that does not parse must be reported, or the series ends in silence")
	}
}

// Compile refuses a sub-daily rule at the door, but a shard furrow did not
// write can carry one — and it makes every close of that task expand a
// pathological number of occurrences. What Compile refuses, Valid reports, so
// `furrow lint` can see it.
func TestValidReportsWhatCompileRefuses(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := Compile("FREQ=MINUTELY", nil); err == nil {
		t.Error("a sub-daily rule compiled")
	}
	if err := Valid("FREQ=MINUTELY;INTERVAL=1", anchor); err == nil {
		t.Error("a stored sub-daily rule was reported valid — lint would never see it")
	}
	if err := Valid("FREQ=DAILY", anchor); err != nil {
		t.Errorf("a daily rule was reported invalid: %v", err)
	}
}
