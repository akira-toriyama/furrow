package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The epic entity's app layer: the single mutation funnel for boxes, shaped like
// repo.go/review.go. Every write goes through mutateEpic so the `updated` stamp
// and the store call live in one place.

// EpicItem is one `epic ls` row: the box plus its derived member roll-up.
type EpicItem struct {
	Epic     core.Epic
	Progress Progress
	Stuck    bool
	// OpenDeps are the epic's deps still open (openEpicDeps) — the unsatisfied
	// "open after" edges, resolved here so a row can say what it waits on
	// without a second read. Empty for a box whose deps are all closed.
	OpenDeps []string
}

// EpicDetail is `epic show`: the box, its roll-up, its members in canonical
// order (lane -> priority -> id, the same order `ls` prints), and its body —
// the prose record (goal context, the activation log) a task's `show` would
// print, folded in for the same reason: the follow-up read is the read.
type EpicDetail struct {
	Epic     core.Epic
	Progress Progress
	Stuck    bool
	// Deps are the epic's dep edges resolved to id+title+state (EpicRef), so
	// `epic show` can print what this box waits on without a second read; the
	// raw id set rides in Epic.Deps.
	Deps  []EpicRef
	Tasks []ListItem
	Body  string
}

// EpicAddOpts are the creation options for `furrow epic add`.
type EpicAddOpts struct {
	Goal   string
	Meta   map[string]string
	Labels []string
	Repos  []string
	Body   string // initial body markdown; "" seeds a heading from the title
}

// EpicSetOpts are the edits `furrow epic set` applies. A nil pointer leaves the
// field alone; SetMeta/RmMeta are applied in that order so `--meta k=v --rm-meta k`
// is a delete, not an ordering puzzle.
type EpicSetOpts struct {
	Title     *string
	Goal      *string
	SetMeta   map[string]string
	RmMeta    []string
	AddLabels []string
	RmLabels  []string
	AddRepos  []string
	RmRepos   []string
	// Standing / Pinned flip the v7 declarations (see core.Epic); a nil pointer
	// leaves the flag alone, so `--standing=false` is an explicit clear, never a
	// default.
	Standing *bool
	Pinned   *bool
}

func (o EpicSetOpts) empty() bool {
	return o.Title == nil && o.Goal == nil && len(o.SetMeta) == 0 && len(o.RmMeta) == 0 &&
		len(o.AddLabels) == 0 && len(o.RmLabels) == 0 && len(o.AddRepos) == 0 && len(o.RmRepos) == 0 &&
		o.Standing == nil && o.Pinned == nil
}

