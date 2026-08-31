package app

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/query"
)

// qualifierVocab is the set of field qualifiers `-q` understands (the
// candidates offered on an unknown field): the enum/text fields, the ordinals,
// the five dates (four system stamps + the promised `due`), the direct-edge
// graph qualifiers, and their transitive twins (descendant-of/ancestor-of —
// the deps DAG walked to a fixpoint; the v5 parent hierarchy they were first
// sketched against is gone, and epic membership is the epic: qualifier).
var qualifierVocab = []string{
	"status", "lane", "epic", "label", "repo", "id", "title", "body",
	"value", "effort", "priority", "roi",
	"created", "updated", "closed", "reviewed", "due",
	"depends-on", "blocks", "descendant-of", "ancestor-of",
}

// presenceVocab is the field set has:/no: accept.
var presenceVocab = []string{
	"label", "repo", "epic", "value", "effort", "deps", "refs", "checklist", "closed", "reviewed", "body", "due",
}

// stateVocab is the is: flag set.
var stateVocab = []string{"actionable", "blocked", "stale", "open", "closed", "draft", "unfiled", "overdue"}

// isDateField reports whether f is one of the date qualifiers — the four system
// timestamps plus `due`, the one date a human sets rather than furrow stamping.
func isDateField(f string) bool {
	return f == "created" || f == "updated" || f == "closed" || f == "reviewed" || f == "due"
}

// taskPred is a compiled `-q` predicate over one task. Evaluation may load a
// body on demand (free text, body:, has:body), so it can fail with a store
// error — callers abort the whole read on a non-nil error rather than treating
// an unreadable body as a non-match.
type taskPred func(*core.Task) (bool, error)

// queryPred compiles a raw `-q` string against the loaded index, or returns
// (nil, nil) when there is no query — the one entry point every filtering read
// (ls/next/revisit/stats/search) funnels through, so `-q` means the same thing
// everywhere. staleDays feeds `is:stale`: normally the config's
// [revisit].stale_days; Revisit passes its effective (possibly --stale-days
// overridden) window so `revisit -q is:stale` and revisit's own stale signal
// can never disagree within one call. loadBody resolves a task's body for the
// body-matching terms — nil means the hot store's; a read filtering a DIFFERENT
// index (ls --archived) must pass that store's loader, or `body:` terms would
// silently search the wrong bodies.
func (a *App) queryPred(raw string, idx *core.Index, staleDays int, loadBody func(string) (string, error)) (taskPred, error) {
	p, _, err := a.queryPredShared(raw, idx, staleDays, loadBody)
	return p, err
}

// queryPredShared is queryPred plus the compiler's own cached body reader —
// for a caller that reads bodies ITSELF after filtering (Search's snippet
// scan): reading through the shared per-run cache keeps each body at ONE load
// even when the query also carried a body term (`search -q body:x term` used
// to pay two). The reader is nil when there is no query — with no compiler
// there is no cache to share, and the caller's own loader is already single-read.
func (a *App) queryPredShared(raw string, idx *core.Index, staleDays int, loadBody func(string) (string, error)) (taskPred, func(*core.Task) (string, error), error) {
	if raw == "" {
		return nil, nil, nil
	}
	return a.compileQuery(raw, idx, staleDays, loadBody)
}

// queryCompiler carries the state a query's terms bind against: the loaded
// index, the compile-time instant (ONE Clock read per query, so every relative
// date and staleness test in a pass agrees), the stale threshold, and
// lazily-built derived state — the done set, the children map, and the body
// cache. Bodies are loaded on demand and only by terms that read them, so a
// query with no text-over-body term never pays for a single body read.
type queryCompiler struct {
	app       *App
	idx       *core.Index
	now       time.Time
	staleDays int
	loadBody  func(string) (string, error)
	doneIDs   map[string]bool
	bodies    map[string]string
	bodyErr   error
}

// body returns t's body, loading it once per id. A store failure is parked in
// bodyErr — a per-term predicate cannot return an error — and surfaced by the
// compiled taskPred right after the failing term evaluates, failing the read.
func (c *queryCompiler) body(t *core.Task) string {
	if b, ok := c.bodies[t.ID]; ok {
		return b
	}
	b, err := c.loadBody(t.ID)
	if err != nil {
		if c.bodyErr == nil {
			c.bodyErr = err
		}
		return ""
	}
	if c.bodies == nil {
		c.bodies = map[string]string{}
	}
	c.bodies[t.ID] = b
	return b
}

