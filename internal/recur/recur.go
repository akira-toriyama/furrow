// Package recur compiles furrow's short recurrence spellings into the RFC 5545
// RRULE line a task shard stores, and expands a stored rule to its next
// occurrence.
//
// It is a leaf: stdlib plus the RRULE library, no furrow layer below or above
// it. It deliberately does NOT know furrow's date vocabulary — a trailing
// `until <date>` is resolved by the caller (internal/cli, through the same
// parser `--due` uses), so the two can never drift into two date grammars.
//
// Validation is furrow's, not the library's: every spelling is checked here and
// refused with furrow's own wording, because the library's parse errors are not
// product-quality text.
//
// Every expansion runs in a frame that has no midnight gap rather than in the
// board's calendar directly, and the calendar is an explicit parameter — see
// naked for the day-grid defect that forces both.
package recur

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Spellings is the closed vocabulary of short forms, in the order the help
// lists them. A raw RRULE line is accepted too, minus a DTSTART (see Compile).
var Spellings = []string{
	"daily",
	"every <n> days",
	"weekly",
	"weekly on <days>",
	"every <n> weeks on <days>",
	"monthly",
	"monthly on <day-of-month>",
	"monthly on last",
	"monthly on <nth> <weekday>",
	"monthly on last <weekday>",
	"every <n> months on <day-of-month>",
	"yearly",
	"every <n> years",
}

var weekdays = map[string]rrule.Weekday{
	"mon": rrule.MO, "tue": rrule.TU, "wed": rrule.WE, "thu": rrule.TH,
	"fri": rrule.FR, "sat": rrule.SA, "sun": rrule.SU,
}

var ordinals = map[string]int{"1st": 1, "2nd": 2, "3rd": 3, "4th": 4, "5th": 5, "last": -1}

var everyRe = regexp.MustCompile(`^every ([0-9]+) (day|days|week|weeks|month|months|year|years)$`)

// countTermRe reads the COUNT term out of a raw RRULE line — the one place a
// zero count survives, since the parsed option cannot hold one.
var countTermRe = regexp.MustCompile(`(?:^|;)COUNT=([+-]?[0-9]+)(?:;|$)`)

// namedCount is the count a raw line spells, and whether it spelled one at all.
func namedCount(raw string) (int, bool) {
	m := countTermRe.FindStringSubmatch(raw)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// The two terminator introducers, matched case-insensitively on the ORIGINAL
// spelling so every index is a valid byte offset into it.
var (
	untilRe = regexp.MustCompile(`(?i)\s+until\s+`)
	forRe   = regexp.MustCompile(`(?i)\s+for\s+`)
)

// Compile turns one operator spelling into the RRULE line furrow stores.
//
// Two forms are accepted, deliberately: a short spelling (Spellings) and a raw
// RRULE line for anything the short grammar cannot say — except a DTSTART,
// which is refused: the series start is the task's repeat_anchor. Either way the
// result is re-rendered by the library, so what lands on disk is always a rule
// the expander can read back.
//
// resolveDate resolves the `until <date>` suffix with furrow's own date rules;
// it is only called when that suffix is present.
func Compile(spec string, resolveDate func(string) (time.Time, error)) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty recurrence rule")
	}

	head, until, count, err := splitTerminator(spec, resolveDate)
	if err != nil {
		return "", err
	}

	opt, err := parseHead(head)
	if err != nil {
		return "", err
	}
	// Assign ONLY what the spelling supplied. A raw RRULE line can carry its own
	// COUNT/UNTIL, and overwriting them with the zero value turned a bounded
	// series into an endless one — silently, since a zero Count and a zero Until
	// are exactly how "no terminator" is spelled.
	if count != 0 {
		if opt.Count != 0 || !opt.Until.IsZero() {
			return "", fmt.Errorf("the rule already ends itself (COUNT/UNTIL); drop `for <n> times` or the terminator in the rule")
		}
		opt.Count = count
	}
	if !until.IsZero() {
		if opt.Count != 0 || !opt.Until.IsZero() {
			return "", fmt.Errorf("the rule already ends itself (COUNT/UNTIL); drop `until <date>` or the terminator in the rule")
		}
		opt.Until = until
	}
	if opt.Count != 0 && !opt.Until.IsZero() {
		return "", fmt.Errorf("a rule may end with `until <date>` or `for <n> times`, never both (RFC 5545 forbids UNTIL with COUNT)")
	}
	// furrow promises DATES. A sub-daily rule is not just useless here — it is a
	// denial of service: one close would expand millions of occurrences. FREQ is
	// only half of it, since BYHOUR/BYMINUTE/BYSECOND multiply a DAILY rule into
	// the same thing.
	if err := refuseSubDaily(opt); err != nil {
		return "", err
	}
	// A DTSTART is the other property furrow cannot honour: RRuleString() drops
	// it on the way to disk and build() overwrites it with the anchor, so
	// accepting one would store a rule that silently disagrees with what was
	// typed. Refusing keeps the promise the shard schema already publishes.
	if err := refuseDtstart(opt); err != nil {
		return "", err
	}
	// Round-trip through the library: it both validates the combination and
	// gives the canonical spelling that goes on disk.
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return "", fmt.Errorf("not a usable recurrence rule: %v", err)
	}
	line := r.OrigOptions.RRuleString()
	if _, err := rrule.StrToRRule(line); err != nil {
		return "", fmt.Errorf("not a usable recurrence rule: %v", err)
	}
	return line, nil
}