// EpicAdd creates a box. It never sets Active: opening a box is a separate,
// deliberate act (`furrow epic activate`), so creating one can never silently
// change what `furrow next` hands out.
func (a *App) EpicAdd(title string, o EpicAddOpts) (*core.Epic, error) {
	title = core.NormalizeTitle(title)
	if strings.TrimSpace(title) == "" {
		return nil, core.Validationf("", "epic title must not be empty")
	}
	if err := requireNonBlank("", "--label", o.Labels); err != nil {
		return nil, err
	}
	for k := range o.Meta {
		if strings.TrimSpace(k) == "" {
			return nil, core.Validationf("", "--meta key must not be empty")
		}
	}

	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	repos, err := a.resolveEpicRepos(o.Repos)
	if err != nil {
		return nil, err
	}
	// FALLBACK to the board-scope repo when no -r was given: a repo-less box
	// on a scoped board was invisible to the scoped `epic ls` (and to brief's
	// epic header) the moment it was made — the state a task only reaches by
	// asking for --draft. Deliberately WEAKER than task Add's withBoardRepo
	// union: an explicit `epic add -r other/repo` names the box's realm on
	// purpose (a requests/cross-repo box is a normal shape), so the scope repo
	// is not glued on top the way it is for a task. Shed a wrong fallback with
	// `epic set --rm-repo`.
	if len(repos) == 0 && a.DefaultRepo != "" {
		repos = []string{a.DefaultRepo}
	}

	id, err := a.uniqueEpicID(epics)
	if err != nil {
		return nil, err
	}
	now := a.Clock.Now()
	e := core.Epic{
		ID: id, Title: title, Goal: strings.TrimSpace(o.Goal),
		Labels: o.Labels, Repos: repos, Meta: o.Meta,
		Created: now, Updated: now, Body: core.BodyPath(id),
	}

	body := o.Body
	if body == "" {
		body = "# " + title + "\n"
	}
	// Body first, then the shard: an interrupted create leaves at worst an orphan
	// body file (which lint reports), never a shard pointing at nothing.
	if err := a.Store.SaveBody(id, body); err != nil {
		return nil, err
	}
	if err := a.Store.SaveEpic(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// EpicQueryOpts filters `epic ls`.
type EpicQueryOpts struct {
	All   bool   // include closed epics (default: open only)
	Label string // tag filter — the task reads' -l semantics (comma = OR, exact match)
	Repo  string // owner/repo filter, already resolved
	Limit int
}

// EpicList returns the boxes matching o, active first, then open by id, then
// closed by id — the order attention should go in.
func (a *App) EpicList(o EpicQueryOpts) ([]EpicItem, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	counts := epicProgress(idx, a.Cfg.DoneLane)
	doneIDs := a.doneSet(idx)

	out := make([]EpicItem, 0, len(epics))
	for i := range epics {
		e := epics[i]
		if !o.All && !e.IsOpen() {
			continue
		}
		// matchAnyLabel is the task reads' -l matcher (comma = OR, exact), so a
		// box filter can never mean something different from `ls -l` — the old
		// single-token containsFold silently dropped every value after a comma.
		if !matchAnyLabel(o.Label, e.Labels) {
			continue
		}
		if o.Repo != "" && !anyRepoMatch(e.Repos, []string{o.Repo}) {
			continue
		}
		out = append(out, EpicItem{Epic: e, Progress: counts[e.ID], Stuck: a.epicStuck(idx, e.ID, doneIDs),
			OpenDeps: openEpicDeps(&e, epics)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := epicRank(out[i].Epic), epicRank(out[j].Epic)
		if ri != rj {
			return ri < rj
		}
		return out[i].Epic.ID < out[j].Epic.ID
	})
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, nil
}

// epicRank orders `epic ls`: the active box first (the focus), then pinned
// open boxes (the always-visible channels), then plain open, then closed. A
// box both active and pinned ranks as active — focus outranks channel.
func epicRank(e core.Epic) int {
	switch {
	case e.Active && e.IsOpen():
		return 0
	case e.Pinned && e.IsOpen():
		return 1
	case e.IsOpen():
		return 2
	default:
		return 3
	}
}

// EpicShow resolves ref and returns the box with its members. It loads the
// whole epic set (not just the one shard) so the dep edges can be resolved to
// titles+states in the same read.
func (a *App) EpicShow(ref string) (*EpicDetail, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	id, err := a.resolveEpicIn(ref, epics)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*core.Epic, len(epics))
	for i := range epics {
		byID[epics[i].ID] = &epics[i]
	}
	e := byID[id]
	deps := []EpicRef{}
	for _, d := range e.Deps {
		deps = append(deps, resolveEpicRef(byID, d))
	}
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	items, err := a.ListItems(QueryOpts{Epic: id})
	if err != nil {
		return nil, err
	}
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return nil, err
	}
	return &EpicDetail{
		Epic:     *e,
		Progress: epicProgress(idx, a.Cfg.DoneLane)[id],
		Stuck:    a.epicStuck(idx, id, a.doneSet(idx)),
		Deps:     deps,
		Tasks:    items,
		Body:     body,
	}, nil
}

// EpicSet applies the metadata edits. A no-op request is exit 2 rather than a
// silent `updated` bump — the same rule Set follows for tasks.
func (a *App) EpicSet(ref string, o EpicSetOpts) (*core.Epic, *core.Epic, error) {
	if o.empty() {
		return nil, nil, core.Validationf(ref, "epic set needs at least one change (--title / --goal / --meta / --rm-meta / --add-label / --rm-label / --add-repo / --rm-repo)")
	}
	if err := requireNonBlank(ref, "--add-label", o.AddLabels); err != nil {
		return nil, nil, err
	}
	repos, err := a.resolveEpicRepos(o.AddRepos)
	if err != nil {
		return nil, nil, err
	}
	return a.mutateEpic(ref, func(e *core.Epic) error {
		if o.Title != nil {
			t := core.NormalizeTitle(*o.Title)
			if strings.TrimSpace(t) == "" {
				return core.Validationf(e.ID, "epic title must not be empty")
			}
			e.Title = t
		}
		if o.Goal != nil {
			e.Goal = strings.TrimSpace(*o.Goal)
		}
		if o.Standing != nil {
			e.Standing = *o.Standing
		}
		if o.Pinned != nil {
			e.Pinned = *o.Pinned
		}
		if len(o.SetMeta) > 0 && e.Meta == nil {
			e.Meta = map[string]string{}
		}
		for k, v := range o.SetMeta {
			if strings.TrimSpace(k) == "" {
				return core.Validationf(e.ID, "--meta key must not be empty")
			}
			e.Meta[k] = v
		}
		for _, k := range o.RmMeta {
			delete(e.Meta, k)
		}
		// labelDelta is the task-side set editor; reusing it keeps add/remove
		// semantics (and their edge cases) identical across the two entities.
		e.Labels = labelDelta(e.Labels, o.AddLabels, o.RmLabels)
		e.Repos = labelDelta(e.Repos, repos, o.RmRepos)
		return nil
	})
}

// EpicActivate opens a box for work. This is where the per-repo single-active
// invariant is enforced.
//
// The constraint is per REPO, not per board: reads are repo-scoped by default, so
// a board-wide limit would force a deactivate/activate every time you changed
// repo — friction that outweighs what it buys. An epic naming several repos
// consumes a slot in EVERY one of them, because a cross-repo epic really is "the
// current core" on both sides; the check stays a count per repo.
//
// A repo-LESS epic cannot be activated at all: with no repo to consume, it would
// slip past the per-repo count, and you could hold arbitrarily many "current"
// boxes at once.
//
// furrow does NOT police WHO calls this — a CLI has no caller identity, and a
// prohibition would get in the way the one time the switch is legitimate. It
// RECORDS instead: the switch is appended to the epic's body, and `furrow sync`
// surfaces the session's switches. The risk worth catching is not "the agent
// switched on purpose" but "the agent read an instruction AS a switch", and a
// record makes that visible in the same session rather than weeks later.
//
// openDeps names the target's deps still open (openEpicDeps) — the "this box
// was meant to open after those" edges being crossed. Deliberately a WARNING
// payload, never a gate: the dep is information (see Epic.Deps), the ordering
// is advice, and the day it must be crossed the tool must not stand in the
// way. The CLI prints it to stderr and puts it in the --json envelope; lint's
// epic-dep-open keeps the state visible afterwards.
func (a *App) EpicActivate(ref, reason string) (before, after *core.Epic, openDeps []string, err error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, nil, nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, nil, nil, err
	}
	var target *core.Epic
	for i := range epics {
		if epics[i].ID == id {
			target = &epics[i]
		}
	}
	if target == nil {
		return nil, nil, nil, core.NotFound(id)
	}
	if !target.IsOpen() {
		return nil, nil, nil, core.Validationf(id, "epic %s is closed — run `furrow epic reopen %s` first", id, id)
	}
	if len(target.Repos) == 0 {
		return nil, nil, nil, core.Validationf(id, "epic %s names no repo — attach one first (`furrow epic set %s --add-repo <owner/repo>`); a repo-less box would bypass the one-active-per-repo rule", id, id)
	}
	openDeps = openEpicDeps(target, epics)
	if target.Active {
		// Idempotent: re-activating the open box is a no-op, not an error.
		b := *target
		return &b, target, openDeps, nil
	}
	if err := a.checkRepoSlots(target, epics); err != nil {
		return nil, nil, nil, err
	}

	before, after, err = a.mutateEpic(id, func(e *core.Epic) error { e.Active = true; return nil })
	if err != nil {
		return nil, nil, nil, err
	}
	if err := a.recordSwitch(id, reason); err != nil {
		return nil, nil, nil, err
	}
	return before, after, openDeps, nil
}

