package app

import (
	"errors"
	"fmt"
	"sort"
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
	"status", "lane", "epic", "anchor", "label", "repo", "id", "title", "body",
	"value", "effort", "priority", "roi",
	"created", "updated", "closed", "reviewed", "due",
	"depends-on", "blocks", "descendant-of", "ancestor-of",
}

// presenceVocab is the field set has:/no: accept.
var presenceVocab = []string{
	"label", "repo", "epic", "value", "effort", "deps", "refs", "checklist", "closed", "reviewed", "body", "due", "repeat", "anchor",
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

// queryRead is what a filtering read tells the compiler about the index it
// hands over. staleDays feeds `is:stale`: normally the config's
// [revisit].stale_days; Revisit passes its effective (possibly --stale-days
// overridden) window so `revisit -q is:stale` and revisit's own stale signal
// can never disagree within one call. loadBody resolves a task's body for the
// body-matching terms — nil means the hot store's; a read filtering a
// DIFFERENT index (ls --archived) must pass that store's loader, or `body:`
// terms would silently search the wrong bodies. archived says the index IS
// that other snapshot, which decides where a ref binder looks for a task the
// snapshot does not hold (the live board, not the archive). wider is for the
// one read that unions two snapshots — stats' --since/--until window scans
// the hot index and the archive in turn — and names the OTHER one: a ref that
// snapshot knows is not refused by this pass, since the other pass answers it.
type queryRead struct {
	staleDays int
	loadBody  func(string) (string, error)
	archived  bool
	wider     *core.Index
}

// queryPred compiles a raw `-q` string against the loaded index, or returns
// (nil, nil) when there is no query — the one entry point every filtering read
// (ls/next/revisit/stats/search) funnels through, so `-q` means the same thing
// everywhere.
func (a *App) queryPred(raw string, idx *core.Index, r queryRead) (taskPred, error) {
	p, _, err := a.queryPredShared(raw, idx, r)
	return p, err
}

// queryPredShared is queryPred plus the compiler's own cached body reader —
// for a caller that reads bodies ITSELF after filtering (Search's snippet
// scan): reading through the shared per-run cache keeps each body at ONE load
// even when the query also carried a body term (`search -q body:x term` used
// to pay two). The reader is nil when there is no query — with no compiler
// there is no cache to share, and the caller's own loader is already single-read.
func (a *App) queryPredShared(raw string, idx *core.Index, r queryRead) (taskPred, func(*core.Task) (string, error), error) {
	if raw == "" {
		return nil, nil, nil
	}
	return a.compileQuery(raw, idx, r)
}

// queryCompiler carries the state a query's terms bind against: the loaded
// index and what the read said about it (queryRead), the compile-time instant
// (ONE Clock read per query, so every relative date and staleness test in a
// pass agrees), and lazily-built derived state — the done set, the body
// cache, the box list, the dep-target set. Each is built on first use and only
// by a term that needs it, so a query with no body term never pays for a body
// read and one with no ref never loads the boxes.
type queryCompiler struct {
	app         *App
	idx         *core.Index
	now         time.Time
	staleDays   int
	loadBody    func(string) (string, error)
	archived    bool
	wider       *core.Index
	doneIDs     map[string]bool
	bodies      map[string]string
	bodyErr     error
	epics       []core.Epic
	epicsLoaded bool
	targets     map[string]bool
	otherIdx    *core.Index
	otherLoaded bool
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

// epicList loads the board's boxes once per query — only a term that resolves
// an epic ref (epic:/anchor:) pays for the read.
func (c *queryCompiler) epicList() ([]core.Epic, error) {
	if !c.epicsLoaded {
		epics, err := c.app.Store.LoadEpics()
		if err != nil {
			return nil, err
		}
		c.epics, c.epicsLoaded = epics, true
	}
	return c.epics, nil
}

// depTargets is the set of ids this snapshot's tasks name in Deps — its edge
// universe, built once on first use. A retired or removed far end stays in
// it: the edge is a fact of this snapshot even when the task it points at is
// not, which is what lets `depends-on:<archived id>` list the live tasks that
// still carry it.
func (c *queryCompiler) depTargets() map[string]bool {
	if c.targets == nil {
		c.targets = map[string]bool{}
		for i := range c.idx.Tasks {
			for _, d := range c.idx.Tasks[i].Deps {
				c.targets[d] = true
			}
		}
	}
	return c.targets
}

// carries reports whether any task of idx carries literal in field — its Epic
// or Anchor pointer, or (any other field) a Deps entry. It is the ref binders'
// literal fallback: a ref the resolver cannot place but this snapshot already
// points at names something real to this read, the pointer itself, and the
// match set is non-empty by construction, so accepting it can never be a
// silent 0 rows. That keeps reachable the repairs the resolver alone would
// refuse — `set -q epic:<removed box> -e <new>` after `epic rm`, `set -q
// anchor:<removed box> --clear-anchor`, `ls -q depends-on:<retired dep>` —
// and lets an --archived read name a box only the archive still carries.
func carries(idx *core.Index, field, literal string) bool {
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		switch field {
		case "epic":
			if t.Epic == literal {
				return true
			}
		case "anchor":
			if t.Anchor == literal {
				return true
			}
		default:
			if contains(t.Deps, literal) {
				return true
			}
		}
	}
	return false
}

// carriers counts the tasks of idx that carry literal in field (carries, as a
// count — the ambiguity message says how many pointers stand behind the
// literal).
func carriers(idx *core.Index, field, literal string) int {
	n := 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		switch {
		case field == "epic" && t.Epic == literal, field == "anchor" && t.Anchor == literal:
			n++
		}
	}
	return n
}

