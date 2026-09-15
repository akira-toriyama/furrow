// Reads: Get/GetBatch/ShowBatch, the list evaluator (List/ListItems/Next) and
// the backlink scan. Read-only — nothing here calls Store.Save.

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Get returns a task and its body. NotFound when the id is unknown.
func (a *App) Get(id string) (*core.Task, string, error) {
	idx, err := a.load()
	if err != nil {
		return nil, "", err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, "", a.notFoundTask(id)
	}
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return nil, "", err
	}
	return t, body, nil
}

// ShowItem is one result of GetBatch: a task plus its body (empty when the
// batch was read without bodies).
type ShowItem struct {
	Task core.Task
	Body string
}

// GetBatch resolves a set of ids in one index load: the found tasks come back
// in input order (duplicates collapse to their first occurrence, misses too),
// and the ids that named no task come back in missing. A miss is data, not an
// error — partial success stays representable, the caller decides what a
// non-empty missing means. err is reserved for load/IO failures. withBody
// loads each found task's body; without it the body files are never touched.
func (a *App) GetBatch(ids []string, withBody bool) ([]ShowItem, []string, error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	return getBatchFrom(idx, a.Store.LoadBody, ids, withBody)
}

// getBatchFrom resolves ids against idx in input order (duplicates collapse to
// their first occurrence, misses collected), loading each found task's body via
// loadBody when withBody. It is the shared core of the hot GetBatch and the
// archive GetBatchArchived, so both reads behave identically.
func getBatchFrom(idx *core.Index, loadBody func(string) (string, error), ids []string, withBody bool) ([]ShowItem, []string, error) {
	items, missing := []ShowItem{}, []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, i := idx.Find(id)
		if i < 0 {
			missing = append(missing, id)
			continue
		}
		body := ""
		if withBody {
			b, err := loadBody(id)
			if err != nil {
				return nil, nil, err
			}
			body = b
		}
		items = append(items, ShowItem{Task: *t, Body: body})
	}
	return items, missing, nil
}

// ShowEntry is one result of ShowBatch: exactly one of Task / Epic is set. A
// sum type rather than a widened ShowItem, because a box is not a task wearing
// a type (core.Epic) and the two views a reader wants are genuinely different —
// a task's lane/priority dashboard vs a box's goal + member roll-up.
type ShowEntry struct {
	Task *ShowItem
	Epic *EpicDetail
}

// ShowBatch resolves refs across BOTH entities in input order — the read behind
// `furrow show <id>...` taking an epic id. MEMBERSHIP routes each ref, the same
// rule RefTargetsEpic states: a ref naming a real task is that task, else a ref
// the epic store resolves is that box, else it is missing. Duplicates collapse
// to their first occurrence, as in GetBatch.
//
// A miss stays a MISS here rather than becoming the box resolver's exit 2:
// `show` is a batch read whose partial failure is data (details.missing), and a
// resolution error would throw away the entries that were found. withBody=false
// drops both entities' prose, so --no-body means one thing.
//
// Two things it must NOT do, both learned the hard way:
//   - Read the epic store per ref and treat ANY failure as "not a box".
//     core.UnmarshalEpic reports a corrupt shard as a VALIDATION error, exactly
//     like an unresolvable ref, so that shape launders store corruption into
//     "this id names nothing" — `show` would report a box that is on disk as
//     missing while `epic show`, `note`, `edit` and `lint` all report the
//     unreadable shard. The store is therefore read ONCE, up front, and a read
//     failure is returned: "not found" and "unreadable" are different answers.
//   - Dedupe by the REF. A box takes several refs (id, unique prefix, unique
//     title substring), so `show <id> <prefix>` would render it twice and
//     inflate the "N of M ids not found" arithmetic. Dedup is by the RESOLVED
//     id; the ref-level set only keeps a repeated MISS from being listed twice.
func (a *App) ShowBatch(refs []string, withBody bool) ([]ShowEntry, []string, error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	var (
		epics       []core.Epic
		epicsLoaded bool
	)
	out, missing := []ShowEntry{}, []string{}
	seenRef, seenID := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		if seenRef[ref] {
			continue
		}
		seenRef[ref] = true
		if t, i := idx.Find(ref); i >= 0 {
			if seenID[t.ID] {
				continue
			}
			seenID[t.ID] = true
			body := ""
			if withBody {
				if body, err = a.Store.LoadBody(t.ID); err != nil {
					return nil, nil, err
				}
			}
			out = append(out, ShowEntry{Task: &ShowItem{Task: *t, Body: body}})
			continue
		}
		if !epicsLoaded {
			if epics, err = a.Store.LoadEpics(); err != nil {
				return nil, nil, err
			}
			epicsLoaded = true
		}
		id, rerr := a.resolveEpicIn(ref, epics)
		if rerr != nil {
			// The store READ succeeded above, so this can only be "no such box"
			// (or an ambiguous ref, which for a batch read is equally a miss).
			missing = append(missing, ref)
			continue
		}
		if seenID[id] {
			continue
		}
		seenID[id] = true
		d, err := a.EpicShow(id)
		if err != nil {
			return nil, nil, err
		}
		if !withBody {
			d.Body = ""
		}
		out = append(out, ShowEntry{Epic: d})
	}
	return out, missing, nil
}