// checkRepoSlots refuses an activation that would give a repo two active boxes,
// naming the repo AND the incumbent so the fix is one command away.
func (a *App) checkRepoSlots(target *core.Epic, epics []core.Epic) error {
	held := map[string]string{} // repo -> the epic already holding its slot
	for i := range epics {
		e := &epics[i]
		if e.ID == target.ID || !e.Active || !e.IsOpen() {
			continue
		}
		for _, r := range e.Repos {
			held[r] = e.ID
		}
	}
	var clashes []string
	details := map[string]any{}
	for _, r := range target.Repos {
		if other, ok := held[r]; ok {
			clashes = append(clashes, fmt.Sprintf("%s (held by %s)", r, other))
			details[r] = other
		}
	}
	if len(clashes) == 0 {
		return nil
	}
	sort.Strings(clashes)
	return &core.Error{
		Code:    core.CodeValidation,
		Kind:    core.KindEpicActiveClash,
		Subject: target.ID,
		Msg:     fmt.Sprintf("another epic is already active for %s — deactivate it first (`furrow epic deactivate <id>`)", strings.Join(clashes, ", ")),
		Details: map[string]any{"held": details},
	}
}

// recordSwitch appends the activation to the epic's body. It is deliberately in
// the BODY and not a shard field: the body is prose furrow never parses, so the
// log can grow without a schema decision, and it lands in the same git history a
// reviewer already reads.
func (a *App) recordSwitch(id, reason string) error {
	line := a.Clock.Now().Format("2006-01-02 15:04") + " activated"
	if r := strings.TrimSpace(reason); r != "" {
		line += " — " + r
	}
	return a.appendBody(id, line)
}