// epicNotFound is resolveEpicIn's miss, built here for the quoted form (which
// never calls the resolver): the same kind, message and candidates.
func epicNotFound(ref string, epics []core.Epic) *core.Error {
	ids := make([]string, 0, len(epics))
	for i := range epics {
		ids = append(ids, epics[i].ID)
	}
	sort.Strings(ids)
	return &core.Error{Code: core.CodeValidation, Kind: core.KindEpicNotFound, Msg: fmt.Sprintf("unknown epic %q", ref), Candidates: ids}
}

// resolveEpicRef binds one epic ref against the boxes and the pointers the
// snapshot carries. Unquoted, it is the SAME resolution -e uses (resolveEpicIn:
// exact id, else unique id prefix, else unique case-folded title substring),
// so `-q epic:X` cannot disagree with `-e X`; quoted, it is exact only — the
// id, else the pointer itself, else the whole title (case-folded, as
// title:'…' is whole-field) — which is how a pointer named in candidates is
// selected, even one that spells a box's whole title (the box has its id).
// Beyond -e: a pointer the snapshot (or a union read's other snapshot) carries
// in the field but no box answers to resolves as that literal — the carriers,
// non-empty by construction, which keeps `set -q epic:<gone> -e <new>` and
// `set -q anchor:<gone> --clear-anchor` reachable after `epic rm`; and past an
// exact id, a carried pointer beside a box the resolver found by prefix or
// title is TWO targets, so it is epic-ambiguous with both as candidates —
// each order of preference was measured wrong (a live box whose title
// mentioned a removed box's id shadowed its carriers; a corrupt pointer
// holding a box's title hid that box's members from epic: while -e listed
// them). A miss says where else the pointer lives (the other store's tasks),
// with the read that sees it: this read does not resolve there.
func (c *queryCompiler) resolveEpicRef(field string, v query.Value, epics []core.Epic) (string, *core.Error) {
	lit := v.Text
	for i := range epics {
		if epics[i].ID == lit {
			// An exact box id: the one target a carried copy of it also names.
			return lit, nil
		}
	}
	here := carriers(c.idx, field, lit)
	total := here
	if c.wider != nil {
		total += carriers(c.wider, field, lit)
	}
	carried := total > 0
	var byTitle []string
	for i := range epics {
		if strings.EqualFold(epics[i].Title, lit) {
			byTitle = append(byTitle, epics[i].ID)
		}
	}
	sort.Strings(byTitle)
	// The pointer's own story, for the ambiguity messages: how many tasks
	// carry it (both snapshots in a union read), lint's name for the dangling
	// pointer where lint looks (the live board's own carriers), and how the
	// carriers are selected — the quoted spelling, unless the pointer is also
	// a box's whole title (then quoting is the same two targets again: name
	// the box by id, and the carriers by what names them) or only the other
	// store's tasks carry it (then the read that lists them).
	pointer := fmt.Sprintf("%d task(s) carry it as a pointer (no live box answers to it", total)
	if !c.archived && here > 0 {
		pointer += fmt.Sprintf(" — lint's %s-missing", field)
	}
	pointer += ")"
	carriersBy := fmt.Sprintf("quote it (%s:'%s') for the carriers", field, lit)
	switch {
	case len(byTitle) > 0:
		// Tested first: the other store's read would meet this same arm.
		carriersBy = "the pointer is also a whole title, so it has no -q spelling of its own"
		if !c.archived {
			carriersBy += fmt.Sprintf(" — `furrow lint` names its carriers (%s-missing)", field)
		}
	case here == 0:
		side, read := "the archive's", "--archived "
		if c.archived {
			side, read = "the live board's", ""
		}
		carriersBy = fmt.Sprintf("only %s tasks carry it — `furrow ls %s-q \"%s:'%s'\"` lists them", side, read, field, lit)
	}
	if v.Quoted {
		// Exact only, in the order a reader can always spell: the pointer
		// itself (a box is still nameable by its id), else the whole title —
		// and a pointer that IS a whole title is the two targets again.
		switch {
		case carried && len(byTitle) > 0:
			return "", &core.Error{
				Code: core.CodeValidation, Kind: core.KindEpicAmbiguous,
				Msg:        fmt.Sprintf("epic %q is ambiguous: %s, and it is also the whole title of box %s — name the box by id; %s", lit, pointer, strings.Join(byTitle, ", "), carriersBy),
				Candidates: append([]string{lit}, byTitle...),
			}
		case carried:
			return lit, nil
		}
		switch len(byTitle) {
		case 1:
			return byTitle[0], nil
		case 0:
			return "", c.hintOtherCarriers(field, lit, epicNotFound(lit, epics))
		default:
			return "", &core.Error{Code: core.CodeValidation, Kind: core.KindEpicAmbiguous, Msg: fmt.Sprintf("epic %q is ambiguous (%s)", lit, strings.Join(byTitle, ", ")), Candidates: byTitle}
		}
	}
	var hit string
	var rerr *core.Error
	id, err := c.app.resolveEpicIn(lit, epics)
	if err == nil {
		hit = id
	} else if !errors.As(err, &rerr) {
		return "", &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: err.Error()}
	}
	switch {
	case hit != "" && !carried:
		return hit, nil
	case hit != "":
		return "", &core.Error{
			Code: core.CodeValidation, Kind: core.KindEpicAmbiguous,
			Msg:        fmt.Sprintf("epic %q is ambiguous: %s, and it also names box %s — name the box by id; %s", lit, pointer, hit, carriersBy),
			Candidates: []string{lit, hit},
		}
	case carried && rerr.Kind == core.KindEpicAmbiguous:
		rerr.Candidates = append([]string{lit}, rerr.Candidates...)
		rerr.Msg = fmt.Sprintf("epic %q is ambiguous: %s, and it also matches boxes %s — name a box by id; %s", lit, pointer, strings.Join(rerr.Candidates[1:], ", "), carriersBy)
		return "", rerr
	case carried:
		return lit, nil
	}
	return "", c.hintOtherCarriers(field, lit, rerr)
}