// refuseSubDaily rejects everything that would make one rule fire more than once
// a day, whichever spelling asks for it.
func refuseSubDaily(opt *rrule.ROption) error {
	if opt.Freq > rrule.DAILY {
		return fmt.Errorf("a recurrence finer than daily is not supported — furrow promises a date, not a time of day")
	}
	for _, f := range []struct {
		name string
		vals []int
	}{{"BYHOUR", opt.Byhour}, {"BYMINUTE", opt.Byminute}, {"BYSECOND", opt.Bysecond}} {
		if len(f.vals) > 0 {
			return fmt.Errorf("%s makes a rule fire more than once a day, which furrow does not support — it promises a date, not a time of day", f.name)
		}
	}
	return nil
}

// refuseDtstart rejects a DTSTART, in either raw spelling (a `DTSTART=` term of
// the `;`-list, or a leading `DTSTART:`/`DTSTART;TZID=…` line) — the parsed
// ROption carries both the same way.
//
// furrow's series start is repeat_anchor, the first due, and nothing else: the
// stored line is rendered without a DTSTART and the expander assigns the anchor
// over whatever one was parsed. So a DTSTART can only ever be a value furrow
// took and then ignored, which is the one outcome worse than a refusal.
func refuseDtstart(opt *rrule.ROption) error {
	if !opt.Dtstart.IsZero() {
		return fmt.Errorf("a DTSTART is not accepted in a rule — the series starts at the task's due date, which furrow stores as its repeat_anchor; drop the DTSTART")
	}
	return nil
}