// compileQuery parses raw -q text and binds it to a predicate over the loaded
// index. Validation faults (bad grammar, unknown field/flag, an operator on a
// non-ordered field, an unknown lane/type value, a malformed date) are exit-2
// errors carrying a stable kebab id and, where the input almost resolved,
// candidates. A nil predicate is returned only with a non-nil error; an empty
// query compiles to a match-everything predicate. The second return is the
// compiler's cached body reader (see queryPredShared).
func (a *App) compileQuery(raw string, idx *core.Index, staleDays int, loadBody func(string) (string, error)) (taskPred, func(*core.Task) (string, error), error) {
	q, err := query.Parse(raw)
	if err != nil {
		e := &core.Error{Code: core.CodeValidation, Kind: core.KindQueryParse, Msg: "invalid query: " + err.Error()}
		var pe *query.ParseError
		if errors.As(err, &pe) {
			// term + offset: enough for a front-end (vista's filter bar) to
			// underline the offending token without re-lexing the query.
			e.Details = map[string]any{"term": pe.Term, "offset": pe.Offset}
		}
		return nil, nil, e
	}

	if loadBody == nil {
		loadBody = a.Store.LoadBody
	}
	c := &queryCompiler{app: a, idx: idx, now: a.Clock.Now(), staleDays: staleDays, loadBody: loadBody}
	// Precompute shared derived state only when a term needs it.
	for _, t := range q {
		if t.Kind == query.State {
			switch t.Field {
			case "actionable", "blocked":
				if c.doneIDs == nil {
					c.doneIDs = a.doneSet(idx)
				}
			}
		}
	}

	preds := make([]func(*core.Task) bool, 0, len(q))
	for _, term := range q {
		p, err := c.compileTerm(term)
		if err != nil {
			return nil, nil, err
		}
		preds = append(preds, p)
	}
	pred := func(t *core.Task) (bool, error) {
		for _, p := range preds {
			ok := p(t)
			if c.bodyErr != nil {
				return false, c.bodyErr
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return pred, c.Body, nil
}

// Body is the compiler's cached body reader in error-returning form — the
// per-run cache exposed to a caller that reads bodies after filtering, so a
// body is loaded once whether the query, the caller, or both need it.
func (c *queryCompiler) Body(t *core.Task) (string, error) {
	b := c.body(t)
	if c.bodyErr != nil {
		return "", c.bodyErr
	}
	return b, nil
}

// compileTerm builds one term's matcher, with Not already folded in.
func (c *queryCompiler) compileTerm(term query.Term) (func(*core.Task) bool, error) {
	a := c.app
	neg := func(base func(*core.Task) bool) func(*core.Task) bool {
		if !term.Not {
			return base
		}
		return func(t *core.Task) bool { return !base(t) }
	}

	switch term.Kind {
	case query.FreeText:
		// Free text = furrow search's matcher over title + body (case-insensitive
		// substring, core.ContainsFold), so `-q foo` finds what `furrow search
		// foo` finds. The body is consulted only when the title misses, so a
		// title hit never pays for a body read.
		needle := term.Text
		return neg(func(t *core.Task) bool {
			return core.ContainsFold(t.Title, needle) || core.ContainsFold(c.body(t), needle)
		}), nil

	case query.State:
		if !contains(stateVocab, term.Field) {
			return nil, termErr(unknownQueryErr(core.KindQueryUnknownFlag, "unknown is: flag "+strconv.Quote(term.Field), stateVocab), term, nil)
		}
		f := term.Field
		return neg(func(t *core.Task) bool {
			switch f {
			case "actionable":
				return a.actionable(c.idx, t, c.doneIDs)
			case "blocked":
				return len(blockedDeps(t, c.doneIDs)) > 0
			case "stale":
				return core.IsStale(*t, c.now, c.staleDays)
			case "open":
				return t.Closed == nil
			case "closed":
				return t.Closed != nil
			case "draft":
				return len(t.Repos) == 0
			case "unfiled":
				// No box. The lint-error state, queryable so a backfill sweep is one
				// read rather than a diff of two lists.
				return t.Epic == ""
			case "overdue":
				// The promised instant has passed — lint's due-overdue as a filter,
				// through the same predicate, so a query can never disagree with the
				// error. Lane exclusions are deliberately NOT applied: `-q` filters what
				// the caller asked for (`is:overdue -s icebox` is a legitimate audit),
				// and the lint rule is where the "which lanes nag" policy lives.
				return core.DueStateOf(t, c.now, a.loc()) == core.DueOverdue
			}
			return false
		}), nil

	case query.Presence:
		if !contains(presenceVocab, term.Field) {
			return nil, termErr(unknownQueryErr(core.KindQueryUnknownField, "has:/no: unknown field "+strconv.Quote(term.Field), presenceVocab), term, nil)
		}
		f := term.Field
		return neg(func(t *core.Task) bool {
			switch f {
			case "label":
				return len(t.Labels) > 0
			case "repo":
				return len(t.Repos) > 0
			case "epic":
				return t.Epic != ""
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
			case "due":
				return t.Due != nil
			case "body":
				// Non-whitespace body content. Note `add` seeds every body with
				// a heading, so no:body means a body someone deliberately
				// emptied, not "never written to".
				return strings.TrimSpace(c.body(t)) != ""
			}
			return false
		}), nil

	case query.Qualifier:
		return c.compileQualifier(term, neg)
	}
	return nil, core.Validationf("query-parse", "unhandled query term")
}

func (c *queryCompiler) compileQualifier(term query.Term, neg func(func(*core.Task) bool) func(*core.Task) bool) (func(*core.Task) bool, error) {
	a := c.app
	f := term.Field
	// Validate the FIELD before the operator. Reversed, a misspelling that
	// happened to carry an operator (`updatd:>=-2w` — and the date syntax is
	// documented only with >= and .. idioms, so that is the likeliest typo)
	// fell into the ordered-field gate and was answered with `query-type` and
	// NO candidates, in a message asserting the field exists.
	if !contains(qualifierVocab, f) {
		return nil, termErr(unknownQueryErr(core.KindQueryUnknownField, "unknown qualifier "+strconv.Quote(f), qualifierVocab), term, nil)
	}
	// Ordered fields — the ordinals and the date timestamps — take
	// comparisons/ranges; everything else is equality only. The type fault
	// carries the field's allowed operators in details (t-ehk7's unmet v1
	// acceptance line), so a front-end can offer the legal ones instead of
	// parsing them out of the message.
	ordinal := f == "value" || f == "effort" || f == "priority" || f == "roi"
	if term.Op != query.Eq && !ordinal && !isDateField(f) {
		return nil, termErr(
			unknownQueryErr(core.KindQueryType, "field "+strconv.Quote(f)+" takes an equality value, not a comparison/range (only value/effort/priority/roi and created/updated/closed/reviewed/due are ordered)", nil),
			term, map[string]any{"allowed_operators": []string{"="}})
	}

	switch f {
	case "status", "lane":
		for _, v := range term.Values {
			if !a.Cfg.IsLane(v.Text) {
				return nil, a.unknownLaneErr("", v.Text)
			}
		}
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool { return contains(vals, t.Status) }), nil

	case "epic":
		// Exact epic ids. Lenient on an unknown id (like id:): an epic that does
		// not exist simply has no members, and `furrow lint` owns reporting the
		// dangling reference. The CLI's -e flag is the strict path (ResolveEpic
		// gives candidates on a typo).
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool { return contains(vals, t.Epic) }), nil

	case "label":
		// A value containing `*` is a wildcard (the v2 token reserved since v1):
		// `label:*ui*` matches any label with "ui" inside, `label:area/*` any
		// label under the prefix. `*` spans any run (including empty); matching
		// stays exact-per-label and case-sensitive, like the plain form — the
		// wildcard widens WHERE a value can match, never how letters compare.
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool {
			for _, v := range vals {
				for _, l := range t.Labels {
					if matchWildcard(v, l) {
						return true
					}
				}
			}
			return false
		}), nil

	case "repo":
		// Resolve through the SAME strict path -r uses (resolveRepoIn over the
		// board's repo universe), so `-q repo:` cannot disagree with `-r`: an
		// ambiguous short name is exit 2 with candidates and casing folds. The
		// hand-rolled suffix match this replaces was case-SENSITIVE and had no
		// uniqueness check, so `-q repo:Furrow` answered [] where `-r Furrow`
		// resolved, and on a two-owner board a short name silently unioned across
		// both — a scoped read quietly crossing a repo boundary.
		universe := repoUniverse(c.idx, a.BoardRepos)
		resolved := make([]string, 0, len(term.Values))
		for _, v := range term.Values {
			r, err := resolveRepoIn(v.Text, "", universe)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, r)
		}
		return neg(func(t *core.Task) bool { return anyRepoMatch(t.Repos, resolved) }), nil

	case "id":
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool {
			for _, v := range vals {
				if t.ID == v || strings.HasPrefix(t.ID, v) {
					return true
				}
			}
			return false
		}), nil

	case "depends-on":
		// t waits on any named id (the named task blocks t) — the Deps edge
		// read from the dependent's side, Index.Dependents' membership test.
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool { return anyContains(t.Deps, vals) }), nil

	case "blocks":
		// t blocks any named id — the same edge read from the other side:
		// t ∈ X.Deps. Each named X is resolved once at compile; an unknown id
		// blocks nothing (lenient, exit 0).
		blocked := map[string]bool{}
		for _, v := range term.Values {
			if x, _ := c.idx.Find(v.Text); x != nil {
				for _, d := range x.Deps {
					blocked[d] = true
				}
			}
		}
		return neg(func(t *core.Task) bool { return blocked[t.ID] }), nil

	case "descendant-of":
		// depends-on's transitive twin: t (transitively) waits on any named id —
		// X's descendants are everything DOWNSTREAM of it in the deps DAG. The
		// closure is computed once at compile (O(edges)); an unknown id has no
		// descendants (lenient, like blocks:); the named task itself is not its
		// own descendant, mirroring how depends-on:X never matches X.
		set := c.reach(valTexts(term.Values), false)
		return neg(func(t *core.Task) bool { return set[t.ID] }), nil

	case "ancestor-of":
		// blocks' transitive twin: t is anything any named id (transitively)
		// waits on — X's ancestors are UPSTREAM, the work that must land first.
		set := c.reach(valTexts(term.Values), true)
		return neg(func(t *core.Task) bool { return set[t.ID] }), nil

	case "title":
		vals := term.Values
		return neg(func(t *core.Task) bool { return anyTextMatch(t.Title, vals, true) }), nil

	case "body":
		// The explicit opt-in to body text — loads bodies on demand (O(board)
		// reads at worst), like furrow search. Quoting does NOT flip this to
		// whole-field equality the way it does on title:: the "field" here is the
		// entire markdown document, so exact-match has no use, while quoting IS
		// how you include a space — `body:'zebra paragraph'` must stay a phrase
		// search or it has no spelling at all.
		vals := term.Values
		return neg(func(t *core.Task) bool { return anyTextMatch(c.body(t), vals, false) }), nil

	case "created", "updated", "closed", "reviewed", "due":
		return c.compileDate(term, neg)

	case "value", "effort", "priority", "roi":
		return compileNumeric(term, neg)
	}
	// Unreachable: the vocabulary gate above already rejected an unknown field.
	// Kept as a compile-time-exhaustive backstop in case the two lists drift.
	return nil, unknownQueryErr(core.KindQueryUnknownField, "unknown qualifier "+strconv.Quote(f), qualifierVocab)
}

