package recur

import (
	"fmt"
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
		{"interval past the ceiling", "every 2147483647 days", "at most 10000"},
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
		next, ok, err := Next(line, anchor, cur, jst)
		if err != nil || !ok {
			t.Fatalf("Next after %s: ok=%v err=%v", cur, ok, err)
		}
		if got := next.Format(time.RFC3339); got != w {
			t.Fatalf("next = %s, want %s", got, w)
		}
		cur = next
	}

	if skip, ok := Skips(line, anchor, jst); !ok || skip.Day != 31 || skip.Period != Months {
		t.Errorf("Skips = (%+v, %v), want ({31 Months}, true) so the CLI can say so at bind time", skip, ok)
	}
	lastLine, _ := Compile("monthly on last", nil)
	if _, ok := Skips(lastLine, anchor, jst); ok {
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
			next, ok, err := Next(line, anchor, cur, jst)
			if err != nil || !ok {
				t.Fatalf("occurrence %d: ok=%v err=%v", i+2, ok, err)
			}
			cur = next
		}
		next, ok, err := Next(line, anchor, cur, jst)
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
		if _, ok, err := Next(line, anchor, until.AddDate(0, 0, 1), jst); ok || err != nil {
			t.Errorf("past UNTIL: ok=%v err=%v, want (false, nil)", ok, err)
		}
	})

	t.Run("a rule that can never match again", func(t *testing.T) {
		// 30 February: legal to write, impossible to reach.
		line, err := Compile("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := Next(line, anchor, anchor, jst); ok || err != nil {
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
	next, ok, err := Next(line, anchor, anchor.AddDate(0, 0, 20), ny)
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if got := next.Format(time.RFC3339); got != "2026-03-15T23:59:59-04:00" {
		t.Errorf("next = %s, want 2026-03-15T23:59:59-04:00 (same wall clock, DST offset moved)", got)
	}
}

// The zones whose local MIDNIGHT does not exist — they spring forward AT 00:00 —
// are the ones the library's day grid gets wrong: it is built from January 1 at
// local midnight in the anchor's zone, so from the transition on, every
// occurrence lands a day early (see naked). A daily rule emitted the day before
// TWICE and never the gap day; a BYDAY or BYMONTHDAY rule simply landed on the
// wrong day, with no duplicate to notice it by.
func TestOccurrencesLandOnADayWithNoLocalMidnight(t *testing.T) {
	for _, c := range []struct{ zone, gap string }{
		{"America/Santiago", "2026-09-06"},
		{"America/Havana", "2026-03-08"},
		{"Atlantic/Azores", "2026-03-29"},
	} {
		t.Run(c.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(c.zone)
			if err != nil {
				t.Skipf("no tzdata for %s: %v", c.zone, err)
			}
			// Parsed in UTC, so the fields are the LITERAL day: the one instant
			// this zone does not have is that day's own local midnight.
			day, err := time.Parse("2006-01-02", c.gap)
			if err != nil {
				t.Fatal(err)
			}
			y, mo, d := day.Date()
			endOfDay := func(offset int) time.Time {
				return time.Date(y, mo, d+offset, 23, 59, 59, 0, loc)
			}

			t.Run("daily crosses it exactly once", func(t *testing.T) {
				line, err := Compile("daily", nil)
				if err != nil {
					t.Fatal(err)
				}
				anchor := endOfDay(-2)
				cur := anchor
				for _, want := range []time.Time{endOfDay(-1), endOfDay(0), endOfDay(1)} {
					next, ok, err := Next(line, anchor, cur, loc)
					if err != nil || !ok {
						t.Fatalf("Next after %s: ok=%v err=%v", cur, ok, err)
					}
					if !next.Equal(want) {
						t.Fatalf("occurrence after %s = %s, want %s", cur.Format(time.RFC3339), next.Format(time.RFC3339), want.Format(time.RFC3339))
					}
					cur = next
				}
			})

			t.Run("a weekday rule keeps its weekday", func(t *testing.T) {
				line, err := Compile("weekly on "+strings.ToLower(day.Weekday().String()[:3]), nil)
				if err != nil {
					t.Fatal(err)
				}
				anchor := endOfDay(-7)
				next, ok, err := Next(line, anchor, anchor, loc)
				if err != nil || !ok {
					t.Fatalf("Next: ok=%v err=%v", ok, err)
				}
				if !next.Equal(endOfDay(0)) {
					t.Errorf("next = %s (%s), want %s (%s) — a weekly rule promised for the wrong weekday says nothing about it",
						next.Format(time.RFC3339), next.Weekday(), endOfDay(0).Format(time.RFC3339), day.Weekday())
				}
			})

			t.Run("a day-of-month rule keeps its day", func(t *testing.T) {
				line, err := Compile(fmt.Sprintf("monthly on %d", d), nil)
				if err != nil {
					t.Fatal(err)
				}
				anchor := endOfDay(-40) // the month before, whatever its length
				next, ok, err := Next(line, anchor, endOfDay(-1), loc)
				if err != nil || !ok {
					t.Fatalf("Next: ok=%v err=%v", ok, err)
				}
				if !next.Equal(endOfDay(0)) {
					t.Errorf("next = %s, want %s", next.Format(time.RFC3339), endOfDay(0).Format(time.RFC3339))
				}
			})
		})
	}
}

// The lapse count a late close reports reads the same grid, so the day with no
// local midnight went missing from it too — the series below lapsed seven days
// and six were reported.
func TestCountBetweenCountsADayWithNoLocalMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tzdata for America/Santiago: %v", err)
	}
	line, err := Compile("daily", nil)
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 9, 5, 23, 59, 59, 0, loc) // promised for the 5th
	closedOn := time.Date(2026, 9, 13, 0, 0, 0, 0, loc) // cleared on the 13th
	n, err := CountBetween(line, anchor, anchor, closedOn, loc)
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Errorf("skipped = %d, want 7 (6..12 September — the 6th is the day this zone has no midnight)", n)
	}
}

