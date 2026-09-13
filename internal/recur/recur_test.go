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
		// A weekday list is a SET, in first-seen order: the library joins BYDAY
		// in the order given and never dedupes, so these used to store
		// BYDAY=MO,MO — a rule that behaves the same and is not canonical.
		{"weekly on mon,mon", "FREQ=WEEKLY;BYDAY=MO"},
		{"weekly on MON,mon", "FREQ=WEEKLY;BYDAY=MO"},
		{"every 2 weeks on fri,fri,mon", "FREQ=WEEKLY;INTERVAL=2;BYDAY=FR,MO"},
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
		{"DTSTART as a term of the rule", "FREQ=DAILY;DTSTART=20200101T000000Z", "DTSTART"},
		{"DTSTART on its own line", "DTSTART:20200101T000000Z\nRRULE:FREQ=DAILY", "DTSTART"},
		// TZID=UTC, not a named zone: parseHead upper-cases the whole raw line, and
		// time.LoadLocation("AMERICA/NEW_YORK") resolves only on a case-insensitive
		// filesystem — the refusal under test would be a parse error on Linux CI.
		{"DTSTART with a zone", "DTSTART;TZID=UTC:20200101T083000\nRRULE:FREQ=WEEKLY;BYDAY=MO", "DTSTART"},
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

	if skip, ok := Skips(line, anchor); !ok || skip.Day != 31 || skip.Period != Months {
		t.Errorf("Skips = (%+v, %v), want ({31 Months}, true) so the CLI can say so at bind time", skip, ok)
	}
	lastLine, _ := Compile("monthly on last", nil)
	if _, ok := Skips(lastLine, anchor); ok {
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

// FREQ is only half the sub-daily story: BYHOUR/BYMINUTE/BYSECOND multiply a
// DAILY rule into the same thing, and furrow promises a date, not a time of day.
func TestSubDailyIsRefusedWhicheverSpellingAsksForIt(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, spec := range []string{
		"FREQ=MINUTELY",
		"FREQ=DAILY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11",
		"FREQ=DAILY;BYMINUTE=0,30",
		"FREQ=DAILY;BYSECOND=0,1",
	} {
		if line, err := Compile(spec, nil); err == nil {
			t.Errorf("Compile(%q) = %q, want a refusal", spec, line)
		}
		if err := Valid(spec, anchor); err == nil {
			t.Errorf("Valid(%q) accepted a stored sub-daily rule — lint would never see it", spec)
		}
	}
}

// A DTSTART furrow accepted would be dropped by the renderer and overwritten by
// the expander, so the rule on disk would disagree with what was typed. Both
// doors refuse it — and Valid is the one that can see a hand-edited shard, which
// is the case the expander ignores in silence forever.
func TestDtstartIsRefusedAtBothDoors(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, spec := range []string{
		"FREQ=DAILY;DTSTART=20200101T000000Z",
		"DTSTART:20200101T000000Z\nRRULE:FREQ=DAILY",
		"FREQ=MONTHLY;DTSTART=20200101T000000Z;BYMONTHDAY=15",
	} {
		if line, err := Compile(spec, nil); err == nil {
			t.Errorf("Compile(%q) = %q, want a refusal — the DTSTART was dropped", spec, line)
		}
		if err := Valid(spec, anchor); err == nil {
			t.Errorf("Valid(%q) accepted a stored DTSTART — lint would never see it", spec)
		}
	}
	// Valid reads the DTSTART off the LINE, never off build()'s result: build
	// assigns the anchor to Dtstart, so a check there refuses everything.
	if err := Valid("FREQ=DAILY", anchor); err != nil {
		t.Errorf("a plain stored rule was reported invalid: %v", err)
	}
}

// A count must be a count.
func TestOutOfRangeCountIsRefused(t *testing.T) {
	for _, spec := range []string{"FREQ=DAILY;COUNT=-5", "daily for 0 times"} {
		if line, err := Compile(spec, nil); err == nil {
			t.Errorf("Compile(%q) = %q, want a refusal", spec, line)
		}
	}
}

// Bindable is the door a rule that can never fire again must not get through.
func TestBindableRejectsARuleWithNothingLeft(t *testing.T) {
	anchor := time.Date(2026, 3, 1, 23, 59, 59, 0, jst)
	if err := Bindable("FREQ=DAILY;COUNT=1", anchor); err == nil {
		t.Error("a rule whose only occurrence is the anchor was called bindable")
	}
	if err := Bindable("FREQ=DAILY;COUNT=2", anchor); err != nil {
		t.Errorf("a rule with one more occurrence was refused: %v", err)
	}
}

// A bare `monthly` takes its day from the ANCHOR, so anchoring one on the 31st
// skips exactly the months `monthly on 31` does — while naming no day at all.
// That is the spelling an operator reaches for first.
func TestSkipsSeesTheAnchorsDay(t *testing.T) {
	line, err := Compile("monthly", nil)
	if err != nil {
		t.Fatal(err)
	}
	onThe31st := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	if skip, ok := Skips(line, onThe31st); !ok || skip.Day != 31 || skip.Period != Months {
		t.Errorf("Skips(bare monthly, anchored on the 31st) = (%+v, %v), want ({31 Months}, true)", skip, ok)
	}
	onThe15th := time.Date(2026, 1, 15, 23, 59, 59, 0, jst)
	if _, ok := Skips(line, onThe15th); ok {
		t.Error("a rule anchored on the 15th lands every month; it must not be warned about")
	}
}

// Lowercasing can change a string's LENGTH (İ becomes two runes), so an index
// taken from a lowercased copy and used to slice the original lands mid-rune
// and panics the process. The match is made on the original now.
func TestAMultibyteSpellingDoesNotPanic(t *testing.T) {
	until := time.Date(2026, 12, 31, 23, 59, 59, 0, jst)
	for _, spec := range []string{
		"monthly UNTİL 2026-12-31",
		"İmonthly for 3 times",
		"monthly Until 2026-12-31",
		"DAILY FOR 3 TIMES",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Compile(%q) panicked: %v", spec, r)
				}
			}()
			if _, err := Compile(spec, fixedDate(until)); err != nil {
				t.Logf("Compile(%q) refused: %v", spec, err) // a refusal is fine; a panic is not
			}
		}()
	}
}

