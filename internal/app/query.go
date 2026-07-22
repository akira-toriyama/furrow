package app

import (
	"errors"
	"strconv"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/query"
)

// qualifierVocab is the set of field qualifiers `-q` understands in v1 (the
// candidates offered on an unknown field). Date qualifiers (created/updated/…),
// body:, and the transitive graph qualifiers (child-of/depends-on/…) are the
// tracked v2 remainder and are NOT listed, so writing one yields a clear
// unknown-field error rather than a silent no-match.
var qualifierVocab = []string{
	"status", "lane", "type", "label", "repo", "id", "parent", "title",
	"value", "effort", "priority", "roi",
}

// presenceVocab is the field set has:/no: accept in v1.
var presenceVocab = []string{
	"label", "repo", "parent", "value", "effort", "deps", "refs", "checklist", "closed", "reviewed",
}

// stateVocab is the is: flag set in v1.
var stateVocab = []string{"actionable", "blocked", "stuck", "open", "closed", "draft", "container"}

// compileQuery parses raw -q text and binds it to a predicate over the loaded
// index. Validation faults (bad grammar, unknown field/flag, an operator on a
// non-ordinal field, an unknown lane/type value) are exit-2 errors carrying a
// stable kebab id and, where the input almost resolved, candidates. A nil
// predicate is returned only with a non-nil error; an empty query compiles to a
// match-everything predicate.
func (a *App) compileQuery(raw string, idx *core.Index) (func(*core.Task) bool, error) {
	q, err := query.Parse(raw)
	if err != nil {
		var pe *query.ParseError
		if errors.As(err, &pe) {
			e := &core.Error{Code: core.CodeValidation, ID: "query-parse", Msg: "invalid query: " + pe.Error()}
			return nil, e
		}
		return nil, core.Validationf("query-parse", "invalid query: %v", err)
	}

	// Precompute shared state only when a term needs it.
	var doneIDs map[string]bool
	var kids map[string][]*core.Task
	for _, t := range q {
		if t.Kind == query.State {
			switch t.Field {
			case "actionable", "blocked", "stuck":
				if doneIDs == nil {
					doneIDs = a.doneSet(idx)
				}
			}
			if t.Field == "stuck" && kids == nil {
				kids = childrenMap(idx)
			}
		}
	}

	preds := make([]func(*core.Task) bool, 0, len(q))
	for _, term := range q {
		p, err := a.compileTerm(term, idx, doneIDs, kids)
		if err != nil {
			return nil, err
		}
		preds = append(preds, p)
	}
	return func(t *core.Task) bool {
		for _, p := range preds {
			if !p(t) {
				return false
			}
		}
		return true
	}, nil
}

// compileTerm builds one term's matcher, with Not already folded in.
func (a *App) compileTerm(term query.Term, idx *core.Index, doneIDs map[string]bool, kids map[string][]*core.Task) (func(*core.Task) bool, error) {
	neg := func(base func(*core.Task) bool) func(*core.Task) bool {
		if !term.Not {
			return base
		}
		return func(t *core.Task) bool { return !base(t) }
	}

	switch term.Kind {
	case query.FreeText:
		needle := term.Text
		return neg(func(t *core.Task) bool { return containsFold(t.Title, needle) }), nil

	case query.State:
		if !contains(stateVocab, term.Field) {
			return nil, unknownQueryErr("query-unknown-flag", "unknown is: flag "+strconv.Quote(term.Field), stateVocab)
		}
		f := term.Field
		return neg(func(t *core.Task) bool {
			switch f {
			case "actionable":
				return a.actionable(idx, t, doneIDs)
			case "blocked":
				return len(blockedDeps(t, doneIDs)) > 0
			case "stuck":
				return a.isStuck(idx, t.ID, kids, doneIDs)
			case "open":
				return t.Closed == nil
			case "closed":
				return t.Closed != nil
			case "draft":
				return len(t.Repos) == 0
			case "container":
				return a.Cfg.IsContainerType(t.Type)
			}
			return false
		}), nil

	case query.Presence:
		if !contains(presenceVocab, term.Field) {
			return nil, unknownQueryErr("query-unknown-field", "has:/no: unknown field "+strconv.Quote(term.Field), presenceVocab)
		}
		f := term.Field
		return neg(func(t *core.Task) bool {
			switch f {
			case "label":
				return len(t.Labels) > 0
			case "repo":
				return len(t.Repos) > 0
			case "parent":
				return t.Parent != ""
			case "value":
				return t.Value != nil
			case "effort":
				return t.Effort != nil
			case "deps":
				return len(t.Deps) > 0
			case "refs":
				return len(t.Refs) > 0
			case "checklist":
				return len(t.Checklist) > 0
			case "closed":
				return t.Closed != nil
			case "reviewed":
				return t.Reviewed != nil
			}
			return false
		}), nil

	case query.Qualifier:
		return a.compileQualifier(term, neg)
	}
	return nil, core.Validationf("query-parse", "unhandled query term")
}