// hintOtherCarriers appends, to a not-found, where else the pointer lives —
// the other store's tasks carry it — with the read that lists them; a union
// read has already looked there.
func (c *queryCompiler) hintOtherCarriers(field, lit string, rerr *core.Error) *core.Error {
	if rerr.Kind == core.KindEpicNotFound && c.wider == nil {
		if o := c.other(); o != nil && carries(o, field, lit) {
			side, read := "the archive's", "--archived "
			if c.archived {
				side, read = "the live board's", ""
			}
			rerr.Msg += fmt.Sprintf(" — %s tasks point at it: `furrow ls %s-q '%s:%s'` lists them", side, read, field, lit)
		}
	}
	return rerr
}

// resolveEpicRefs binds an OR-set of epic refs (resolveEpicRef each), stamping
// a fault with the term's position like every other binder fault; a miss
// carries the ref in details.missing. The exact-id-only, silent-on-a-miss
// form this replaces answered `anchor:会場` (a unique title substring the
// sibling flags resolve) with 0 rows at exit 0, which a drill session read as
// "nothing is anchored" and started re-pointing 80 dues by hand (t-5mcm). A
// dangling pointer on a SHARD stays lint's to report (epic-missing,
// anchor-missing); the ref a QUERY names must resolve.
func (c *queryCompiler) resolveEpicRefs(term query.Term) ([]string, error) {
	epics, err := c.epicList()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(term.Values))
	for _, v := range term.Values {
		id, ce := c.resolveEpicRef(term.Field, v, epics)
		if ce == nil {
			ids = append(ids, id)
			continue
		}
		extra := map[string]any{"ref": v.Text}
		if ce.Kind == core.KindEpicNotFound {
			extra["missing"] = []string{v.Text}
			if term.Field == "anchor" {
				// The likeliest slip, the one `set --anchor` catches too: the
				// value is the box's DAY, but the pointer names the box. The
				// kind and the candidates stay — a consumer branching on
				// epic-not-found still lands here.
				if _, perr := core.ParseAnchor(v.Text); perr == nil {
					ce.Msg += fmt.Sprintf(" — anchor: names the BOX whose day the due follows, not the day itself: `due:%s` selects by date, and the day lives on the box (`furrow epic set <epic> --anchor %s`)", v.Text, v.Text)
				}
			}
		}
		return nil, termErr(ce, term, extra)
	}
	return ids, nil
}