// The terminator is recognised whatever case it is typed in, and the rest of
// the spelling survives intact.
func TestTerminatorsAreCaseInsensitive(t *testing.T) {
	until := time.Date(2026, 12, 31, 23, 59, 59, 0, jst)
	got, err := Compile("MONTHLY UNTIL 2026-12-31", fixedDate(until))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !strings.HasPrefix(got, "FREQ=MONTHLY") || !strings.Contains(got, "UNTIL=") {
		t.Errorf("Compile = %q, want a monthly rule with an UNTIL", got)
	}
	if got, err := Compile("DAILY FOR 3 TIMES", nil); err != nil || got != "FREQ=DAILY;COUNT=3" {
		t.Errorf("Compile = %q, %v; want FREQ=DAILY;COUNT=3", got, err)
	}
}

// A YEARLY rule names its month, so most days past 28 cannot skip anything:
// `yearly` anchored on January 31 lands every January. February 29 is the one
// exception — it exists only in leap years, so the rule jumps three years in
// four (measured: anchored 2028-02-29, the next occurrence is 2032-02-29) and
// must never be handed the monthly `on last` remedy.
func TestYearlySkipsOnlyOnFebruary29(t *testing.T) {
	feb29 := time.Date(2028, 2, 29, 23, 59, 59, 0, jst)
	jan31 := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	jun15 := time.Date(2026, 6, 15, 23, 59, 59, 0, jst)

	cases := []struct {
		name   string
		spec   string
		anchor time.Time
		want   Skip // the zero Skip means: say nothing
	}{
		{"bare yearly on the leap day", "yearly", feb29, Skip{Day: 29, Period: Years}},
		{"every 2 years on the leap day", "every 2 years", feb29, Skip{Day: 29, Period: Years}},
		{"every 3 years on the leap day", "every 3 years", feb29, Skip{Day: 29, Period: Years}},
		{"a raw leap-day line", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", feb29, Skip{Day: 29, Period: Years}},
		// The rule names the day outright, so the anchor's own day says nothing.
		{"a raw leap-day line anchored elsewhere", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", jun15, Skip{Day: 29, Period: Years}},
		// Only February fires, whatever FREQ asks for it.
		{"a monthly rule confined to February", "FREQ=MONTHLY;BYMONTH=2;BYMONTHDAY=29", feb29, Skip{Day: 29, Period: Years}},
		// Measured: a YEARLY rule WITH a BYMONTHDAY expands over all twelve
		// months, so it skips the Februarys — the months remedy is the right one.
		{"a raw yearly line naming no month", "FREQ=YEARLY;BYMONTHDAY=29", feb29, Skip{Day: 29, Period: Months}},
		{"yearly on January 31", "yearly", jan31, Skip{}},
		{"February's last day", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1", feb29, Skip{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, err := Compile(c.spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			skip, ok := Skips(line, c.anchor)
			if !ok {
				skip = Skip{}
			}
			if skip != c.want {
				t.Errorf("Skips(%q, %s) = (%+v, %v), want %+v", line, c.anchor.Format("2006-01-02"), skip, ok, c.want)
			}
		})
	}
}

// The remedy the leap-day note prescribes has to be one that actually lands
// every year — the whole complaint about `on last` was that it was measured
// against the wrong frequency.
func TestTheLeapDayRemediesLandEveryYear(t *testing.T) {
	anchor := time.Date(2028, 2, 29, 23, 59, 59, 0, jst)
	for _, c := range []struct {
		spec   string
		anchor time.Time
		want   string
	}{
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1", anchor, "2029-02-28"},
		{"yearly", time.Date(2028, 2, 28, 23, 59, 59, 0, jst), "2029-02-28"},
	} {
		line, err := Compile(c.spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := Skips(line, c.anchor); ok {
			t.Errorf("%q was reported as skipping; it is the rule the note recommends", line)
		}
		next, ok, err := Next(line, c.anchor, c.anchor)
		if err != nil || !ok {
			t.Fatalf("Next(%q): ok=%v err=%v", line, ok, err)
		}
		if got := next.Format("2006-01-02"); got != c.want {
			t.Errorf("Next(%q) = %s, want %s — the remedy must land the very next year", line, got, c.want)
		}
	}
}

// A rule whose period is a day or a week lands on whatever day comes next, so
// the anchor's day of the month says nothing about it.
func TestASubMonthlyRuleIsNeverReportedAsSkipping(t *testing.T) {
	onThe31st := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	for _, spec := range []string{"daily", "every 3 days", "weekly"} {
		line, err := Compile(spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		if skip, ok := Skips(line, onThe31st); ok {
			t.Errorf("%q anchored on the 31st was reported as skipping (%+v)", spec, skip)
		}
	}
}

// The anchor is not required to lie on the rule's lattice, and the library does
// not fold an unsynchronized one in — the case RFC 5545 §3.8.5.3 leaves
// undefined. OffLattice is how the caller can say so at bind time, rather than
// letting a `for n times` rule hand out one occurrence more than it reads like.
func TestOffLatticeReportsTheRulesOwnFirstDate(t *testing.T) {
	friday := time.Date(2026, 9, 18, 23, 59, 59, 0, jst)
	monday := time.Date(2026, 9, 21, 23, 59, 59, 0, jst)
	cases := []struct {
		name   string
		spec   string
		anchor time.Time
		want   time.Time // zero when the anchor is ON the lattice
	}{
		{"weekly on mon anchored on a Friday", "weekly on mon for 3 times", friday, monday},
		{"weekly on mon anchored on a Monday", "weekly on mon for 3 times", monday, time.Time{}},
		{"a COUNT of 1 hides the same asymmetry", "weekly on mon for 1 times", friday, monday},
		{"daily lands on every anchor", "daily", friday, time.Time{}},
		{"a bare monthly takes the anchor's own day", "monthly", friday, time.Time{}},
		{"a day-of-month rule anchored elsewhere", "monthly on 31", time.Date(2026, 3, 15, 23, 59, 59, 0, jst), time.Date(2026, 3, 31, 23, 59, 59, 0, jst)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, err := Compile(c.spec, nil)
			if err != nil {
				t.Fatalf("Compile(%q): %v", c.spec, err)
			}
			first, off := OffLattice(line, c.anchor)
			if off != !c.want.IsZero() {
				t.Fatalf("OffLattice(%q) = (%s, %v), want off=%v", line, first.Format(time.RFC3339), off, !c.want.IsZero())
			}
			if off && !first.Equal(c.want) {
				t.Errorf("first date = %s, want %s", first.Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
		})
	}

	// A raw RRULE line — the escape hatch for what the short grammar cannot say —
	// is read exactly the same way.
	if first, off := OffLattice("FREQ=WEEKLY;BYDAY=MO", friday); !off || !first.Equal(monday) {
		t.Errorf("OffLattice(raw RRULE) = (%s, %v), want (%s, true)", first.Format(time.RFC3339), off, monday.Format(time.RFC3339))
	}
	// A stored rule that no longer parses is `lint`'s finding (repeat-invalid),
	// not this one's: a note naming no date would say less than silence.
	if _, off := OffLattice("FREQ=NONSENSE", friday); off {
		t.Error("an unparseable rule was reported as off-lattice")
	}
}