// EpicNote appends text as a new paragraph to an EPIC's body AND stamps the
// box's Updated — AddNote's box twin, and deliberately the SAME operation
// rather than a second one wearing an epic's name: epic bodies live in the
// task bodies/ directory (see core.Epic.Body), appendBody is shared, and the
// only thing that differs is which shard's `updated` moves. That is why
// `furrow note` routes to it (RefTargetsEpic) instead of growing an `epic
// note` verb — a box's progress record is not a different kind of writing.
//
// The write ORDER matches AddNote's for the same reason: the body lands inside
// the mutate closure, i.e. before mutateEpic stamps Updated and saves the
// shard, so a partial failure costs a timestamp, never content, and a re-run
// re-appends and fixes Updated.
//
// ref resolves like every other epic reference (exact id, unique id prefix,
// unique title substring) — so an unknown box is exit 2 with candidates, not
// the task path's exit 1. An empty/whitespace-only note is exit 2, checked
// BEFORE resolution exactly as AddNote checks it before the index load.
func (a *App) EpicNote(ref, text string) (*core.Epic, *core.Epic, error) {
	text, err := normalizeNote(ref, text)
	if err != nil {
		return nil, nil, err
	}
	return a.mutateEpicProse(ref, func(e *core.Epic) error { return a.appendBody(e.ID, text) })
}

// EpicSetBody replaces an EPIC's entire body AND stamps the box's Updated —
// SetBody's box twin, routed to by `furrow edit --body` exactly as EpicNote is
// by `furrow note` (membership decides, never the id's prefix), and the same
// operation rather than a second one wearing an epic's name: the bodies/
// directory is shared and only the shard whose `updated` moves differs. The
// empty-replacement check runs BEFORE resolution, matching EpicNote's order.
func (a *App) EpicSetBody(ref, text string) (*core.Epic, *core.Epic, error) {
	text, err := normalizeBody(ref, text)
	if err != nil {
		return nil, nil, err
	}
	return a.mutateEpicProse(ref, func(e *core.Epic) error { return a.saveBody(e.ID, text) })
}