// BacklinksBatch is Backlinks for a set of ids in ONE board pass: a single
// load plus a single body scan, regardless of how many ids are requested — so
// `show <many ids> --backlinks` stays O(board), not O(ids × board). Each entry
// equals what Backlinks would return for that id: mentioners in canonical
// order, self-mentions excluded, each mentioner counted once. Every requested
// id present in the index maps to a (possibly empty, never nil) slice; unknown
// ids are simply absent from the map (the caller has already filtered to found
// tasks, so this never needs to raise NotFound).
func (a *App) BacklinksBatch(ids []string) (map[string][]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	out := map[string][]core.Task{}
	want := map[string]bool{}
	for _, id := range ids {
		if idx.Has(id) {
			want[id] = true
			out[id] = []core.Task{}
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	re := core.LinkPattern(a.Cfg.IDPrefix)
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	// mentions[mentionerID] = the requested targets that body links to (deduped
	// so a body linking the same target twice still counts its author once).
	mentions := map[string][]string{}
	for _, bid := range bodyIDs {
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, target := range core.ExtractLinks(body, re) {
			if target == bid || !want[target] || seen[target] {
				continue // self-mention isn't a backlink; ignore unrequested/dup
			}
			seen[target] = true
			mentions[bid] = append(mentions[bid], target)
		}
	}
	// Walk tasks in canonical order so each target's mentioners come out ordered.
	for i := range idx.Tasks {
		for _, target := range mentions[idx.Tasks[i].ID] {
			out[target] = append(out[target], idx.Tasks[i])
		}
	}
	return out, nil
}

// QueryOpts filters List/Next/Revisit. Zero values mean "no filter". Label (an
// explicit tag filter) and ScopeRepo (the board scope) are separate on
// purpose: they AND together, so filtering by a tag never widens a scoped
// board. Repo (an explicit -r) and ScopeRepo both filter on the repos field;
// Drafts selects only repo-less tasks and bypasses the scope (a draft belongs
// to no repo, so no repo scope may hide it — see match).
type QueryOpts struct {
	Status    string
	Label     string // explicit tag filter; ANDs with ScopeRepo
	ScopeRepo string // board-scope repo (a pointer's / central board's DefaultRepo)
	Repo      string // owner/repo filter on the repos field (already resolved)
	Drafts    bool   // only tasks with repos == []; ignores ScopeRepo/Repo
	// Since/Until filter on the Updated timestamp (inclusive bounds); nil = no
	// bound. They live on QueryOpts rather than on one command's flags so any
	// read funnelling through match can gain a date window without a second
	// predicate.
	Since *time.Time
	Until *time.Time
	// Sort re-orders List's result by one of core.SortFields (empty = canonical
	// lane->priority->id order); Reverse flips the default "most first" direction.
	Sort    string
	Reverse bool
	// Archived reads from the sibling .furrow/archive/ store instead of the hot
	// index (the `ls --archived` browse of retired tasks). The same filters/sort
	// apply; only the source index changes.
	Archived bool
	// Epic is an explicit -e filter, ALREADY resolved to an epic id; "" = none.
	// It is STRICT: unlike Next's implicit active-epic scope it has no carve-out
	// for unfiled tasks, because naming a box means you want that box.
	Epic string
	// AllEpics is the `--all-epics` escape hatch: ignore Next's active-epic scope
	// and read the whole board. Stepping outside the box is allowed — it just has
	// to leave a trace in the shell history, which is what makes the scope a
	// mechanism rather than a convention. Only Next reads it (the scope itself is
	// computed inside Next — see NextScope).
	AllEpics bool
	// Actionable / Blocked are the `ls` derived-state filters, orthogonal to (and
	// ANDing with) the lane/label/repo scope. Actionable keeps only tasks `furrow
	// next` would hand you (a next lane, every dep done, not a container); Blocked
	// keeps only tasks with an unsatisfied dep (a non-empty blocked_by). They are
	// disjoint by construction, so the CLI marks them mutually exclusive. List
	// (hence Tree) honours them; Next/Revisit ignore them.
	Actionable bool
	Blocked    bool
	// Lanes is a one-shot override of the configured [next].lanes for `furrow next`
	// (the --lanes flag): when non-empty, next tests lane membership against THESE
	// lanes instead of the config's, leaving config untouched (non-destructive).
	// nil/empty = use the configured next-lanes. Only Next reads it.
	Lanes []string
	// Query is the raw `-q` typed-query string (empty = no query filter). It is
	// parsed (internal/query) and compiled ONCE against the loaded index
	// (queryPred, the shared match layer) into a per-task predicate that ANDs
	// with every other filter here, so a query never widens a scoped board.
	// Every filtering read honors it — List (hence Tree), Next, Revisit, Stats,
	// Search; Brief deliberately does not (a fixed session-orient read).
	Query string
	Limit int
}

// match reports whether t passes the query's filters (Limit excluded — that is
// the iteration's job). In Drafts mode only repo-less tasks pass and the board
// scope is bypassed; Status and Label still apply. Note a repo-scoped read
// (ScopeRepo or Repo set) hides drafts — the CLI's hidden-drafts hint exists
// for exactly that.
func (o QueryOpts) match(t *core.Task) bool {
	if !matchAnyLane(o.Status, t.Status) {
		return false
	}
	if !matchAnyLabel(o.Label, t.Labels) {
		return false
	}
	// The date window is a field filter like Status/Label, so it applies even in
	// Drafts mode (before the draft short-circuit below).
	if o.Since != nil && t.Updated.Before(*o.Since) {
		return false
	}
	if o.Until != nil && t.Updated.After(*o.Until) {
		return false
	}
	// The explicit -e filter is strict membership (a task's epic is 0..1, so this
	// is equality, not set containment). It applies before the draft short-circuit:
	// a draft can be filed in a box, and `-e travel --drafts` means both.
	if o.Epic != "" && t.Epic != o.Epic {
		return false
	}
	if o.Drafts {
		return len(t.Repos) == 0
	}
	if o.ScopeRepo != "" && !contains(t.Repos, o.ScopeRepo) {
		return false
	}
	if o.Repo != "" && !contains(t.Repos, o.Repo) {
		return false
	}
	return true
}

// matchRevisit is match with the draft carve-out `revisit` needs: an open
// draft (repos == []) passes the board scope and any repo filter, so drafts
// surface (with the no_repo signal) regardless of scope. The explicit filters
// (Status/Label) still apply — asking for one tag must not return unrelated
// drafts.
func (o QueryOpts) matchRevisit(t *core.Task) bool {
	if len(t.Repos) > 0 {
		return o.match(t)
	}
	d := o
	d.ScopeRepo, d.Repo = "", ""
	return d.match(t)
}

// List returns tasks after applying the filters, in canonical
// lane->priority->id order unless o.Sort re-orders them. A -s filter naming an
// unknown lane, or an unknown --sort field, fails fast (rather than silently
// returning [] / ignoring the flag) — `ls` is the only read carrying these, so
// this is its guard. With a sort, Limit applies AFTER ordering (the top N of the
// sorted set), so it collects all matches first; without a sort the result is
// identical to the old canonical-order-first-N.
func (a *App) List(o QueryOpts) ([]core.Task, error) {
	tasks, _, err := a.listMatched(o)
	return tasks, err
}

// listMatched is List's engine, returning the matched+sorted+limited tasks AND
// the loaded index — so ListItems can enrich the exact same result set without a
// second load. The --actionable/--blocked derived-state filters apply here, BEFORE
// the limit, so `-n` caps the filtered set (not the pre-filter one); computing
// doneIDs is skipped unless one of those filters is set.
func (a *App) listMatched(o QueryOpts) ([]core.Task, *core.Index, error) {
	if err := a.validateLaneFilter(o.Status); err != nil {
		return nil, nil, err
	}
	if err := validateSortField(o.Sort); err != nil {
		return nil, nil, err
	}
	idx, err := a.listIndex(o)
	if err != nil {
		return nil, nil, err
	}
	var doneIDs map[string]bool
	if o.Actionable || o.Blocked {
		doneIDs = a.doneSet(idx)
	}
	// Compile -q once (against the loaded index) into a per-task predicate; a
	// parse/validation fault fails the whole read with exit 2 before any output.
	// An archived read filters the archive index, so body-matching terms must
	// resolve bodies in the archive store too — the loader rides along.
	var loadBody func(string) (string, error)
	if o.Archived && o.Query != "" {
		arc, err := a.archiveStore()
		if err != nil {
			return nil, nil, err
		}
		loadBody = arc.LoadBody
	}
	qpred, err := a.queryPred(o.Query, idx, a.Cfg.RevisitStaleDays, loadBody)
	if err != nil {
		return nil, nil, err
	}
	var out []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		if o.Actionable && !a.actionable(idx, t, doneIDs) {
			continue
		}
		if o.Blocked && len(blockedDeps(t, doneIDs)) == 0 {
			continue
		}
		if qpred != nil {
			ok, err := qpred(t)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, *t)
	}
	if o.Sort != "" {
		core.SortTasks(out, o.Sort, o.Reverse)
	}
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, idx, nil
}

// ListItem is a task plus the derived facts `ls` exposes on every row — the same
// two the tree carries — so the flat list answers "what can I pick up / what's in
// the way" without a separate `--tree`.
type ListItem struct {
	Task       core.Task
	Actionable bool
	BlockedBy  []string
}

// ListItems is List enriched with per-row derived facts (actionable /
// blocked_by), for the flat `ls` human table (the glyph) and its --json. It
// reuses listMatched's single load and computes each fact through the SAME
// helper the tree uses (factsFor), so flat and tree never disagree.
//
// The v5 `container`/`stuck` columns are gone with the type: a box is no longer a
// row in this list at all. Epic-level state lives on the epic, where `epic ls`
// and `ls --tree`'s groups report it.
func (a *App) ListItems(o QueryOpts) ([]ListItem, error) {
	tasks, idx, err := a.listMatched(o)
	if err != nil {
		return nil, err
	}
	doneIDs := a.doneSet(idx)
	items := make([]ListItem, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		actionable, blockedBy := a.factsFor(idx, t, doneIDs)
		items = append(items, ListItem{Task: *t, Actionable: actionable, BlockedBy: blockedBy})
	}
	return items, nil
}

// doneSet returns the ids in the done lane — the shared input to every readiness
// predicate (actionable, blocked_by, stuck).
func (a *App) doneSet(idx *core.Index) map[string]bool {
	done := make(map[string]bool)
	for i := range idx.Tasks {
		if idx.Tasks[i].Status == a.Cfg.DoneLane {
			done[idx.Tasks[i].ID] = true
		}
	}
	return done
}

// validateSortField rejects an unknown --sort key with the valid fields in
// Candidates (symmetric with the unknown-lane guard) — a typo must not silently
// fall back to canonical order. Empty = no sort, always valid.
func validateSortField(field string) error {
	if field == "" || core.IsSortField(field) {
		return nil
	}
	return &core.Error{
		Code:       core.CodeValidation,
		Kind:       core.KindValidation,
		Msg:        fmt.Sprintf("unknown sort field %q (valid: %s)", field, strings.Join(core.SortFields, ", ")),
		Candidates: append([]string(nil), core.SortFields...),
	}
}

// Next returns the actionable tasks in canonical order — the work that is ready
// to pick up: status in the configured next-lanes ([next].lanes, default
// ready+in-progress) AND every dependency already done. The query's filters
// restrict the result with the same semantics as List.
//
// On a board that has declared at least one epic, the result is ALSO scoped to
// the active epic(s) for the read's repo scope, plus the unfiled pile (see
// NextScope) — the mechanism that keeps a session on the declared focus. A
// PINNED box's tasks pass through that scope entirely and lead the result (v7):
// a pinned channel (a mandate box) must be seen without stealing `active` from
// the box being worked. Then the active epics' tasks (they ARE the focus), then
// the unfiled rescue — canonical order within each band. An engaged scope with
// nothing active matches nothing BUT the pinned band: "no box open" must not
// silently degrade into "show me the unfiled pile", while an always-visible
// channel stays visible by definition. An explicit -e (o.Epic) or --all-epics
// bypasses the scope, pinned band included.
func (a *App) Next(o QueryOpts) ([]core.Task, error) {
	scope, err := a.NextScope(o)
	if err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	doneIDs := a.doneSet(idx)
	// inNextLane is normally the configured [next].lanes membership; --lanes
	// (o.Lanes) overrides it for this call ONLY — a non-destructive, in-memory
	// swap that never rewrites config. The deps-done half of the predicate
	// (idx.Actionable) is unchanged and shared, so `--lanes` widens WHICH lanes
	// count as "now", not what "ready" means.
	inNextLane := a.Cfg.IsNextLane
	if len(o.Lanes) > 0 {
		// A --lanes token is an explicit CLI arg, so an unknown one fails fast with
		// the configured lanes in candidates (symmetric with `ls -s`) rather than
		// silently matching nothing.
		override := make(map[string]bool, len(o.Lanes))
		for _, l := range o.Lanes {
			if !a.Cfg.IsLane(l) {
				return nil, a.unknownLaneErr("", l)
			}
			override[l] = true
		}
		inNextLane = func(lane string) bool { return override[lane] }
	}
	// Compile -q once; it ANDs with readiness like every other filter. It is
	// evaluated LAST (after the cheap ready test) so a body-reading query never
	// loads a body for a task that was not ready anyway.
	qpred, err := a.queryPred(o.Query, idx, a.Cfg.RevisitStaleDays, nil)
	if err != nil {
		return nil, err
	}
	// With the scope engaged, pinned (pass-through), focus (active-epic members)
	// and rescue (unfiled) collect separately so the bands stack in that order
	// regardless of canonical interleaving; the limit therefore applies AFTER
	// the partition, so `-n1` hands you the pinned channel first, then the
	// focus, never whatever unfiled task sorts first.
	var pinned, out, rescue []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		if scope.Engaged && !scope.Pinned[t.Epic] {
			if len(scope.Active) == 0 {
				continue // nothing is active: deliberately empty (pinned only), never the unfiled pile
			}
			if t.Epic != "" && !scope.Active[t.Epic] {
				continue
			}
		}
		// "Ready" = in a next lane AND every dep done — the same rule
		// App.actionable encodes, expressed here against the (possibly
		// overridden) lane set so `ls --tree`'s ★ and a plain `next` still agree.
		ready := inNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs)
		if !ready {
			continue
		}
		if qpred != nil {
			ok, err := qpred(t)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		switch {
		case scope.Engaged && scope.Pinned[t.Epic]:
			// A box both pinned and active lands here: the pass-through band is
			// the stronger claim on attention.
			pinned = append(pinned, *t)
			continue
		case scope.Engaged && t.Epic == "":
			rescue = append(rescue, *t)
			continue
		}
		out = append(out, *t)
		if !scope.Engaged && o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	out = append(pinned, append(out, rescue...)...)
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, nil
}
