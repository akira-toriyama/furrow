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
}

// EpicDetail is `epic show`: the box, its roll-up, its members in canonical
// order (lane -> priority -> id, the same order `ls` prints), and its body —
// the prose record (goal context, the activation log) a task's `show` would
// print, folded in for the same reason: the follow-up read is the read.
type EpicDetail struct {
	Epic     core.Epic
	Progress Progress
	Stuck    bool
	Tasks    []ListItem
	Body     string
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
}

func (o EpicSetOpts) empty() bool {
	return o.Title == nil && o.Goal == nil && len(o.SetMeta) == 0 && len(o.RmMeta) == 0 &&
		len(o.AddLabels) == 0 && len(o.RmLabels) == 0 && len(o.AddRepos) == 0 && len(o.RmRepos) == 0
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
	Label string // tag filter
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
		if o.Label != "" && !containsFold(e.Labels, o.Label) {
			continue
		}
		if o.Repo != "" && !anyRepoMatch(e.Repos, []string{o.Repo}) {
			continue
		}
		out = append(out, EpicItem{Epic: e, Progress: counts[e.ID], Stuck: a.epicStuck(idx, e.ID, doneIDs)})
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

func epicRank(e core.Epic) int {
	switch {
	case e.Active && e.IsOpen():
		return 0
	case e.IsOpen():
		return 1
	default:
		return 2
	}
}

// EpicShow resolves ref and returns the box with its members.
func (a *App) EpicShow(ref string) (*EpicDetail, error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, err
	}
	e, ok, err := a.Store.LoadEpic(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, core.NotFound(id)
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
func (a *App) EpicActivate(ref, reason string) (*core.Epic, *core.Epic, error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, nil, err
	}
	var target *core.Epic
	for i := range epics {
		if epics[i].ID == id {
			target = &epics[i]
		}
	}
	if target == nil {
		return nil, nil, core.NotFound(id)
	}
	if !target.IsOpen() {
		return nil, nil, core.Validationf(id, "epic %s is closed — reopen it before activating", id)
	}
	if len(target.Repos) == 0 {
		return nil, nil, core.Validationf(id, "epic %s names no repo — attach one first (`furrow epic set %s --add-repo <owner/repo>`); a repo-less box would bypass the one-active-per-repo rule", id, id)
	}
	if target.Active {
		// Idempotent: re-activating the open box is a no-op, not an error.
		before := *target
		return &before, target, nil
	}
	if err := a.checkRepoSlots(target, epics); err != nil {
		return nil, nil, err
	}

	before, after, err := a.mutateEpic(id, func(e *core.Epic) error { e.Active = true; return nil })
	if err != nil {
		return nil, nil, err
	}
	if err := a.recordSwitch(id, reason); err != nil {
		return nil, nil, err
	}
	return before, after, nil
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
		ID:      target.ID,
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
	// "a participating board with no box open": Next matches NOTHING.
	Active map[string]bool
}

// NextScope computes the active-epic scope Next will apply to o. One epic is
// active per repo, so a repo-scoped read sees at most one id; a board-wide read
// (`-r ”`) sees every repo's active box — their union, not an error, since a
// cross-repo listing has no single focus to pick.
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
	s := EpicScope{Engaged: true, Active: map[string]bool{}}
	for i := range epics {
		e := &epics[i]
		if !e.Active || !e.IsOpen() {
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
				ID:         "",
				Msg:        fmt.Sprintf("epic %q is ambiguous (%s)", ref, strings.Join(hits, ", ")),
				Candidates: hits,
			}
		}
	}
	return "", &core.Error{
		Code:       core.CodeValidation,
		ID:         "",
		Msg:        fmt.Sprintf("unknown epic %q", ref),
		Candidates: ids,
	}
}

// mutateEpic is the single write funnel: load, apply, stamp `updated`, save.
func (a *App) mutateEpic(ref string, fn func(*core.Epic) error) (*core.Epic, *core.Epic, error) {
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
	if err := fn(e); err != nil {
		return nil, nil, err
	}
	e.Updated = a.Clock.Now()
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

// containsFold is a case-insensitive membership test for label filters.
func containsFold(ss []string, v string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