// RefTargetsEpic reports whether an id-keyed command's ref belongs to the EPIC
// store rather than the task index — the one routing decision behind `furrow
// note` / `edit` / `show` taking either entity.
//
// MEMBERSHIP decides, never the id's prefix. sync.go's publishedSwitches
// already states the rule and the reason ("Membership in the epic store — not
// an id-prefix guess — decides ... so a custom [ids] prefix cannot fool the
// scan"), and a prefix guess really does misroute here: config accepts an
// [ids].epic_prefix that EXTENDS [ids].prefix (or, through a clamp that is a
// no-op when the colliding value is the default, one equal to it), and ids are
// prefix + random base32 — so under `prefix = "t-"` with `epic_prefix = "t-e"`,
// roughly one TASK id in 32 is shaped like an epic id. A shape test hands those
// to the box path, and `furrow note` on an ordinary task fails.
//
// So: a ref that names a real task is a task, always; else a ref the epic store
// RESOLVES (exact id → unique id prefix → unique title substring, the contract
// every other epic reference follows) is that box; else the shape decides — and
// only to pick whose ERROR the caller gets (an unknown `e-` id deserves the
// epic resolver's exit 2 + candidates, an unknown task id the task path's exit
// 1), never where a write lands. It therefore returns an error only when the
// store cannot be read: an unresolvable ref is a routing answer, not a failure,
// which is what keeps the write paths' own validation order intact (an empty
// note is still exit 2 before any lookup).
func (a *App) RefTargetsEpic(ref string) (bool, error) {
	idx, err := a.load()
	if err != nil {
		return false, err
	}
	if idx.Has(ref) {
		return false, nil
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return false, err
	}
	if _, err := a.resolveEpicIn(ref, epics); err == nil {
		return true, nil
	}
	return a.isEpicIDShape(ref), nil
}

// isEpicIDShape reports whether ref LOOKS like an epic id ([ids].epic_prefix +
// base32) — "an id names its entity kind on sight" (core.Epic.ID). It is a
// guess, deliberately confined to choosing the error for a ref that resolved to
// nothing at all: see RefTargetsEpic for why it must never route a write.
func (a *App) isEpicIDShape(ref string) bool {
	if !a.Cfg.EpicIDPattern().MatchString(ref) {
		return false
	}
	if a.Cfg.IDPattern().MatchString(ref) {
		// Both shapes match — one prefix extends the other. The longer prefix is
		// the more specific claim, so it picks the error. Nothing is written on
		// this branch either way.
		return len(a.Cfg.EpicIDPrefix) > len(a.Cfg.IDPrefix)
	}
	return true
}

// EpicDeactivate closes the box for work without closing the box.
func (a *App) EpicDeactivate(ref string) (*core.Epic, *core.Epic, error) {
	return a.mutateEpic(ref, func(e *core.Epic) error { e.Active = false; return nil })
}

// EpicDone closes a box, and clears Active in the SAME write — a closed epic that
// kept its flag would hold its repos' slots forever and nothing could be opened
// after it.
//
// It deliberately does NOT pick the next box. Choosing what to work on is the one
// judgement here that is genuinely the human's; furrow does the bookkeeping and
// then `furrow lint`'s epic-no-active nags until a person decides.
func (a *App) EpicDone(ref string) (*core.Epic, *core.Epic, error) {
	return a.mutateEpic(ref, func(e *core.Epic) error {
		if !e.IsOpen() {
			return core.Validationf(e.ID, "epic %s is already closed", e.ID)
		}
		now := a.Clock.Now()
		e.Closed = &now
		e.Active = false
		return nil
	})
}

// EpicReopen clears a closed box's Closed stamp — the inverse of EpicDone, for
// the box closed by mistake (recovery used to be a hand-edit of the shard,
// which the store contract forbids, or a git revert on the board repo). It
// never re-activates: the box comes back OPEN and INACTIVE, and choosing it
// again stays the human's own `epic activate` — reopening is a deliberate act,
// activating is another. An already-open box is exit 2, mirroring done's
// already-closed refusal.
func (a *App) EpicReopen(ref string) (*core.Epic, *core.Epic, error) {
	return a.mutateEpic(ref, func(e *core.Epic) error {
		if e.IsOpen() {
			return core.Validationf(e.ID, "epic %s is already open", e.ID)
		}
		e.Closed = nil
		return nil
	})
}