// taskRef is one bound task ref: the id, and whether it is a task of THIS
// snapshot — the only case in which its own deps are readable here.
type taskRef struct {
	id   string
	here bool
}

func refIDs(refs []taskRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.id
	}
	return out
}

// resolveTaskRefs binds an OR-set of task refs. A ref names a task of this
// snapshot, an id this snapshot's tasks carry as a dep (depTargets: an edge
// is a fact of the snapshot even when its far end retired), or, in a union
// read, a task or dep of the other snapshot (wider). Anything else is exit 2
// validation with the misses in details.missing — the contract `furrow dep`
// holds its <dep> arguments to (a dependency that "does not exist" is exit 2,
// nothing written) and the one `show`/`dep --list` hold an archived id to
// under a live read — and the message says where else the id lives, with
// the read that sees it: the other store holds it (details.archived or
// details.live) or only the other store's tasks carry it
// (details.carried_elsewhere). Task ids resolve by prefix or title nowhere
// in furrow (a random suffix makes a typo a miss, not a near-miss), so a miss
// carries no candidates. Until t-5mcm an unknown id matched nothing at exit 0
// — `depends-on:t-typo` answering "nothing waits on it".
func (c *queryCompiler) resolveTaskRefs(term query.Term) ([]taskRef, error) {
	refs := make([]taskRef, 0, len(term.Values))
	var missing []string
	for _, v := range term.Values {
		id := v.Text
		switch {
		case c.idx.Has(id):
			refs = append(refs, taskRef{id: id, here: true})
		case c.depTargets()[id], c.wider != nil && (c.wider.Has(id) || carries(c.wider, "deps", id)):
			refs = append(refs, taskRef{id: id})
		default:
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return refs, nil
	}
	extra := map[string]any{"missing": missing}
	msg := fmt.Sprintf("%s: %s not in this snapshot — neither a task here nor a dep a task here carries", term.Field, strings.Join(missing, ", "))
	hint := ""
	if c.wider == nil {
		hint = c.whereElse(term, missing, extra)
	}
	// The ids the other store neither holds nor carries are known nowhere —
	// said for them alone in a mixed list, and for the whole list when the
	// hint found nothing.
	var nowhere []string
	for _, id := range missing {
		if !contains(anyStrings(extra["archived"]), id) && !contains(anyStrings(extra["live"]), id) && !contains(anyStrings(extra["carried_elsewhere"]), id) {
			nowhere = append(nowhere, id)
		}
	}
	if len(nowhere) > 0 {
		hint += fmt.Sprintf("; no store knows %s — a task ref is an existing id, spelled exactly", strings.Join(nowhere, ", "))
	}
	e := &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: msg + hint}
	return nil, termErr(e, term, extra)
}