// splitTerminator peels a trailing `until <date>` or `for <n> times` off the
// spelling. RFC 5545 forbids UNTIL and COUNT together, so asking for both is a
// usage error rather than a silently dropped half.
func splitTerminator(spec string, resolveDate func(string) (time.Time, error)) (head string, until time.Time, count int, err error) {
	head = spec
	// Case-insensitive, but matched against the ORIGINAL: lowercasing can change
	// a string's LENGTH (İ -> i̇ is one rune to two), so an index taken from the
	// lowercased copy and used to slice the original can land mid-rune and panic.
	ui, fi := -1, -1
	if m := untilRe.FindStringIndex(spec); m != nil {
		ui = m[0]
	}
	if m := forRe.FindStringIndex(spec); m != nil {
		fi = m[0]
	}
	if ui >= 0 && fi >= 0 {
		return "", time.Time{}, 0, fmt.Errorf("a rule may end with `until <date>` or `for <n> times`, never both (RFC 5545 forbids UNTIL with COUNT)")
	}

	switch {
	case ui >= 0:
		if resolveDate == nil {
			return "", time.Time{}, 0, fmt.Errorf("`until <date>` is not accepted here")
		}
		text := strings.TrimSpace(untilRe.ReplaceAllString(spec[ui:], ""))
		if text == "" {
			return "", time.Time{}, 0, fmt.Errorf("`until` needs a date")
		}
		t, derr := resolveDate(text)
		if derr != nil {
			return "", time.Time{}, 0, fmt.Errorf("`until %s`: %v", text, derr)
		}
		until = t.UTC()
		head = strings.TrimSpace(spec[:ui])
	case fi >= 0:
		tail := strings.Fields(strings.ToLower(forRe.ReplaceAllString(spec[fi:], "")))
		if len(tail) != 2 || (tail[1] != "times" && tail[1] != "time") {
			return "", time.Time{}, 0, fmt.Errorf("the count spelling is `for <n> times`")
		}
		n, cerr := strconv.Atoi(tail[0])
		if cerr != nil || n < 1 {
			return "", time.Time{}, 0, fmt.Errorf("`for %s times`: the count must be a positive whole number", tail[0])
		}
		count = n
		head = strings.TrimSpace(spec[:fi])
	}
	return head, until, count, nil
}

func parseHead(head string) (*rrule.ROption, error) {
	typed := strings.TrimSpace(head)
	h := strings.ToLower(typed)
	if h == "" {
		return nil, fmt.Errorf("empty recurrence rule")
	}

	// A raw RRULE line, for anything the short grammar cannot say.
	if strings.Contains(h, "=") {
		raw := strings.ToUpper(strings.TrimPrefix(head, "RRULE:"))
		opt, err := rrule.StrToROption(raw)
		if err != nil {
			return nil, fmt.Errorf("not a usable RRULE line: %v", err)
		}
		// The COUNT term has to be read off the TEXT, because rrule.ROption
		// spells "COUNT=0" and "no COUNT at all" with the same zero value. Left
		// to the struct, a zero passes the terminator checks below (they read the
		// same zero as "no terminator"), RRuleString() drops the term on the way
		// to disk, and the operator's bounded rule is stored — at exit 0 — as an
		// endless one. `daily for 0 times` is refused; this is the same mistake
		// in the other spelling.
		if n, ok := namedCount(raw); ok && n < 1 {
			return nil, fmt.Errorf("a count must be a positive whole number, got %d", n)
		}
		return opt, nil
	}

	// `on <...>` tail, shared by the weekly and monthly forms.
	var on string
	if i := strings.Index(h, " on "); i >= 0 {
		on = strings.TrimSpace(h[i+len(" on "):])
		h = strings.TrimSpace(h[:i])
	}

	freq, interval, err := parseFreq(h, typed)
	if err != nil {
		return nil, err
	}
	opt := &rrule.ROption{Freq: freq, Interval: interval}
	if on == "" {
		return opt, nil
	}

	switch freq {
	case rrule.WEEKLY:
		days, err := parseWeekdayList(on)
		if err != nil {
			return nil, err
		}
		opt.Byweekday = days
	case rrule.MONTHLY:
		if err := applyMonthlyOn(opt, on); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("`on ...` is only meaningful with a weekly or monthly rule")
	}
	return opt, nil
}

func parseFreq(h, typed string) (rrule.Frequency, int, error) {
	switch h {
	case "daily":
		return rrule.DAILY, 0, nil
	case "weekly":
		return rrule.WEEKLY, 0, nil
	case "monthly":
		return rrule.MONTHLY, 0, nil
	case "yearly":
		return rrule.YEARLY, 0, nil
	}
	m := everyRe.FindStringSubmatch(h)
	if m == nil {
		return 0, 0, fmt.Errorf("unknown recurrence spelling %q; expected one of: %s (or a raw RRULE line)", typed, strings.Join(Spellings, ", "))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, 0, fmt.Errorf("the interval in %q must be a positive whole number", typed)
	}
	if n > MaxInterval {
		return 0, 0, fmt.Errorf("the interval in %q must be at most %d", typed, MaxInterval)
	}
	switch strings.TrimSuffix(m[2], "s") {
	case "day":
		return rrule.DAILY, n, nil
	case "week":
		return rrule.WEEKLY, n, nil
	case "month":
		return rrule.MONTHLY, n, nil
	default:
		return rrule.YEARLY, n, nil
	}
}