// ActiveEpicFor returns the id of the epic active for repo, or "" when none is.
// An empty repo means "any repo": it returns the single active epic when there is
// exactly one board-wide, else "" — the board-wide read (`-r ”`) uses
// ActiveEpicIDs instead.
func (a *App) ActiveEpicFor(repo string) (string, error) {
	ids, err := a.ActiveEpicIDs(repo)
	if err != nil || len(ids) != 1 {
		return "", err
	}
	return ids[0], nil
}

// ActiveEpicIDs returns every open+active epic id, optionally narrowed to those
// naming repo. Sorted, so a caller's output never depends on store order.
func (a *App) ActiveEpicIDs(repo string) ([]string, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	var out []string
	for i := range epics {
		e := &epics[i]
		if !e.Active || !e.IsOpen() {
			continue
		}
		if repo != "" && !anyRepoMatch(e.Repos, []string{repo}) {
			continue
		}
		out = append(out, e.ID)
	}
	sort.Strings(out)
	return out, nil
}

// EpicScope is how Next scoped (or will scope) a read — exposed so the CLI can
// explain an empty result without re-deriving the rule.
type EpicScope struct {
	// Engaged reports whether the active-epic scope applies at all. It is false
	// when the read carries an explicit -e or --all-epics (both bypass the
	// scope), and — deliberately — on a board with no epics at all: a board that
	// never declared a box is not participating, and scoping it would make every
	// `furrow next` empty until an epic exists. Declaring the first box is what
	// switches the discipline on (the same rule epic-required/epic-no-active
	// follow in lint).
	Engaged bool
	// Active is the set of open+active epic ids for the read's repo scope
	// (o.Repo, else o.ScopeRepo, else board-wide). Empty while Engaged means
	// "a participating board with no box open": Next matches nothing EXCEPT the
	// pinned pass-through below.
	Active map[string]bool
	// Pinned is the set of open+PINNED epic ids (v7) — the boxes whose
	// actionable tasks pass through the active scope, at the top of the result.
	// Deliberately NOT repo-filtered, unlike Active: pinning holds no per-repo
	// slot, and the read's own repo filter already scopes the TASKS it can
	// surface — filtering the boxes too would only hide a cross-repo channel.
	Pinned map[string]bool
}

// NextScope computes the active-epic scope Next will apply to o. One epic is
// active per repo, so a repo-scoped read sees at most one id; a board-wide read
// (`-r ”`) sees every repo's active box — their union, not an error, since a
// cross-repo listing has no single focus to pick. Pinned boxes ride alongside
// regardless of repo (see EpicScope.Pinned).
func (a *App) NextScope(o QueryOpts) (EpicScope, error) {
	if o.Epic != "" || o.AllEpics {
		return EpicScope{}, nil
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return EpicScope{}, err
	}
	if len(epics) == 0 {
		return EpicScope{}, nil
	}
	repo := o.Repo
	if repo == "" {
		repo = o.ScopeRepo
	}
	s := EpicScope{Engaged: true, Active: map[string]bool{}, Pinned: map[string]bool{}}
	for i := range epics {
		e := &epics[i]
		if !e.IsOpen() {
			continue
		}
		if e.Pinned {
			s.Pinned[e.ID] = true
		}
		if !e.Active {
			continue
		}
		if repo != "" && !anyRepoMatch(e.Repos, []string{repo}) {
			continue
		}
		s.Active[e.ID] = true
	}
	return s, nil
}

// ResolveEpic turns a user-supplied reference into an epic id, shaped exactly
// like ResolveRepo so `-e` is as typo-safe as `-r`: an exact id, else a unique id
// prefix, else a unique case-folded title substring. Anything ambiguous or absent
// is exit 2 with the candidates.
func (a *App) ResolveEpic(ref string) (string, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return "", err
	}
	return a.resolveEpicIn(ref, epics)
}

