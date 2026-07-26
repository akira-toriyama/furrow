package app

import (
	"strconv"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/query"
)

// The date qualifiers (created/updated/closed/reviewed) — the -q generalization
// of ls --since/--until. Every scalar is bound to an inclusive INTERVAL of
// instants at compile time, which is what makes a bare day and a comparison
// compose without a special case:
//
//	created:2026-07-01        — inside that whole (UTC) day
//	created:>2026-07-01       — strictly AFTER the day (not later the same day)
//	updated:>=-2w             — within the last two weeks (a relative offset)
//	closed:2026-07-01..-1d    — range, ends inclusive; * = an open end
//
// A nil closed/reviewed satisfies NO comparison (existence is has:/no:'s job),
// and a negated term therefore INCLUDES the unset — `-closed:<2026-01-01` keeps
// the still-open tasks, matching how negation treats unset estimates.

// dateBound is one date scalar as an inclusive [lo, hi] instant interval: a
// bare YYYY-MM-DD covers its whole UTC day (hi = 23:59:59, the last whole
// second furrow ever stamps — the same convention as ls --until); an RFC3339
// instant and a relative offset are a single instant (lo == hi). rel marks a
// relative offset, which equality rejects (see compileDate).
type dateBound struct {
	lo, hi time.Time
	rel    bool
}

// parseDateScalar binds one date scalar at compile time. Three spellings:
// YYYY-MM-DD (a whole UTC day), RFC3339 (an exact instant), and a signed
// relative offset ±N{m,h,d,w} from now — JQL's units, where m is MINUTES, not
// months ("-2w" = two weeks ago). Anything else is exit 2 (query-type).
func (c *queryCompiler) parseDateScalar(field, s string) (dateBound, error) {
	if d, ok := parseRelativeOffset(s); ok {
		i := c.now.Add(d)
		return dateBound{lo: i, hi: i, rel: true}, nil
	}
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return dateBound{lo: d, hi: d.Add(24*time.Hour - time.Second)}, nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		u := ts.UTC()
		return dateBound{lo: u, hi: u}, nil
	}
	return dateBound{}, unknownQueryErr("query-type",
		"field "+strconv.Quote(field)+" needs a date (YYYY-MM-DD, RFC3339, or a relative offset like -2w), got "+strconv.Quote(s), nil)
}

// parseRelativeOffset parses a signed relative offset ±N{m,h,d,w} into a
// duration ("-2w" → minus two weeks). The sign is required — an unsigned "2w"
// is not a date. ok=false means "not an offset" (the caller tries the absolute
// forms next), never an error.
func parseRelativeOffset(s string) (time.Duration, bool) {
	if len(s) < 3 || (s[0] != '-' && s[0] != '+') {
		return 0, false
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return 0, false
	}
	n, err := strconv.Atoi(s[1 : len(s)-1])
	if err != nil || n < 0 {
		return 0, false
	}
	d := time.Duration(n) * unit
	if s[0] == '-' {
		d = -d
	}
	return d, true
}

// compileDate binds a date qualifier (created/updated/closed/reviewed) with an
// equality (day/instant membership, comma = OR), comparison, or range. All
// values are parsed at compile time, so a malformed date fails the read before
// a single task is matched.
func (c *queryCompiler) compileDate(term query.Term, neg func(func(*core.Task) bool) func(*core.Task) bool) (func(*core.Task) bool, error) {
	f := term.Field
	getTime := func(t *core.Task) (time.Time, bool) {
		switch f {
		case "created":
			return t.Created, true
		case "updated":
			return t.Updated, true
		case "closed":
			if t.Closed == nil {
				return time.Time{}, false
			}
			return *t.Closed, true
		case "reviewed":
			if t.Reviewed == nil {
				return time.Time{}, false
			}
			return *t.Reviewed, true
		}
		return time.Time{}, false
	}

	switch term.Op {
	case query.Between:
		// A..B inclusive: the interval spans A's start to B's end; * = open.
		var lo, hi time.Time
		haveLo, haveHi := term.Values[0].Text != "*", term.Values[1].Text != "*"
		if haveLo {
			b, err := c.parseDateScalar(f, term.Values[0].Text)
			if err != nil {
				return nil, err
			}
			lo = b.lo
		}
		if haveHi {
			b, err := c.parseDateScalar(f, term.Values[1].Text)
			if err != nil {
				return nil, err
			}
			hi = b.hi
		}
		return neg(func(t *core.Task) bool {
			v, ok := getTime(t)
			if !ok {
				return false
			}
			return (!haveLo || !v.Before(lo)) && (!haveHi || !v.After(hi))
		}), nil

	case query.Eq:
		// Membership in any listed day/instant (comma = OR). A relative offset
		// is a bare instant, and "equals an instant to the second" is never
		// what anyone means — reject it and name the fix.
		bounds := make([]dateBound, 0, len(term.Values))
		for _, v := range term.Values {
			b, err := c.parseDateScalar(f, v.Text)
			if err != nil {
				return nil, err
			}
			if b.rel {
				return nil, unknownQueryErr("query-type",
					"a relative date offset needs a comparison or range (e.g. "+f+":>="+v.Text+"), not a bare equality", nil)
			}
			bounds = append(bounds, b)
		}
		return neg(func(t *core.Task) bool {
			v, ok := getTime(t)
			if !ok {
				return false
			}
			for _, b := range bounds {
				if !v.Before(b.lo) && !v.After(b.hi) {
					return true
				}
			}
			return false
		}), nil

	default: // Gt/Ge/Lt/Le — interval-aware, so a bare day behaves as a whole day.
		b, err := c.parseDateScalar(f, term.Values[0].Text)
		if err != nil {
			return nil, err
		}
		op := term.Op
		return neg(func(t *core.Task) bool {
			v, ok := getTime(t)
			if !ok {
				return false
			}
			switch op {
			case query.Gt:
				return v.After(b.hi) // strictly after the whole day/instant
			case query.Ge:
				return !v.Before(b.lo) // on/after its start
			case query.Lt:
				return v.Before(b.lo) // strictly before its start
			case query.Le:
				return !v.After(b.hi) // on/before its end
			}
			return false
		}), nil
	}
}