// compileNumeric binds an ordinal field (value/effort/priority/roi) with an
// equality, comparison, or range. An unset value/effort (and an undefined roi)
// never satisfies a comparison or range, mirroring how they sort last.
func compileNumeric(term query.Term, neg func(func(*core.Task) bool) func(*core.Task) bool) (func(*core.Task) bool, error) {
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
			return 0, unknownQueryErr(core.KindQueryType, "field "+strconv.Quote(f)+" needs a number, got "+strconv.Quote(s), nil)
		}
		return n, nil
	}

	switch term.Op {
	case query.Between:
		var lo, hi float64
		haveLo, haveHi := term.Values[0].Text != "*", term.Values[1].Text != "*"
		if haveLo {
			n, err := parse(term.Values[0].Text)
			if err != nil {
				return nil, err
			}
			lo = n
		}
		if haveHi {
			n, err := parse(term.Values[1].Text)
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
		// Comma is OR on EVERY qualifier — the rule README and the glossary state
		// unconditionally. Parsing only Values[0] made `value:2,4` silently answer
		// with the value-2 tasks alone at exit 0, which is the one failure shape
		// this repo's read contract exists to prevent. compileDate does the same
		// loop; labels, ids and lanes always did.
		ns := make([]float64, 0, len(term.Values))
		for _, v := range term.Values {
			n, err := parse(v.Text)
			if err != nil {
				return nil, err
			}
			ns = append(ns, n)
		}
		return neg(func(t *core.Task) bool {
			v, ok := getVal(t)
			if !ok {
				return false
			}
			for _, n := range ns {
				if v == n {
					return true
				}
			}
			return false
		}), nil
	default: // Gt/Ge/Lt/Le
		n, err := parse(term.Values[0].Text)
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

// unknownQueryErr builds an exit-2 query validation error with a stable kind
// and optional did-you-mean candidates.
func unknownQueryErr(kind, msg string, candidates []string) *core.Error {
	e := &core.Error{Code: core.CodeValidation, Kind: kind, Msg: msg}
	if len(candidates) > 0 {
		e.Candidates = append([]string(nil), candidates...)
	}
	return e
}

// termErr stamps the failing term's source position (and any extra keys) onto
// a query error's details — the binder-side half of the ParseError contract,
// so an unknown field or a type fault underlines exactly like a parse fault.
func termErr(e *core.Error, term query.Term, extra map[string]any) *core.Error {
	d := map[string]any{"term": term.Raw, "offset": term.Offset}
	for k, v := range extra {
		d[k] = v
	}
	e.Details = d
	return e
}

// valTexts projects an OR-set's texts (for the equality fields, where
// quoted-ness carries no extra meaning — quotes only protected separators).
func valTexts(vals []query.Value) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v.Text
	}
	return out
}