func (a *App) resolveEpicIn(ref string, epics []core.Epic) (string, error) {
	if ref == "" {
		return "", core.Validationf("", "epic reference must not be empty")
	}
	ids := make([]string, 0, len(epics))
	for i := range epics {
		ids = append(ids, epics[i].ID)
		if epics[i].ID == ref {
			return ref, nil
		}
	}
	sort.Strings(ids)

	var byPrefix, byTitle []string
	lower := strings.ToLower(ref)
	for i := range epics {
		e := &epics[i]
		if strings.HasPrefix(e.ID, ref) {
			byPrefix = append(byPrefix, e.ID)
		}
		if strings.Contains(strings.ToLower(e.Title), lower) {
			byTitle = append(byTitle, e.ID)
		}
	}
	for _, hits := range [][]string{byPrefix, byTitle} {
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
			continue
		default:
			sort.Strings(hits)
			return "", &core.Error{
				Code:       core.CodeValidation,
				Kind:       core.KindEpicAmbiguous,
				Msg:        fmt.Sprintf("epic %q is ambiguous (%s)", ref, strings.Join(hits, ", ")),
				Candidates: hits,
			}
		}
	}
	return "", &core.Error{
		Code:       core.CodeValidation,
		Kind:       core.KindEpicNotFound,
		Msg:        fmt.Sprintf("unknown epic %q", ref),
		Candidates: ids,
	}
}

// mutateEpic is the single write funnel: load, apply, stamp `updated` when the
// edit changed the box's shard, save.
func (a *App) mutateEpic(ref string, fn func(*core.Epic) error) (*core.Epic, *core.Epic, error) {
	return a.mutateEpicStamping(ref, false, fn)
}

// mutateEpicProse is mutateEpic for the paths that write a box's PROSE (a note,
// a body replacement). The body is the box's content but lives outside the
// shard, so the shard comparison structurally cannot see it and the stamp must
// be unconditional — the task side draws exactly the same line (see
// App.stampIfChanged).
func (a *App) mutateEpicProse(ref string, fn func(*core.Epic) error) (*core.Epic, *core.Epic, error) {
	return a.mutateEpicStamping(ref, true, fn)
}

func (a *App) mutateEpicStamping(ref string, alwaysStamp bool, fn func(*core.Epic) error) (*core.Epic, *core.Epic, error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, nil, err
	}
	e, ok, err := a.Store.LoadEpic(id)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, core.NotFound(id)
	}
	before := *e
	snap, err := core.MarshalEpic(e)
	if err != nil {
		return nil, nil, err
	}
	if err := fn(e); err != nil {
		return nil, nil, err
	}
	// Same rule as tasks (App.stampIfChanged): a box whose edit changed nothing
	// keeps its clock, so `epic set` re-run with the values it already has is a
	// true no-op instead of a fresh shard and a fresh commit.
	if alwaysStamp {
		e.Updated = a.Clock.Now()
	} else if err := a.stampEpicIfChanged(e, snap); err != nil {
		return nil, nil, err
	}
	if err := a.Store.SaveEpic(e); err != nil {
		return nil, nil, err
	}
	return &before, e, nil
}

// uniqueEpicID draws a fresh id and retries on the (astronomically rare) clash
// with one already in the store — the epic twin of uniqueID.
func (a *App) uniqueEpicID(epics []core.Epic) (string, error) {
	taken := make(map[string]bool, len(epics))
	for i := range epics {
		taken[epics[i].ID] = true
	}
	for attempt := 0; attempt < 8; attempt++ {
		id, err := a.Store.NextEpicID()
		if err != nil {
			return "", err
		}
		if !taken[id] {
			return id, nil
		}
	}
	return "", core.Internalf("", "could not draw a free epic id after 8 attempts")
}

// resolveEpicRepos runs an epic's repo arguments through the SAME strict resolver
// tasks use, so `-r furrow` means the same thing on both entities.
func (a *App) resolveEpicRepos(repos []string) ([]string, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	return resolveRepoArgs(repos, "", repoUniverse(idx, a.BoardRepos))
}