// parseWeekdayList reads the weekly `on <days>` tail as a SET: a day named
// twice is one member, kept where it was first typed.
//
// The dedupe is furrow's own. rrule-go joins BYDAY in the order given and never
// dedupes, so `weekly on mon,mon` would store `BYDAY=MO,MO` — a rule that
// behaves identically (BYDAY is a membership filter there, not a per-entry
// generator) but contradicts Compile's promise of a canonical spelling, and is
// read back verbatim by `furrow show`. Silent rather than a refusal: naming a
// set member twice is not an error anywhere else in furrow (labels, deps,
// repos all sort-and-dedupe).
//
// The seen-set keys on the whole rrule.Weekday — day AND n — so an
// nth-qualified weekday (+2MO) stays its own member, never a duplicate of the
// bare day.
func parseWeekdayList(on string) ([]rrule.Weekday, error) {
	var out []rrule.Weekday
	seen := make(map[rrule.Weekday]bool)
	for _, f := range strings.Split(on, ",") {
		f = strings.TrimSpace(f)
		w, ok := weekdays[f]
		if !ok {
			return nil, fmt.Errorf("%q is not a weekday; use mon,tue,wed,thu,fri,sat,sun", f)
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("`on` needs at least one weekday")
	}
	return out, nil
}

// applyMonthlyOn reads the three monthly shapes: a day of the month, `last`,
// and an ordinal weekday (`2nd tue`, `last fri`).
//
// `monthly on 31` and `monthly on last` are deliberately DIFFERENT rules: RFC
// 5545 says a BYMONTHDAY that a month does not have is skipped, so 31 lands 7
// times a year, while -1 is the last day of every month and always lands. The
// caller warns about the first; it is not an error, because skipping is the
// standard's answer and the operator may mean exactly that.
func applyMonthlyOn(opt *rrule.ROption, on string) error {
	fields := strings.Fields(on)
	switch len(fields) {
	case 1:
		if fields[0] == "last" {
			opt.Bymonthday = []int{-1}
			return nil
		}
		d, err := strconv.Atoi(fields[0])
		if err != nil || d < 1 || d > 31 {
			return fmt.Errorf("%q is not a day of the month; use 1..31, `last`, or an ordinal weekday like `2nd tue`", on)
		}
		opt.Bymonthday = []int{d}
		return nil
	case 2:
		nth, ok := ordinals[fields[0]]
		if !ok {
			return fmt.Errorf("%q is not an ordinal; use 1st, 2nd, 3rd, 4th, 5th, or last", fields[0])
		}
		w, ok := weekdays[fields[1]]
		if !ok {
			return fmt.Errorf("%q is not a weekday; use mon,tue,wed,thu,fri,sat,sun", fields[1])
		}
		opt.Byweekday = []rrule.Weekday{w.Nth(nth)}
		return nil
	default:
		return fmt.Errorf("%q is not a monthly `on` spelling; use a day (15), `last`, or an ordinal weekday (`2nd tue`, `last fri`)", on)
	}
}

// Next returns the first occurrence strictly after `after`, expanding the rule
// from `anchor` in the calendar loc.
//
// ok=false means the series is over — an UNTIL that has passed, a COUNT spent,
// or a rule that can never match again. That is the case the library reports
// with a ZERO time.Time rather than an error, which is why every caller has to
// look at ok and not just err.
//
// Occurrences keep the anchor's WALL CLOCK: a monthly rule anchored at 23:59:59
// local stays at 23:59:59 local across a DST boundary instead of sliding an
// hour. loc is the calendar that clock is read in, and it is a parameter rather
// than the zone `anchor` happens to carry: the answer must not depend on whether
// a caller remembered to hand the stored (UTC) anchor over in board time.
func Next(line string, anchor, after time.Time, loc *time.Location) (time.Time, bool, error) {
	loc = calendar(loc)
	r, err := build(line, anchor, loc)
	if err != nil {
		return time.Time{}, false, err
	}
	next := r.After(naked(after, loc), false)
	if next.IsZero() {
		return time.Time{}, false, nil
	}
	return zoned(next, loc), true, nil
}

// CountBetween reports how many occurrences fall strictly between lo and hi.
//
// It is what makes a late close honest: closing a monthly task two months after
// its due skips two occurrences, and `furrow done` says so rather than quietly
// pretending the series never lapsed.
func CountBetween(line string, anchor, lo, hi time.Time, loc *time.Location) (int, error) {
	loc = calendar(loc)
	r, err := build(line, anchor, loc)
	if err != nil {
		return 0, err
	}
	// The window is compared against occurrences the library hands back, so it
	// crosses into their frame with them.
	lo, hi = naked(lo, loc), naked(hi, loc)
	// Iterate rather than materialize: Between allocates the whole slice, which a
	// pathological stored rule can make enormous. Walking stops at hi.
	n := 0
	next := r.Iterator()
	for {
		t, ok := next()
		if !ok || !t.Before(hi) {
			break
		}
		if t.After(lo) {
			n++
		}
	}
	return n, nil
}

// Bindable reports whether a rule will ever fire again from this anchor. A rule
// that parses, is accepted, and then produces NOTHING is the worst of both — the
// shard says the task recurs and the first close ends the series without anyone
// having asked for that.
func Bindable(line string, anchor time.Time, loc *time.Location) error {
	if err := Valid(line, anchor, loc); err != nil {
		return err
	}
	_, ok, err := Next(line, anchor, anchor, loc)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("that rule has no occurrence after the first one — it would end the series on the very next close")
	}
	return nil
}