// anyTextMatch matches a text field against an OR-set: a bare value is a
// case-insensitive substring (core.ContainsFold — search's matcher). With
// exactOnQuote, a quoted value ('Bug fix') means whole-field equality, still
// case-folded — quoting flips substring→exact without introducing the
// language's only case-sensitive corner. body: passes false: its "field" is a
// whole markdown document, so equality is useless there while quoting is still
// the only way to write a phrase.
func anyTextMatch(field string, vals []query.Value, exactOnQuote bool) bool {
	for _, v := range vals {
		if v.Quoted && exactOnQuote {
			if strings.EqualFold(field, v.Text) {
				return true
			}
		} else if core.ContainsFold(field, v.Text) {
			return true
		}
	}
	return false
}

func anyContains(set, vals []string) bool {
	for _, v := range vals {
		if contains(set, v) {
			return true
		}
	}
	return false
}

// anyRepoMatch reports whether the task carries any of the ALREADY-RESOLVED
// repos. Resolution (short name -> unique owner/repo, case folding, the
// ambiguity error) happens once at compile time through resolveRepoIn, so this
// is a plain membership test and cannot drift from what -r accepts. EqualFold,
// not ==, because a hand-edited shard may carry a casing the universe entry
// does not — the same leniency resolveRepoIn applies on the way in.
func anyRepoMatch(repos, vals []string) bool {
	for _, v := range vals {
		for _, r := range repos {
			if strings.EqualFold(v, r) {
				return true
			}
		}
	}
	return false
}