// anyStrings reads a details value back as the []string it was filed as; nil
// for an absent key.
func anyStrings(v any) []string {
	ss, _ := v.([]string)
	return ss
}

// whereElse words a miss's hint from the other store — the ids it holds, and
// the ids only its tasks carry as a dep — and files them in extra
// (details.archived / details.live, details.carried_elsewhere), naming the
// read that sees them. Nothing is loaded unless there was a miss.
func (c *queryCompiler) whereElse(term query.Term, ids []string, extra map[string]any) string {
	o := c.other()
	if o == nil {
		return ""
	}
	var held, carried []string
	for _, id := range ids {
		switch {
		case o.Has(id):
			held = append(held, id)
		case carries(o, "deps", id):
			carried = append(carried, id)
		}
	}
	side, flag, key := "the archive", "--archived ", "archived"
	if c.archived {
		side, flag, key = "the live board", "", "live"
	}
	msg := ""
	if len(held) > 0 {
		extra[key] = held
		msg += fmt.Sprintf("; %s holds %s — `furrow ls %s-q '%s:%s'` reads there, `furrow show %s%s` names the edges", side, strings.Join(held, ", "), flag, term.Field, strings.Join(held, ","), strings.Join(held, " "), strings.TrimSuffix(" "+flag, " "))
	}
	if len(carried) > 0 {
		extra["carried_elsewhere"] = carried
		msg += fmt.Sprintf("; only %s's tasks carry %s as a dep — `furrow ls %s-q 'depends-on:%s'` names them", side, strings.Join(carried, ", "), flag, strings.Join(carried, ","))
	}
	return msg
}

// other returns the snapshot this read is NOT scanning: the union read's other
// snapshot when the caller passed one (wider), else the archive under a live
// read or the live board under an archived one, loaded once per compile and
// only on a miss (a disk read); nil when there is none — an in-memory store
// has no archive. A snapshot read resolves nothing there: it consults it to
// SAY where a missed ref lives (whereElse), the way `show`'s not-found hints
// at the archive. A union read (wider) resolves against both, findTask and
// reach reading the other snapshot's edges too, because the union promised both.
func (c *queryCompiler) other() *core.Index {
	if c.wider != nil {
		return c.wider
	}
	if !c.otherLoaded {
		c.otherLoaded = true
		if c.archived {
			if hot, err := c.app.load(); err == nil {
				c.otherIdx = hot
			}
		} else {
			c.otherIdx = c.app.archiveIndex()
		}
	}
	return c.otherIdx
}

// findTask is Index.Find over the snapshot and, in a union read, the other
// snapshot too — the named task's own deps are readable wherever the union
// holds it, so `stats --since -q blocks:<live>` reaches the archived dep the
// live pass cannot match and the archive pass, reading the live task's deps
// from the other snapshot, does.
func (c *queryCompiler) findTask(id string) *core.Task {
	if t, _ := c.idx.Find(id); t != nil {
		return t
	}
	if c.wider != nil {
		if t, _ := c.wider.Find(id); t != nil {
			return t
		}
	}
	return nil
}