// Valid reports whether a stored rule still parses. `furrow lint` uses it: a
// rule that stopped parsing (a hand-edited shard, or one written by a furrow
// that knows a spelling this one does not) would end the series silently.
func Valid(line string, anchor time.Time, loc *time.Location) error {
	loc = calendar(loc)
	// The refusals read the PARSED LINE, never the built rule: build() assigns
	// the anchor over Dtstart, so a DTSTART check on the built rule would refuse
	// every rule on the board.
	opt, err := parseStored(line)
	if err != nil {
		return err
	}
	// The compile door refuses a sub-daily rule and a DTSTART, but a shard furrow
	// did not write can carry either — the first makes every close of that task
	// expand a pathological number of occurrences, the second stores a start
	// furrow ignores. What Compile refuses, Valid reports.
	if err := refuseSubDaily(opt); err != nil {
		return fmt.Errorf("stored recurrence rule %q: %v", line, err)
	}
	if err := refuseDtstart(opt); err != nil {
		return fmt.Errorf("stored recurrence rule %q: %v", line, err)
	}
	if opt.Interval > MaxInterval {
		return fmt.Errorf("stored recurrence rule %q: the interval must be at most %d", line, MaxInterval)
	}
	if _, err := buildFrom(opt, anchor, line, loc); err != nil {
		return err
	}
	return nil
}

// MaxInterval is the ceiling on a rule's INTERVAL, in the short spelling and a
// raw line alike. RFC 5545 names none, but the expansion is integer date
// arithmetic: past the int32 range every occurrence lands nowhere and the
// refusal that came back was "no occurrence after the first one" — true, and
// nothing to do with the cause (t-qps2). Ten thousand days is over 27 years,
// wider than any series worth a board.
const MaxInterval = 10000

func parseStored(line string) (*rrule.ROption, error) {
	opt, err := rrule.StrToROption(line)
	if err != nil {
		return nil, fmt.Errorf("stored recurrence rule %q does not parse: %v", line, err)
	}
	return opt, nil
}

func build(line string, anchor time.Time, loc *time.Location) (*rrule.RRule, error) {
	opt, err := parseStored(line)
	if err != nil {
		return nil, err
	}
	return buildFrom(opt, anchor, line, loc)
}

