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
// lists them. A raw RRULE line is accepted too (see Compile).
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

// Compile turns one operator spelling into the RRULE line furrow stores.
//
// Two forms are accepted, deliberately: a short spelling (Spellings) and a raw
// RRULE line for anything the short grammar cannot say. Either way the result is
// re-rendered by the library, so what lands on disk is always a rule the
// expander can read back.
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
	opt.Count = count
	opt.Until = until

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

// splitTerminator peels a trailing `until <date>` or `for <n> times` off the
// spelling. RFC 5545 forbids UNTIL and COUNT together, so asking for both is a
// usage error rather than a silently dropped half.
func splitTerminator(spec string, resolveDate func(string) (time.Time, error)) (head string, until time.Time, count int, err error) {
	head = spec
	lower := strings.ToLower(spec)

	ui := strings.Index(lower, " until ")
	fi := strings.Index(lower, " for ")
	if ui >= 0 && fi >= 0 {
		return "", time.Time{}, 0, fmt.Errorf("a rule may end with `until <date>` or `for <n> times`, never both (RFC 5545 forbids UNTIL with COUNT)")
	}

	switch {
	case ui >= 0:
		if resolveDate == nil {
			return "", time.Time{}, 0, fmt.Errorf("`until <date>` is not accepted here")
		}
		text := strings.TrimSpace(spec[ui+len(" until "):])
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
		tail := strings.Fields(strings.ToLower(strings.TrimSpace(spec[fi+len(" for "):])))
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
	h := strings.ToLower(strings.TrimSpace(head))
	if h == "" {
		return nil, fmt.Errorf("empty recurrence rule")
	}

	// A raw RRULE line, for anything the short grammar cannot say.
	if strings.Contains(h, "=") {
		opt, err := rrule.StrToROption(strings.ToUpper(strings.TrimPrefix(head, "RRULE:")))
		if err != nil {
			return nil, fmt.Errorf("not a usable RRULE line: %v", err)
		}
		return opt, nil
	}

	// `on <...>` tail, shared by the weekly and monthly forms.
	var on string
	if i := strings.Index(h, " on "); i >= 0 {
		on = strings.TrimSpace(h[i+len(" on "):])
		h = strings.TrimSpace(h[:i])
	}

	freq, interval, err := parseFreq(h)
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

func parseFreq(h string) (rrule.Frequency, int, error) {
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
		return 0, 0, fmt.Errorf("unknown recurrence spelling %q; expected one of: %s (or a raw RRULE line)", h, strings.Join(Spellings, ", "))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 0, 0, fmt.Errorf("the interval in %q must be a positive whole number", h)
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

func parseWeekdayList(on string) ([]rrule.Weekday, error) {
	var out []rrule.Weekday
	for _, f := range strings.Split(on, ",") {
		f = strings.TrimSpace(f)
		w, ok := weekdays[f]
		if !ok {
			return nil, fmt.Errorf("%q is not a weekday; use mon,tue,wed,thu,fri,sat,sun", f)
		}
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
// from `anchor`.
//
// ok=false means the series is over — an UNTIL that has passed, a COUNT spent,
// or a rule that can never match again. That is the case the library reports
// with a ZERO time.Time rather than an error, which is why every caller has to
// look at ok and not just err.
//
// The zone is the anchor's: occurrences keep the anchor's wall clock, so a
// monthly rule anchored at 23:59:59 local stays at 23:59:59 local across a DST
// boundary instead of sliding an hour.
func Next(line string, anchor, after time.Time) (time.Time, bool, error) {
	r, err := build(line, anchor)
	if err != nil {
		return time.Time{}, false, err
	}
	next := r.After(after, false)
	if next.IsZero() {
		return time.Time{}, false, nil
	}
	return next, true, nil
}

// CountBetween reports how many occurrences fall strictly between lo and hi.
//
// It is what makes a late close honest: closing a monthly task two months after
// its due skips two occurrences, and `furrow done` says so rather than quietly
// pretending the series never lapsed.
func CountBetween(line string, anchor, lo, hi time.Time) (int, error) {
	r, err := build(line, anchor)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range r.Between(lo, hi, false) {
		if t.After(lo) && t.Before(hi) {
			n++
		}
	}
	return n, nil
}

// Valid reports whether a stored rule still parses. `furrow lint` uses it: a
// rule that stopped parsing (a hand-edited shard, or one written by a furrow
// that knows a spelling this one does not) would end the series silently.
func Valid(line string, anchor time.Time) error {
	_, err := build(line, anchor)
	return err
}

func build(line string, anchor time.Time) (*rrule.RRule, error) {
	opt, err := rrule.StrToROption(line)
	if err != nil {
		return nil, fmt.Errorf("stored recurrence rule %q does not parse: %v", line, err)
	}
	opt.Dtstart = anchor
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("stored recurrence rule %q is not usable: %v", line, err)
	}
	return r, nil
}

// SkipsMonths reports whether a rule names a day of the month that some months
// do not have — the 29/30/31 case RFC 5545 answers by SKIPPING the month. The
// caller says so once, at bind time: the operator who typed `monthly on 31`
// usually meant `monthly on last`, and silence would let them find out in March.
func SkipsMonths(line string) (day int, ok bool) {
	opt, err := rrule.StrToROption(line)
	if err != nil {
		return 0, false
	}
	for _, d := range opt.Bymonthday {
		if d >= 29 {
			return d, true
		}
	}
	return 0, false
}