// An occurrence whose wall clock is one the zone SKIPS has no instant of its
// own, and time.Date resolves it BACKWARD, onto the previous day — the same
// defect one layer down. It is pushed forward out of the gap instead, so the
// occurrence stays on the day the rule promised.
func TestAnOccurrenceInsideTheSkippedHourKeepsItsDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tzdata for America/Santiago: %v", err)
	}
	line, err := Compile("monthly on 6", nil)
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 8, 6, 0, 30, 0, 0, loc) // 00:30 does not exist on 6 September
	next, ok, err := Next(line, anchor, anchor, loc)
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if got := next.Format(time.RFC3339); got != "2026-09-06T01:30:00-03:00" {
		t.Errorf("next = %s, want 2026-09-06T01:30:00-03:00 — the promised day, past the hour the zone skips", got)
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
	next, ok, err := Next(line, anchor, closedAt, jst)
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if got := next.Format(time.RFC3339); got != "2026-06-01T23:59:59+09:00" {
		t.Fatalf("next = %s, want 2026-06-01T23:59:59+09:00", got)
	}
	n, err := CountBetween(line, anchor, anchor, next, jst)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("skipped = %d, want 2 (1 April and 1 May)", n)
	}
}

func TestValidRejectsAStoredRuleThatStoppedParsing(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Valid("FREQ=MONTHLY;BYMONTHDAY=15", anchor, time.UTC); err != nil {
		t.Errorf("a good rule reported invalid: %v", err)
	}
	if err := Valid("FREQ=SOMETIMES", anchor, time.UTC); err == nil {
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
	if err := Valid("FREQ=MINUTELY;INTERVAL=1", anchor, time.UTC); err == nil {
		t.Error("a stored sub-daily rule was reported valid — lint would never see it")
	}
	if err := Valid("FREQ=DAILY", anchor, time.UTC); err != nil {
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
		if err := Valid(spec, anchor, time.UTC); err == nil {
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
		if err := Valid(spec, anchor, time.UTC); err == nil {
			t.Errorf("Valid(%q) accepted a stored DTSTART — lint would never see it", spec)
		}
	}
	// Valid reads the DTSTART off the LINE, never off build()'s result: build
	// assigns the anchor to Dtstart, so a check there refuses everything.
	if err := Valid("FREQ=DAILY", anchor, time.UTC); err != nil {
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
	if err := Bindable("FREQ=DAILY;COUNT=1", anchor, jst); err == nil {
		t.Error("a rule whose only occurrence is the anchor was called bindable")
	}
	if err := Bindable("FREQ=DAILY;COUNT=2", anchor, jst); err != nil {
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
	if skip, ok := Skips(line, onThe31st, jst); !ok || skip.Day != 31 || skip.Period != Months {
		t.Errorf("Skips(bare monthly, anchored on the 31st) = (%+v, %v), want ({31 Months}, true)", skip, ok)
	}
	onThe15th := time.Date(2026, 1, 15, 23, 59, 59, 0, jst)
	if _, ok := Skips(line, onThe15th, jst); ok {
		t.Error("a rule anchored on the 15th lands every month; it must not be warned about")
	}
}

// The anchor arrives in whatever zone the caller holds it in — UTC, as the
// shard stores it — and the day it names is a fact of the BOARD's calendar.
// A local January 31 at 23:59:59 in a zone west of Greenwich is February 1 in
// UTC; a rule that names its day (`monthly on 31`) never looked at the anchor,
// but the BARE `monthly` reads the anchor's day and went silent on that UTC
// instant (measured on the pre-change tree, t-ax4c). The sibling functions
// already took loc, and now so does this one.
func TestSkipsReadsTheAnchorInTheBoardsCalendar(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	line, _ := Compile("monthly on 31", nil)
	local := time.Date(2026, 1, 31, 23, 59, 59, 0, ny)
	utc := local.UTC() // 2026-02-01T04:59:59Z
	if skip, ok := Skips(line, utc, ny); !ok || skip.Day != 31 || skip.Period != Months {
		t.Errorf("Skips(UTC anchor, board zone New York) = (%+v, %v), want ({31 Months}, true)", skip, ok)
	}
	bare, _ := Compile("monthly", nil)
	if skip, ok := Skips(bare, utc, ny); !ok || skip.Day != 31 {
		t.Errorf("a bare monthly anchored on the local 31st must be read as the 31st, got (%+v, %v)", skip, ok)
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
			skip, ok := Skips(line, c.anchor, c.anchor.Location())
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
		if _, ok := Skips(line, c.anchor, c.anchor.Location()); ok {
			t.Errorf("%q was reported as skipping; it is the rule the note recommends", line)
		}
		next, ok, err := Next(line, c.anchor, c.anchor, jst)
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
		if skip, ok := Skips(line, onThe31st, onThe31st.Location()); ok {
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
			first, off := OffLattice(line, c.anchor, jst)
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
	if first, off := OffLattice("FREQ=WEEKLY;BYDAY=MO", friday, jst); !off || !first.Equal(monday) {
		t.Errorf("OffLattice(raw RRULE) = (%s, %v), want (%s, true)", first.Format(time.RFC3339), off, monday.Format(time.RFC3339))
	}
	// A stored rule that no longer parses is `lint`'s finding (repeat-invalid),
	// not this one's: a note naming no date would say less than silence.
	if _, off := OffLattice("FREQ=NONSENSE", friday, jst); off {
		t.Error("an unparseable rule was reported as off-lattice")
	}
}

// A MONTHLY rule's INTERVAL decides which months it visits, so a day only the
// months it never visits lack is not a day it skips. `every 6 months on 31`
// reaches January and July, both of which have a 31st — the note that said
// otherwise also prescribed `on last`, a remedy for a rule that was already
// landing every time.
func TestSkipsNarrowsToTheMonthsAnIntervalReaches(t *testing.T) {
	jan31 := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	jan30 := time.Date(2026, 1, 30, 23, 59, 59, 0, jst)
	apr30 := time.Date(2026, 4, 30, 23, 59, 59, 0, jst)
	mar31 := time.Date(2026, 3, 31, 23, 59, 59, 0, jst)
	for _, c := range []struct {
		name   string
		spec   string
		anchor time.Time
		want   Skip
	}{
		{"every 6 months on 31 from January", "every 6 months on 31", jan31, Skip{}},
		{"every 12 months on 30 from April", "every 12 months on 30", apr30, Skip{}},
		// January, May, September — every one of them has a 30th.
		{"a bare every-4-months anchored on the 30th", "every 4 months", jan30, Skip{}},
		// March and September: September has no 31st, so this one really skips.
		{"every 6 months on 31 from March", "every 6 months on 31", mar31, Skip{Day: 31, Period: Months}},
		// January, May, September again — September has no 31st.
		{"every 4 months on 31 from January", "every 4 months on 31", jan31, Skip{Day: 31, Period: Months}},
		// Coprime with 12, so the lattice reaches every month after all.
		{"every 5 months on 31 from January", "every 5 months on 31", jan31, Skip{Day: 31, Period: Months}},
		{"plain monthly on 31 still skips", "monthly on 31", jan31, Skip{Day: 31, Period: Months}},
	} {
		t.Run(c.name, func(t *testing.T) {
			line, err := Compile(c.spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			skip, ok := Skips(line, c.anchor, jst)
			if !ok {
				skip = Skip{}
			}
			if skip != c.want {
				t.Errorf("Skips(%q anchored %s) = (%+v, %v), want %+v",
					line, c.anchor.Format("2006-01-02"), skip, ok, c.want)
			}
			if c.want == (Skip{}) {
				assertEveryOccurrenceLands(t, line, c.anchor)
			}
		})
	}
}

// assertEveryOccurrenceLands walks the series and fails if any period is
// skipped — the fact a silent Skips is claiming.
func assertEveryOccurrenceLands(t *testing.T, line string, anchor time.Time) {
	t.Helper()
	want := anchor.Day()
	cur := anchor
	for i := 0; i < 8; i++ {
		next, ok, err := Next(line, anchor, cur, jst)
		if err != nil || !ok {
			t.Fatalf("Next(%q) after %s: ok=%v err=%v", line, cur.Format("2006-01-02"), ok, err)
		}
		if next.Day() != want {
			t.Fatalf("occurrence %d of %q is %s — the rule does skip, so the note was right",
				i, line, next.Format("2006-01-02"))
		}
		// The day alone cannot tell a series that advances from one that
		// stands still (#392: a weekly series handed back the day it closed on
		// and the shape-only assertion stayed green).
		if !next.After(cur) {
			t.Fatalf("occurrence %d of %q is %s, not after %s — the series stands still",
				i, line, next.Format(time.RFC3339), cur.Format(time.RFC3339))
		}
		cur = next
	}
}

// A rule that fires in February alone misses a whole YEAR when it names the
// 29th, whatever spelling gets it there — the leap-day note and its remedy,
// not the monthly `on last` one.
func TestAnIntervalConfinedToFebruarySkipsYears(t *testing.T) {
	feb29 := time.Date(2028, 2, 29, 23, 59, 59, 0, jst)
	line, err := Compile("every 12 months on 29", nil)
	if err != nil {
		t.Fatal(err)
	}
	skip, ok := Skips(line, feb29, jst)
	if !ok || skip != (Skip{Day: 29, Period: Years}) {
		t.Errorf("Skips(%q anchored on a February 29) = (%+v, %v), want ({29 Years}, true): the rule visits February and nothing else", line, skip, ok)
	}
}

// BYMONTH filters the months the frequency reaches; it does not add any. A
// February named by a rule whose lattice never reaches February is not a month
// the rule skips.
func TestByMonthCannotNameAMonthTheLatticeMisses(t *testing.T) {
	jan31 := time.Date(2026, 1, 31, 23, 59, 59, 0, jst)
	if skip, ok := Skips("FREQ=MONTHLY;INTERVAL=2;BYMONTH=1,2;BYMONTHDAY=31", jan31, jst); ok {
		t.Errorf("Skips = (%+v, true); the odd-month lattice never reaches February, so nothing is skipped", skip)
	}
}

// COUNT=0 is the one count the parsed option cannot hold: it is spelled with
// the same zero as "no terminator", so it passed every check and the term was
// dropped on the way to disk — a bounded rule stored as an endless one, at exit
// 0. It is refused like every other spelling of the same mistake.
func TestZeroCountIsRefusedRatherThanDropped(t *testing.T) {
	for _, spec := range []string{
		"FREQ=DAILY;COUNT=0",
		"RRULE:FREQ=WEEKLY;COUNT=0",
		"FREQ=MONTHLY;COUNT=00;BYMONTHDAY=15",
	} {
		line, err := Compile(spec, nil)
		if err == nil {
			t.Errorf("Compile(%q) = %q, want a refusal — the COUNT term is gone from what would be stored", spec, line)
		}
	}
	// A count that is a count still compiles, and keeps its term.
	if got, err := Compile("FREQ=DAILY;COUNT=3", nil); err != nil || got != "FREQ=DAILY;COUNT=3" {
		t.Errorf("Compile = %q, %v; want FREQ=DAILY;COUNT=3", got, err)
	}
}

// A rule promises a DAY. The Greenland zones spring forward AT 23:00, so a
// lattice point at 23:59:59 has no instant of its own once a year — and pushing
// it forward to the first instant that does exist crossed midnight, handing a
// Saturday chore out on a Sunday. The backward reading is 22:59:59 that same
// evening, which is the day the rule named.
func TestAnOccurrenceKeepsItsDayWhenTheEveningHasAGap(t *testing.T) {
	nuuk, err := time.LoadLocation("America/Nuuk")
	if err != nil {
		t.Skipf("no tzdata for America/Nuuk: %v", err)
	}
	// 2027-03-20 is a Saturday; the zone's gap falls on 2027-03-27, the next one.
	anchor := time.Date(2027, 3, 20, 23, 59, 59, 0, nuuk)
	cur := anchor
	for i := 0; i < 4; i++ {
		next, ok, err := Next("FREQ=WEEKLY", anchor, cur, nuuk)
		if err != nil || !ok {
			t.Fatalf("Next after %s: ok=%v err=%v", cur.Format(time.RFC3339), ok, err)
		}
		if next.Weekday() != time.Saturday {
			t.Errorf("occurrence %d = %s (%s), want a Saturday — a weekly rule recurs on its anchor's weekday",
				i, next.Format(time.RFC3339), next.Weekday())
		}
		// Strictly after, every step. An occurrence clamped onto the gap day
		// reads back as an earlier wall clock than the lattice point it came
		// from, so a search that compared in the expander's frame handed the
		// same day back and the series stood still — on a date that is a
		// Saturday, which is why a weekday assertion alone cannot see it.
		if !next.After(cur) {
			t.Fatalf("occurrence %d = %s did not advance past %s — the series stands still",
				i, next.Format(time.RFC3339), cur.Format(time.RFC3339))
		}
		cur = next
	}
	// The occurrence on the gap day itself is the last instant of that day the
	// zone has, which is where the same board's `--due 2027-03-27` also lands.
	got, _, err := Next("FREQ=WEEKLY", anchor, time.Date(2027, 3, 26, 0, 0, 0, 0, nuuk), nuuk)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2027, 3, 27, 23, 59, 59, 0, nuuk); !got.Equal(want) {
		t.Errorf("the gap day's occurrence = %s, want %s (what a bare --due for that day binds)",
			got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// A zone that skips local MIDNIGHT is the other half of the same rule, and it
// still resolves forward: the backward answer there falls off the promised day
// altogether.
func TestAnOccurrenceMovesForwardWhenMidnightItselfIsMissing(t *testing.T) {
	scl, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tzdata for America/Santiago: %v", err)
	}
	// Santiago springs forward at 00:00 on 2026-09-06; a rule landing at 00:30
	// that day has no instant of its own.
	anchor := time.Date(2026, 8, 30, 0, 30, 0, 0, scl)
	got, ok, err := Next("FREQ=WEEKLY", anchor, time.Date(2026, 9, 5, 0, 0, 0, 0, scl), scl)
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if y, mo, d := got.Date(); y != 2026 || mo != time.September || d != 6 {
		t.Errorf("occurrence = %s, want it on 2026-09-06 — the day the rule named", got.Format(time.RFC3339))
	}
}

// A stored line with COUNT=0 is what Compile refuses in the other spelling;
// Valid read the parsed option, on which a zero count is "no count", and let
// it through (an endless series where a bounded one was written).
func TestValidRefusesAZeroCount(t *testing.T) {
	anchor := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := Valid("FREQ=DAILY;COUNT=0", anchor, nil); err == nil || !strings.Contains(err.Error(), "count") {
		t.Errorf("Valid(COUNT=0) = %v; want a count refusal", err)
	}
	if err := Valid("FREQ=DAILY;COUNT=3", anchor, nil); err != nil {
		t.Errorf("Valid(COUNT=3) = %v; want ok", err)
	}
}

// Lands is "is this one of the dates the series hands out", asked by a write
// that re-dated an occurrence to tell a move ALONG the series from a move off
// it. The clock is half of a date here: a whole-day series does not land on
// 11:00 of a day it lands on, which is the case the question exists for.
func TestLandsTellsASeriesDateFromOneBesideIt(t *testing.T) {
	const weekdays = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
	wed := time.Date(2026, 11, 18, 23, 59, 59, 0, jst) // a Wednesday
	dec15 := time.Date(2026, 12, 15, 23, 59, 59, 0, jst)
	cases := []struct {
		name   string
		line   string
		anchor time.Time
		at     time.Time
		want   bool
	}{
		{"the next slot of the rule", weekdays, wed, time.Date(2026, 11, 19, 23, 59, 59, 0, jst), true},
		{"the same day at another clock", weekdays, wed, time.Date(2026, 11, 18, 11, 0, 0, 0, jst), false},
		{"a day the rule does not name", weekdays, wed, time.Date(2026, 11, 21, 23, 59, 59, 0, jst), false},
		{"a bare monthly keeps the anchor's day", "FREQ=MONTHLY", dec15, time.Date(2027, 1, 15, 23, 59, 59, 0, jst), true},
		{"and no other day of the month", "FREQ=MONTHLY", dec15, time.Date(2027, 1, 20, 23, 59, 59, 0, jst), false},
		{"an anchor on the lattice is the first of them", "FREQ=DAILY", wed, wed, true},
		{"an anchor off it is not one at all", "FREQ=WEEKLY;BYDAY=MO", wed, wed, false},
		{"nothing lands before the anchor", "FREQ=DAILY", wed, wed.AddDate(0, 0, -1), false},
		{"the last date of a COUNT", "FREQ=DAILY;COUNT=3", wed, wed.AddDate(0, 0, 2), true},
		{"the date after a COUNT is spent", "FREQ=DAILY;COUNT=3", wed, wed.AddDate(0, 0, 3), false},
		// The shard stores UTC; the calendar is the parameter, not the zone the
		// instants happen to carry.
		{"stored instants are read in the calendar", weekdays, wed.UTC(), time.Date(2026, 11, 19, 23, 59, 59, 0, jst).UTC(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Lands(c.line, c.anchor, c.at, jst)
			if err != nil {
				t.Fatalf("Lands(%q): %v", c.line, err)
			}
			if got != c.want {
				t.Errorf("Lands(%q from %s, %s) = %v, want %v", c.line, c.anchor.Format(time.RFC3339), c.at.Format(time.RFC3339), got, c.want)
			}
		})
	}

	// An unreadable rule is an error, never "does not land": the caller's note
	// would otherwise speak for a series the expander cannot read.
	if _, err := Lands("FREQ=NONSENSE", wed, wed, jst); err == nil {
		t.Error("Lands on an unparseable rule returned no error")
	}
}

// Whatever a close hands out lands — including the dates a zone's gap
// displaced, which sit at a wall clock the rule never named. Looked up in the
// library's own lattice, the occurrence a close minted on a spring-forward day
// would read as moved off the series by hand; the walk caught the direction a
// lookup through Next misses too (Santiago's gap swallows midnight, and a
// 00:30 occurrence lands at 01:30, LATER than its lattice point).
func TestEveryDateNextHandsOutLands(t *testing.T) {
	for _, zone := range []string{"America/New_York", "America/Santiago", "America/Nuuk", "Europe/London"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Logf("no tzdata for %s: %v", zone, err)
			continue
		}
		for _, clock := range [][3]int{{0, 30, 0}, {2, 30, 0}, {23, 30, 0}, {23, 59, 59}} {
			anchor := time.Date(2026, 1, 1, clock[0], clock[1], clock[2], 0, loc)
			cur := anchor
			for i := 0; i < 500; i++ {
				next, ok, err := Next("FREQ=DAILY", anchor, cur, loc)
				if err != nil || !ok {
					t.Fatalf("%s %v step %d: Next ok=%v err=%v", zone, clock, i, ok, err)
				}
				if lands, err := Lands("FREQ=DAILY", anchor, next, loc); err != nil || !lands {
					t.Fatalf("%s: the occurrence %s (a daily series at %02d:%02d:%02d) does not land on its own series (err=%v)",
						zone, next.Format(time.RFC3339), clock[0], clock[1], clock[2], err)
				}
				cur = next
			}
		}
	}
}

// zonedAfter is the answer Next owes: the first lattice point that READS BACK
// strictly after `after` in the calendar, found by walking the whole series
// from its anchor rather than from `after`.
func zonedAfter(t *testing.T, line string, anchor, after time.Time, loc *time.Location) (time.Time, bool) {
	t.Helper()
	r, err := build(line, anchor, loc)
	if err != nil {
		t.Fatal(err)
	}
	next := r.Iterator()
	for {
		p, ok := next()
		if !ok {
			return time.Time{}, false
		}
		if z := zoned(p, loc); z.After(after) {
			return z, true
		}
	}
}

// On the days a zone changes its offset a lattice point can read back LATER
// than a close whose frame clock is past it — a gap moves London's 01:30 to
// 02:30 BST and Santiago's 00:30 to 01:30, and on London's fall-back night the
// repeated 01:30 is the GMT pass, still ahead of a close at 01:40 BST — and a
// walk that started at naked(after) stepped over it: the close lost that
// occurrence, a week of a weekly rule and a month of a monthly one, and no
// `skipped` counted it (t-5w8g). Every minute across each such day gets the
// oracle's answer. The west zones read a gap point back EARLIER and take the
// first pass of a repeated hour, which never lost one; they stay in as the
// other direction.
func TestNextAgreesWithTheCalendarAcrossEveryOffsetChange(t *testing.T) {
	cases := []struct {
		zone  string
		day   [3]int // the day the offset changes, local
		clock [3]int // a clock the change skips or repeats
		rules []string
	}{
		{"America/Santiago", [3]int{2026, 9, 6}, [3]int{0, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY", "FREQ=MONTHLY;BYMONTHDAY=6"}},
		{"America/Havana", [3]int{2026, 3, 8}, [3]int{0, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"Atlantic/Azores", [3]int{2027, 3, 28}, [3]int{0, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"Europe/London", [3]int{2027, 3, 28}, [3]int{1, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"Europe/Berlin", [3]int{2027, 3, 28}, [3]int{2, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY;BYDAY=SU"}},
		{"Australia/Lord_Howe", [3]int{2027, 10, 3}, [3]int{2, 15, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/New_York", [3]int{2027, 3, 14}, [3]int{2, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/Nuuk", [3]int{2027, 3, 27}, [3]int{23, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/Nuuk", [3]int{2027, 3, 27}, [3]int{23, 59, 59}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		// Fall-back nights: the clock happens twice.
		{"Europe/London", [3]int{2027, 10, 31}, [3]int{1, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"Europe/Berlin", [3]int{2027, 10, 31}, [3]int{2, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"Australia/Lord_Howe", [3]int{2027, 4, 4}, [3]int{1, 45, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/New_York", [3]int{2027, 11, 7}, [3]int{1, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/Santiago", [3]int{2027, 4, 3}, [3]int{23, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
		{"America/Nuuk", [3]int{2027, 10, 30}, [3]int{23, 30, 0}, []string{"FREQ=DAILY", "FREQ=WEEKLY"}},
	}
	for _, c := range cases {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Logf("no tzdata for %s: %v", c.zone, err)
			continue
		}
		for _, line := range c.rules {
			// A month before for the monthly rule, a week before otherwise: either
			// way the gap day is on the lattice.
			anchor := time.Date(c.day[0], time.Month(c.day[1]), c.day[2]-7, c.clock[0], c.clock[1], c.clock[2], 0, loc)
			if strings.HasPrefix(line, "FREQ=MONTHLY") {
				anchor = time.Date(c.day[0], time.Month(c.day[1])-1, c.day[2], c.clock[0], c.clock[1], c.clock[2], 0, loc)
			}
			noon := time.Date(c.day[0], time.Month(c.day[1]), c.day[2], 12, 0, 0, 0, loc)
			wrong, first := 0, ""
			for after := noon.Add(-24 * time.Hour); after.Before(noon.Add(24 * time.Hour)); after = after.Add(time.Minute) {
				got, ok, err := Next(line, anchor, after, loc)
				if err != nil {
					t.Fatalf("%s %s: Next: %v", c.zone, line, err)
				}
				want, wok := zonedAfter(t, line, anchor, after, loc)
				if ok != wok || !got.Equal(want) {
					if wrong == 0 {
						first = fmt.Sprintf("after %s: Next = %s, want %s", after.Format(time.RFC3339), got.Format(time.RFC3339), want.Format(time.RFC3339))
					}
					wrong++
				}
			}
			if wrong > 0 {
				t.Errorf("%s %s at %02d:%02d:%02d: %d minute(s) across the day get the wrong occurrence; first: %s",
					c.zone, line, c.clock[0], c.clock[1], c.clock[2], wrong, first)
			}
		}
	}
}

// The case the finding was filed on: a Santiago daily 00:30 series, whose
// 2026-09-06 occurrence exists only at 01:30, closed at 01:10 that morning.
func TestNextHandsOutTheOccurrenceAMidnightGapMoved(t *testing.T) {
	scl, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no tzdata for America/Santiago: %v", err)
	}
	anchor := time.Date(2026, 8, 30, 0, 30, 0, 0, scl)
	after := time.Date(2026, 9, 6, 1, 10, 0, 0, scl)
	for _, line := range []string{"FREQ=DAILY", "FREQ=WEEKLY"} {
		got, ok, err := Next(line, anchor, after, scl)
		if err != nil || !ok {
			t.Fatalf("%s: Next ok=%v err=%v", line, ok, err)
		}
		if want := "2026-09-06T01:30:00-03:00"; got.Format(time.RFC3339) != want {
			t.Errorf("%s: Next = %s, want %s — the occurrence the gap moved past the close", line, got.Format(time.RFC3339), want)
		}
	}
}

// In the hour a zone repeats, both passes of 01:30 are the series' 01:30 — the
// frame's own ordering, in which the repeated hour compares equal. An instant
// comparison called the second pass "off the series", and the note built on it
// told an operator to re-bind a rule at the clock it already names.
func TestBothPassesOfARepeatedHourLand(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata for America/New_York: %v", err)
	}
	// New York falls back at 02:00 on 2026-11-01, so 01:30 happens twice.
	anchor := time.Date(2026, 10, 30, 1, 30, 0, 0, ny)
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)  // 01:30 EDT
	second := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC) // 01:30 EST
	for name, at := range map[string]time.Time{"the first pass": first, "the second pass": second} {
		if got := at.In(ny).Format("15:04"); got != "01:30" {
			t.Fatalf("%s reads %s in New York, want 01:30 — the fixture is wrong", name, got)
		}
		if lands, err := Lands("FREQ=DAILY", anchor, at, ny); err != nil || !lands {
			t.Errorf("%s of 01:30 does not land on a daily 01:30 series (err=%v)", name, err)
		}
	}
	// An hour that is not the series' clock still does not, on that day or any.
	if lands, _ := Lands("FREQ=DAILY", anchor, time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), ny); lands {
		t.Error("02:30 landed on a daily 01:30 series")
	}
}

// A COUNT is measured from the anchor, an UNTIL is a date: only the first
// starts over when a rule is bound again, so only it earns the sentence.
func TestCountedNamesOnlyTheTerminatorARebindRestarts(t *testing.T) {
	for line, want := range map[string]bool{
		"FREQ=DAILY;COUNT=3":                true,
		"FREQ=WEEKLY;COUNT=10;BYDAY=MO":     true,
		"FREQ=DAILY;UNTIL=20261202T145959Z": false,
		"FREQ=DAILY":                        false,
	} {
		if got := Counted(line); got != want {
			t.Errorf("Counted(%q) = %v, want %v", line, got, want)
		}
	}
	line, err := Compile("daily for 3 times", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !Counted(line) {
		t.Errorf("Counted(%q) = false for a compiled `for 3 times`", line)
	}
}