// buildFrom is build's half that does not parse, so Valid can run its refusals
// on the parsed line and still report the same "not usable" error; line is
// carried for that message alone.
func buildFrom(opt *rrule.ROption, anchor time.Time, line string, loc *time.Location) (*rrule.RRule, error) {
	// The anchor IS the series start: a DTSTART the line carries is overwritten,
	// never honoured, which is why both doors (Compile, Valid) refuse one. It
	// enters the library in the gap-free frame, so every occurrence comes back in
	// that frame too.
	opt.Dtstart = naked(anchor, loc)
	if !opt.Until.IsZero() {
		// UNTIL is stored as a true instant and the library compares it against
		// the occurrences it builds, so it is read in the same frame as they are —
		// otherwise a bounded series loses (or gains) its last occurrence by the
		// board zone's whole offset. The line on disk is untouched: it is rendered
		// from OrigOptions in Compile, never from a built rule.
		opt.Until = naked(opt.Until, loc)
	}
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("stored recurrence rule %q is not usable: %v", line, err)
	}
	return r, nil
}

// naked carries a wall clock across the library boundary, as if it were UTC.
//
// The library derives every occurrence's calendar day from January 1 at LOCAL
// MIDNIGHT in dtstart's zone (rrule-go v1.8.2 rrule.go:337 builds it with
// time.Date; :635 and :669 read the day off it). In a zone whose local midnight
// does not exist — America/Santiago, America/Havana and Atlantic/Azores spring
// forward AT 00:00 — Go resolves that construction BACKWARD onto the previous
// day, and the whole year's day grid shifts with it: a daily rule emits the day
// before twice and never the gap day (a close then under-reports `skipped` by
// one), and a BYDAY/BYMONTHDAY rule lands a day early — a `weekly on sun` chore
// promised for a Saturday, with no duplicate to notice it by. Upstream's fix is
// one line, but furrow pins the version, so the frame is furrow's job.
//
// A UTC frame has no gap and no repeated hour, so the day grid is exact. What it
// costs is that ordering inside the frame is WALL-CLOCK ordering — the one hour a
// zone repeats compares equal — which is the ordering a rule promising dates
// means anyway.
func naked(t time.Time, loc *time.Location) time.Time {
	w := t.In(loc)
	y, mo, d := w.Date()
	h, mi, s := w.Clock()
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

// zoned is naked's inverse: the wall clock t carries, read back in loc.
//
// A wall clock the zone SKIPS has no instant of its own, and time.Date resolves
// one BACKWARD, onto the offset in force before the transition. What the rule
// promised is a DAY, so that is what is kept — and which answer keeps it depends
// on where in the day the zone's gap falls:
//
//   - The gap is inside the day (America/Nuuk, America/Godthab and
//     America/Scoresbysund spring forward AT 23:00, so 23:00-23:59 is missing
//     once a year). The backward answer is 22:59:59 that same evening, which is
//     the promised day; pushing it forward instead landed it at 00:59:59 the
//     NEXT day, and a `weekly` chore anchored on a Saturday handed out a Sunday.
//   - The gap swallows local midnight (America/Santiago, America/Havana,
//     Atlantic/Azores). The backward answer falls off the promised day
//     altogether, and the first instant that exists — the transition — is the
//     one to take.
func zoned(t time.Time, loc *time.Location) time.Time {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	got := time.Date(y, mo, d, h, mi, s, 0, loc)
	if !naked(got, loc).Before(t) {
		return got
	}
	if gy, gmo, gd := got.Date(); gy == y && gmo == mo && gd == d {
		return got
	}
	// got sits before the gap, so the offset it carries is the one in force
	// BEFORE the transition; reading the wall clock under that offset lands on
	// the transition instant itself, or just past it.
	_, off := got.Zone()
	return time.Unix(t.Unix()-int64(off), 0).In(loc)
}

// calendar is the zone an expansion is read in. A nil one is UTC, never the
// running machine's zone: a caller that supplies none must not get a different
// series depending on where it ran.
func calendar(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// Period is what a rule SKIPS when the day of the month it lands on does not
// exist there: the months that lack that day, or — for February 29 alone — the
// years that lack one.
type Period int

const (
	Months Period = iota + 1
	Years
)

// Skip is one rule's day-of-month problem: the day it names, and the period
// that day is missing from.
type Skip struct {
	Day    int
	Period Period
}

// Skips reports whether a rule names a day of the month that some periods do
// not have — the 29/30/31 case RFC 5545 answers by SKIPPING the period instead
// of clamping into it. The caller says so once, at bind time: the operator who
// typed `monthly on 31` usually meant `monthly on last`, and the one who
// anchored a yearly rule on February 29 would otherwise find out in four years.
//
// Which period is skipped follows the rule's SHAPE, not its FREQ. `yearly`
// anchored on January 31 names its month as part of the rule and lands every
// January, so it is silent; `FREQ=MONTHLY;BYMONTH=2;BYMONTHDAY=29` fires only
// on February 29 and skips years like any other leap-day rule. A frequency test
// would get both backwards.
//
// loc is the board's calendar, as for Next/Valid/OffLattice: the day a rule
// lands on is a wall-clock fact, and an anchor handed over in UTC — how the
// shard stores it — reads as the NEXT day east of Greenwich after 23:59:59,
// so `monthly on 31` anchored on a local January 31 asked about February 1 and
// went silent. Skips used to be the one sibling that left the conversion to
// its caller (t-ax4c).
func Skips(line string, anchor time.Time, loc *time.Location) (Skip, bool) {
	opt, err := rrule.StrToROption(line)
	if err != nil {
		return Skip{}, false
	}
	anchor = anchor.In(calendar(loc))
	day, ok := namedDay(opt, anchor)
	if !ok {
		return Skip{}, false
	}
	var lacking, landing int
	for _, m := range firingMonths(opt, anchor) {
		if monthLacks(m, day) {
			lacking++
		} else {
			landing++
		}
	}
	switch {
	case lacking == 0:
		return Skip{}, false
	case day == 29 && landing == 0:
		// February is the only month this rule fires in, and February 29 is the
		// only day of the month a whole YEAR can lack.
		return Skip{Day: day, Period: Years}, true
	default:
		return Skip{Day: day, Period: Months}, true
	}
}

// namedDay is the day of the month a rule lands on, reported only when it is
// past the 28th — the days below that exist everywhere and have nothing to say.
//
// A rule that selects no day of its own takes the ANCHOR's, so `monthly`
// anchored on the 31st skips exactly the months `monthly on 31` does while
// naming no day at all. That is the spelling an operator reaches for first, so
// it is the one that most needs the note. Only a monthly or yearly period can
// miss a day: a daily or weekly rule lands on whatever day comes next.
func namedDay(opt *rrule.ROption, anchor time.Time) (int, bool) {
	for _, d := range opt.Bymonthday {
		if d >= 29 {
			return d, true
		}
	}
	if selectsNoDay(opt) && (opt.Freq == rrule.MONTHLY || opt.Freq == rrule.YEARLY) && anchor.Day() >= 29 {
		return anchor.Day(), true
	}
	return 0, false
}

// OffLattice reports whether the anchor is NOT itself an occurrence of the
// rule, and names the rule's own first date when it is not.
//
// RFC 5545 §3.3.10 says the COUNT rule part range-bounds the recurrence with
// "the DTSTART property value always counts as the first occurrence" — true
// only while DTSTART is synchronized with the rule, which §3.8.5.3 says it
// SHOULD be and leaves undefined when it is not. furrow's answer to the
// undefined case is the library's: an unsynchronized anchor is never folded
// into the series, so it is one live occurrence OUTSIDE it and a COUNT of n
// yields n MORE after it — `--due <a Friday> --repeat "weekly on mon for 3
// times"` is four tasks, not three. Nothing about that is wrong; it is just
// the opposite of what the RFC sentence teaches, so the caller says it once at
// bind time.
//
// A rule that yields nothing at all from this anchor reports false: the bind
// door (Bindable) refuses that case, so the only way to reach it is a
// hand-edited shard, and a note naming no date would say less than silence.
//
// It EXPANDS the rule, so it runs in the same naked frame Next and CountBetween
// do: asked in the zone's own instants, a zone whose local midnight does not
// exist would answer off by a day and report a lattice date as off-lattice.
func OffLattice(line string, anchor time.Time, loc *time.Location) (first time.Time, off bool) {
	loc = calendar(loc)
	r, err := build(line, anchor, loc)
	if err != nil {
		return time.Time{}, false
	}
	// Inclusive: an anchor ON the lattice is the rule's own first occurrence.
	bare := naked(anchor, loc)
	next := r.After(bare, true)
	if next.IsZero() || next.Equal(bare) {
		return time.Time{}, false
	}
	return zoned(next, loc), true
}

// selectsNoDay reports a rule that picks no day for itself in any spelling, so
// the anchor's day stands in for it.
func selectsNoDay(opt *rrule.ROption) bool {
	return len(opt.Bymonthday) == 0 && len(opt.Byweekday) == 0 &&
		len(opt.Byyearday) == 0 && len(opt.Byweekno) == 0
}

// firingMonths is the months a rule can fire in: the months its FREQUENCY
// reaches, narrowed by BYMONTH where it names any.
//
// BYMONTH is a FILTER, not a source — a month it names that the frequency's
// own lattice never reaches is not a month the rule fires in
// (`FREQ=MONTHLY;INTERVAL=2;BYMONTH=1,2` anchored in January fires in January
// alone), so taking BYMONTH outright reported a skip for a February the rule
// never visits.
func firingMonths(opt *rrule.ROption, anchor time.Time) []int {
	months := freqMonths(opt, anchor)
	if len(opt.Bymonth) == 0 {
		return months
	}
	reached := make(map[int]bool, len(months))
	for _, m := range months {
		reached[m] = true
	}
	var out []int
	for _, m := range opt.Bymonth {
		if reached[m] {
			out = append(out, m)
		}
	}
	return out
}

// freqMonths is the months the frequency alone reaches, before BYMONTH narrows
// them.
//
// A YEARLY rule that names no day narrows to the anchor's month — measured
// against the library, a YEARLY rule WITH a BYMONTHDAY expands across all
// twelve months exactly like a MONTHLY one (`FREQ=YEARLY;BYMONTHDAY=29`
// anchored in January fires next in March), so it skips the Februarys, not the
// years.
//
// A MONTHLY rule's INTERVAL is the other narrowing, and the one that was
// missed: `every 6 months on 31` reaches January and July, both of which have a
// 31st, so it never skips a month — yet the note said it did, and prescribed
// `on last` for a rule that always lands.
func freqMonths(opt *rrule.ROption, anchor time.Time) []int {
	if opt.Freq == rrule.YEARLY && len(opt.Bymonthday) == 0 {
		return []int{int(anchor.Month())}
	}
	if opt.Freq == rrule.MONTHLY && opt.Interval > 1 {
		return intervalMonths(int(anchor.Month()), opt.Interval)
	}
	return everyMonth
}

// intervalMonths walks a MONTHLY rule's INTERVAL from the anchor's month until
// it comes back round: 12/gcd(interval,12) months of the year, every one of
// which the rule fires in. An interval coprime with 12 reaches all twelve, which
// is why the caller cannot shortcut on the interval's size.
func intervalMonths(start, interval int) []int {
	seen := make(map[int]bool, 12)
	var out []int
	for m := start; !seen[m]; m = (m-1+interval)%12 + 1 {
		seen[m] = true
		out = append(out, m)
	}
	return out
}

var everyMonth = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

// monthLacks reports a month that does not have that day of the month.
// February counts for 29 even though three years in four it has one: the rule
// still misses every year it does not.
func monthLacks(month, day int) bool {
	switch time.Month(month) {
	case time.February:
		return day >= 29
	case time.April, time.June, time.September, time.November:
		return day > 30
	}
	return false
}