// compileQualifier binds a field:value qualifier.
func (a *App) compileQualifier(term query.Term, neg func(func(*core.Task) bool) func(*core.Task) bool) (func(*core.Task) bool, error) {
	f := term.Field
	// Ordinal fields take comparisons/ranges; everything else is equality only.
	ordinal := f == "value" || f == "effort" || f == "priority" || f == "roi"
	if term.Op != query.Eq && !ordinal {
		return nil, unknownQueryErr("query-type", "field "+strconv.Quote(f)+" takes an equality value, not a comparison/range (only value/effort/priority/roi are ordinal)", nil)
	}

	switch f {
	case "status", "lane":
		for _, v := range term.Values {
			if !a.Cfg.IsLane(v) {
				return nil, a.unknownLaneErr("", v)
			}
		}
		return neg(func(t *core.Task) bool { return contains(term.Values, t.Status) }), nil

	case "type":
		for _, v := range term.Values {
			if !a.Cfg.IsType(v) {
				return nil, a.unknownTypeErr("", v)
			}
		}
		return neg(func(t *core.Task) bool { return contains(term.Values, a.Cfg.EffectiveType(t.Type)) }), nil

	case "label":
		return neg(func(t *core.Task) bool { return anyContains(t.Labels, term.Values) }), nil

	case "repo":
		return neg(func(t *core.Task) bool { return anyRepoMatch(t.Repos, term.Values) }), nil

	case "id":
		return neg(func(t *core.Task) bool {
			for _, v := range term.Values {
				if t.ID == v || strings.HasPrefix(t.ID, v) {
					return true
				}
			}
			return false
		}), nil

	case "parent":
		return neg(func(t *core.Task) bool { return contains(term.Values, t.Parent) }), nil

	case "title":
		return neg(func(t *core.Task) bool { return anyContainsFold(t.Title, term.Values) }), nil

	case "value", "effort", "priority", "roi":
		return a.compileNumeric(term, neg)
	}
	return nil, unknownQueryErr("query-unknown-field", "unknown qualifier "+strconv.Quote(f), qualifierVocab)
}

// compileNumeric binds an ordinal field (value/effort/priority/roi) with an
// equality, comparison, or range. An unset value/effort (and an undefined roi)
// never satisfies a comparison or range, mirroring how they sort last.
func (a *App) compileNumeric(term query.Term, neg func(func(*core.Task) bool) func(*core.Task) bool) (func(*core.Task) bool, error) {
	f := term.Field
	// getVal returns the task's numeric value and whether it is defined.
	getVal := func(t *core.Task) (float64, bool) {
		switch f {
		case "value":
			if t.Value == nil {
				return 0, false
			}
			return float64(*t.Value), true
		case "effort":
			if t.Effort == nil {
				return 0, false
			}
			return float64(*t.Effort), true
		case "priority":
			return float64(t.Priority), true
		case "roi":
			if t.Value == nil || t.Effort == nil || *t.Effort <= 0 {
				return 0, false
			}
			return t.ROI(), true
		}
		return 0, false
	}

	parse := func(s string) (float64, error) {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, unknownQueryErr("query-type", "field "+strconv.Quote(f)+" needs a number, got "+strconv.Quote(s), nil)
		}
		return n, nil
	}

	switch term.Op {
	case query.Between:
		var lo, hi float64
		haveLo, haveHi := term.Values[0] != "*", term.Values[1] != "*"
		if haveLo {
			n, err := parse(term.Values[0])
			if err != nil {
				return nil, err
			}
			lo = n
		}
		if haveHi {
			n, err := parse(term.Values[1])
			if err != nil {
				return nil, err
			}
			hi = n
		}
		return neg(func(t *core.Task) bool {
			v, ok := getVal(t)
			if !ok {
				return false
			}
			return (!haveLo || v >= lo) && (!haveHi || v <= hi)
		}), nil
	case query.Eq:
		n, err := parse(term.Values[0])
		if err != nil {
			return nil, err
		}
		return neg(func(t *core.Task) bool {
			v, ok := getVal(t)
			return ok && v == n
		}), nil
	default: // Gt/Ge/Lt/Le
		n, err := parse(term.Values[0])
		if err != nil {
			return nil, err
		}
		op := term.Op
		return neg(func(t *core.Task) bool {
			v, ok := getVal(t)
			if !ok {
				return false
			}
			switch op {
			case query.Gt:
				return v > n
			case query.Ge:
				return v >= n
			case query.Lt:
				return v < n
			case query.Le:
				return v <= n
			}
			return false
		}), nil
	}
}

// unknownQueryErr builds an exit-2 query validation error with a stable id and
// optional did-you-mean candidates.
func unknownQueryErr(id, msg string, candidates []string) *core.Error {
	e := &core.Error{Code: core.CodeValidation, ID: id, Msg: msg}
	if len(candidates) > 0 {
		e.Candidates = append([]string(nil), candidates...)
	}
	return e
}

// containsFold reports a case-insensitive substring match.
func containsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

func anyContainsFold(hay string, needles []string) bool {
	for _, n := range needles {
		if containsFold(hay, n) {
			return true
		}
	}
	return false
}

// anyContains reports whether set contains any of vals.
func anyContains(set, vals []string) bool {
	for _, v := range vals {
		if contains(set, v) {
			return true
		}
	}
	return false
}

// anyRepoMatch matches a repo value against a task's repos by full owner/repo or
// by short name (the segment after the last '/').
func anyRepoMatch(repos, vals []string) bool {
	for _, v := range vals {
		for _, r := range repos {
			if r == v || shortRepo(r) == v {
				return true
			}
		}
	}
	return false
}

func shortRepo(r string) string {
	if i := strings.LastIndex(r, "/"); i >= 0 && i+1 < len(r) {
		return r[i+1:]
	}
	return r
}