// edgeRefs is resolveTaskRefs for the two qualifiers that read the NAMED
// task's own deps (blocks:, ancestor-of:): every ref must be a task of this
// snapshot — or, in a union read, of the other snapshot, whose task is then
// read through findTask — because a snapshot read never derives facts from the
// other store (GetBatchArchived's reasoning — two reads disagreeing about one
// task is the defect that rule closes). A ref this snapshot only carries as a
// dep is exit 2 (details.outside) saying where its edges are readable, or
// that no store holds it — on the live board, lint's dep-missing.
func (c *queryCompiler) edgeRefs(term query.Term) ([]string, error) {
	refs, err := c.resolveTaskRefs(term)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(refs))
	var outside []string
	for _, r := range refs {
		switch {
		case r.here, c.wider != nil && c.wider.Has(r.id):
			ids = append(ids, r.id)
		default:
			outside = append(outside, r.id)
		}
	}
	if len(outside) == 0 {
		return ids, nil
	}
	extra := map[string]any{"outside": outside}
	msg := fmt.Sprintf("%s: %s — not a task of this snapshot, so its own deps cannot be read here", term.Field, strings.Join(outside, ", "))
	var held, dangling []string
	o := c.other()
	for _, id := range outside {
		if o != nil && c.wider == nil && o.Has(id) {
			held = append(held, id)
		} else {
			dangling = append(dangling, id)
		}
	}
	if len(held) > 0 {
		// The hint names a read that HAS the flag: stats, next, revisit and
		// the write selectors take -q but not --archived.
		side, flag, key := "the archive", "--archived ", "archived"
		if c.archived {
			side, flag, key = "the live board", "", "live"
		}
		extra[key] = held
		msg += fmt.Sprintf("; %s holds %s — `furrow ls %s-q '%s:%s'` reads its edges, `furrow show %s%s` names them", side, strings.Join(held, ", "), flag, term.Field, strings.Join(held, ","), strings.Join(held, " "), strings.TrimSuffix(" "+flag, " "))
	}
	if len(dangling) > 0 {
		lint := ""
		if !c.archived {
			lint = " (lint: dep-missing)"
		}
		msg += fmt.Sprintf("; no store holds %s — carried here as a dep only%s", strings.Join(dangling, ", "), lint)
	}
	e := &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: msg}
	return nil, termErr(e, term, extra)
}

// stampTerm is termErr for a resolver that returns error: a *core.Error gets
// the term's position (and extra keys), anything else passes through.
func stampTerm(err error, term query.Term, extra map[string]any) error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return termErr(ce, term, extra)
	}
	return err
}