// reach computes the transitive closure of the deps DAG from the named start
// ids — upstream (what they wait on, following Deps) when up, downstream (what
// waits on them, following the reversed edges) otherwise. Start ids are not in
// their own closure. A visited set makes a cycle (possible on disk; lint's
// dep-cycle owns reporting it) terminate instead of hanging the read.
func (c *queryCompiler) reach(starts []string, up bool) map[string]bool {
	adj := map[string][]string{}
	for i := range c.idx.Tasks {
		t := &c.idx.Tasks[i]
		for _, d := range t.Deps {
			if up {
				adj[t.ID] = append(adj[t.ID], d)
			} else {
				adj[d] = append(adj[d], t.ID)
			}
		}
	}
	seen := map[string]bool{}
	var frontier []string
	for _, s := range starts {
		if x, _ := c.idx.Find(s); x != nil {
			frontier = append(frontier, x.ID)
		}
	}
	starting := map[string]bool{}
	for _, s := range frontier {
		starting[s] = true
	}
	for len(frontier) > 0 {
		id := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, next := range adj[id] {
			if !seen[next] {
				seen[next] = true
				frontier = append(frontier, next)
			}
		}
	}
	for s := range starting {
		delete(seen, s)
	}
	return seen
}

// matchWildcard matches s against a `*`-pattern: `*` spans any run (including
// empty), everything else is literal. A pattern with no `*` is plain equality,
// so the label qualifier's exact semantics are unchanged for v1 spellings.
func matchWildcard(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.Split(pattern, "*")
	// Anchors: the first/last segment must sit at the string's edges.
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	if !strings.HasSuffix(s, last) {
		return false
	}
	s = s[:len(s)-len(last)]
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return true
}