// compileQuery parses raw -q text and binds it to a predicate over the loaded
// index. Validation faults (bad grammar, unknown field/flag, an operator on a
// non-ordered field, an unknown lane/type value, a malformed date, a ref that
// resolves to no epic/repo/task) are exit-2 errors carrying a stable kebab id
// and, where the input almost resolved, candidates. A nil predicate is
// returned only with a non-nil error; an empty query compiles to a
// match-everything predicate. The second return is the compiler's cached body
// reader (see queryPredShared).
func (a *App) compileQuery(raw string, idx *core.Index, r queryRead) (taskPred, func(*core.Task) (string, error), error) {
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

	loadBody := r.loadBody
	if loadBody == nil {
		loadBody = a.Store.LoadBody
	}
	c := &queryCompiler{app: a, idx: idx, now: a.Clock.Now(), staleDays: r.staleDays, loadBody: loadBody, archived: r.archived, wider: r.wider}
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
			case "repeat":
				// The live occurrence of a series is the only task that carries
				// the rule, so has:repeat is "the recurring work on this board".
				return t.Repeat != ""
			case "anchor":
				// The dues that follow a box's day — the reschedule's moving
				// side; `has:due no:anchor` is the other side, the dates the
				// other side or the calendar fixed.
				return t.Anchor != ""
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
				return nil, stampTerm(a.unknownLaneErr("", v.Text), term, nil)
			}
		}
		vals := valTexts(term.Values)
		return neg(func(t *core.Task) bool { return contains(vals, t.Status) }), nil

	case "epic":
		// A box's members. The ref resolves as -e does (resolveEpicRefs);
		// membership is then an exact-id test.
		vals, err := c.resolveEpicRefs(term)
		if err != nil {
			return nil, err
		}
		return neg(func(t *core.Task) bool { return contains(vals, t.Epic) }), nil

	case "anchor":
		// The tasks whose due follows a box's day. The ref resolves as -e does
		// (resolveEpicRefs) — NOT as `set --anchor` does: that write also
		// demands the box carry a day, because a due cannot follow a day never
		// declared; a read must still list the followers of a box whose day
		// was cleared, which is exactly the `set -q 'anchor:<epic>'
		// --clear-anchor` the clear's own stderr note prescribes.
		vals, err := c.resolveEpicRefs(term)
		if err != nil {
			return nil, err
		}
		return neg(func(t *core.Task) bool { return contains(vals, t.Anchor) }), nil

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
				return nil, stampTerm(err, term, nil)
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
		// The ref must resolve (resolveTaskRefs); as the edge's TARGET it need
		// not be a task of this snapshot.
		refs, err := c.resolveTaskRefs(term)
		if err != nil {
			return nil, err
		}
		vals := refIDs(refs)
		return neg(func(t *core.Task) bool { return anyContains(t.Deps, vals) }), nil

	case "blocks":
		// t blocks any named id — the same edge read from the other side:
		// t ∈ X.Deps. Each named X is resolved once at compile and must be a
		// task of this snapshot, since its own deps are read (edgeRefs).
		ids, err := c.edgeRefs(term)
		if err != nil {
			return nil, err
		}
		blocked := map[string]bool{}
		for _, id := range ids {
			if x := c.findTask(id); x != nil {
				for _, d := range x.Deps {
					blocked[d] = true
				}
			}
		}
		return neg(func(t *core.Task) bool { return blocked[t.ID] }), nil

	case "descendant-of":
		// depends-on's transitive twin: t (transitively) waits on any named id —
		// X's descendants are everything DOWNSTREAM of it in the deps DAG. The
		// closure is computed once at compile (O(edges)); the root must resolve
		// and, being only pointed AT, need not be a task of this snapshot
		// (resolveTaskRefs); the named task itself is not its own descendant,
		// mirroring how depends-on:X never matches X.
		refs, err := c.resolveTaskRefs(term)
		if err != nil {
			return nil, err
		}
		set := c.reach(refIDs(refs), false)
		return neg(func(t *core.Task) bool { return set[t.ID] }), nil

	case "ancestor-of":
		// blocks' transitive twin: t is anything any named id (transitively)
		// waits on — X's ancestors are UPSTREAM, the work that must land first.
		// The root's own deps are the first hop, so it must be a task of this
		// snapshot (edgeRefs).
		ids, err := c.edgeRefs(term)
		if err != nil {
			return nil, err
		}
		set := c.reach(ids, true)
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
	// The edges are the snapshot's and, in a union read, the other snapshot's
	// too: a chain that crosses the archive (live → retired → live) is one
	// DAG to the union, and each pass then matches its own tasks.
	adj := map[string][]string{}
	edges := func(idx *core.Index) {
		for i := range idx.Tasks {
			t := &idx.Tasks[i]
			for _, d := range t.Deps {
				if up {
					adj[t.ID] = append(adj[t.ID], d)
				} else {
					adj[d] = append(adj[d], t.ID)
				}
			}
		}
	}
	edges(c.idx)
	if c.wider != nil {
		edges(c.wider)
	}
	// One closure PER start, unioned: a comma is OR, so `descendant-of:x,y`
	// is x's descendants plus y's. The single shared walk that deleted every
	// start at the end returned nothing for the commonest shape — y waiting on
	// x — because y, x's only descendant, was also a start (t-hwav). A start
	// is excluded from its OWN closure only (the named task is not its own
	// descendant, as depends-on:X never matches X); reached from another
	// start, it stays.
	// A start is walked by id, not looked up: descendant-of's root may be
	// only an edge target of this snapshot (a retired dep), and its
	// descendants are still exactly the tasks whose edges name it.
	result := map[string]bool{}
	for _, s := range starts {
		seen := map[string]bool{}
		frontier := []string{s}
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
		delete(seen, s)
		for id := range seen {
			result[id] = true
		}
	}
	return result
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
