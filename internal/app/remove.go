package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `furrow rm` / `furrow epic rm` — withdrawing a filing.
//
// Role: delete a task or a box outright, shard AND body AND assets, the way
// the operator used to do it by hand with `git rm` (2026-09-10, t-mc03). The
// hand procedure had four holes this file closes: it bypassed the session
// write guard; the reverse references (another task's dep edge, a [[link]]
// in some body, a box's members) were checked by a human grep, and any that
// slipped became lint's dep-missing / dangling-link; a plain `furrow sync`
// would not push a commit furrow did not make; and the procedure lived in
// one session's memory. Not `archive`: archive RETIRES done work into a
// sibling store and is a round trip; rm withdraws a record that should not
// have been filed, and is not.
//
// Contract:
//   - Preview unless Apply (the destructive-op guard archive/tidy share).
//   - A target still referenced is refused (kind `referenced`, exit 2, every
//     reference in details.references) unless Force, which SEVERS them: a dep
//     edge is dropped, a [[id]] link is de-linked to the bare id (the prose
//     keeps its words), a member is unfiled, an epic dep is dropped. Severing
//     never advances `updated` — bookkeeping, not progress (tidy's rule).
//   - The id is never reused: ids are random, nothing recycles a freed one.
//   - Guarded like every task/epic write, on the targets AND on every entity
//     Force edits.
//   - Write order: the index (the removal itself plus the severed edges)
//     first, then the de-linked bodies, then the targets' bodies and assets.
//     An interruption after the index write leaves an orphan body or a
//     dangling link — both lint-visible, neither a task without its shard.

// RemoveOpts drives RemoveTasks / RemoveEpic.
type RemoveOpts struct {
	Force bool // sever the references instead of refusing on them
	Apply bool // write; false previews
}

// DepEdge is one dependency edge: From's deps names To (task→task, or with
// epic ids, box→box).
type DepEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// LinkRef is one live [[To]] link inside bodies/<Body>.md.
type LinkRef struct {
	Body string `json:"body"`
	To   string `json:"to"`
}

// MemberRef is a task filed under a box.
type MemberRef struct {
	Task string `json:"task"`
	Epic string `json:"epic"`
}

// References is everything still pointing at an rm target — what a removal
// must refuse on, or with --force sever. Every slice is [] not null in JSON,
// so a consumer indexes without a nil check.
type References struct {
	Deps     []DepEdge   `json:"deps"`      // task dep edges naming a target task
	Links    []LinkRef   `json:"links"`     // live [[id]] links in any body
	Members  []MemberRef `json:"members"`   // tasks filed under a target epic
	EpicDeps []DepEdge   `json:"epic_deps"` // epic dep edges naming a target epic
}

func newReferences() References {
	return References{Deps: []DepEdge{}, Links: []LinkRef{}, Members: []MemberRef{}, EpicDeps: []DepEdge{}}
}

// Empty reports whether nothing references the targets.
func (r References) Empty() bool {
	return len(r.Deps)+len(r.Links)+len(r.Members)+len(r.EpicDeps) == 0
}

// Summary is the one-line human rendering the refusal message carries.
func (r References) Summary() string {
	var parts []string
	if n := len(r.Deps); n > 0 {
		parts = append(parts, fmt.Sprintf("%d dep edge(s) from %s", n, strings.Join(uniqueFrom(r.Deps), ", ")))
	}
	if n := len(r.Links); n > 0 {
		bodies := make([]string, 0, n)
		seen := map[string]bool{}
		for _, l := range r.Links {
			if !seen[l.Body] {
				seen[l.Body] = true
				bodies = append(bodies, core.BodyPath(l.Body))
			}
		}
		parts = append(parts, fmt.Sprintf("%d [[link]](s) in %s", n, strings.Join(bodies, ", ")))
	}
	if n := len(r.Members); n > 0 {
		ids := make([]string, 0, n)
		for _, m := range r.Members {
			ids = append(ids, m.Task)
		}
		parts = append(parts, fmt.Sprintf("%d member(s): %s", n, strings.Join(ids, ", ")))
	}
	if n := len(r.EpicDeps); n > 0 {
		parts = append(parts, fmt.Sprintf("%d epic dep edge(s) from %s", n, strings.Join(uniqueFrom(r.EpicDeps), ", ")))
	}
	return strings.Join(parts, "; ")
}

func uniqueFrom(edges []DepEdge) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range edges {
		if !seen[e.From] {
			seen[e.From] = true
			out = append(out, e.From)
		}
	}
	return out
}

// RemoveTasksReport is `furrow rm`'s result: the targets (as they were) and
// the references found — refused without Force, severed with it.
type RemoveTasksReport struct {
	DryRun     bool        `json:"dry_run"`
	Force      bool        `json:"force"`
	Tasks      []core.Task `json:"tasks"`
	References References  `json:"references"`
}

// RemoveEpicReport is `furrow epic rm`'s result.
type RemoveEpicReport struct {
	DryRun     bool       `json:"dry_run"`
	Force      bool       `json:"force"`
	Epic       *core.Epic `json:"epic"`
	References References `json:"references"`
}

// RemoveTasks deletes the named tasks — shard, body, assets — all-or-nothing:
// a miss removes nothing (the batch not-found shape, details.missing, with
// the archived enrichment: an archived id is not removable, unarchive it
// first). Duplicates collapse. References among the targets themselves do not
// count (removing a chain in one call is fine).
func (a *App) RemoveTasks(ids []string, o RemoveOpts) (*RemoveTasksReport, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	var targets []core.Task
	var missing []string
	targetSet := map[string]bool{}
	for _, id := range ids {
		if targetSet[id] || contains(missing, id) {
			continue
		}
		t, i := idx.Find(id)
		if i < 0 {
			missing = append(missing, id)
			continue
		}
		targetSet[id] = true
		targets = append(targets, *t)
	}
	if len(missing) > 0 {
		return nil, a.batchMissingErr(missing, len(targets)+len(missing), "removed")
	}

	refs := newReferences()
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if targetSet[t.ID] {
			continue
		}
		for _, dep := range t.Deps {
			if targetSet[dep] {
				refs.Deps = append(refs.Deps, DepEdge{From: t.ID, To: dep})
			}
		}
	}
	linkRe := core.LinkPattern(a.Cfg.IDPrefix)
	links, err := a.linksInto(targetSet, linkRe)
	if err != nil {
		return nil, err
	}
	refs.Links = links

	rep := &RemoveTasksReport{DryRun: !o.Apply, Force: o.Force, Tasks: targets, References: refs}
	if !refs.Empty() && !o.Force {
		return nil, referencedErr(strings.Join(ids, ","), refs)
	}
	if !o.Apply {
		return rep, nil
	}
	if err := a.Store.Writable(); err != nil {
		return nil, err
	}
	// The guard runs over everything the write touches before anything lands:
	// the targets, and under --force the owners of every severed reference.
	for i := range targets {
		if err := a.guardTask(&targets[i]); err != nil {
			return nil, err
		}
	}
	if err := a.guardReferenceOwners(idx, nil, refs); err != nil {
		return nil, err
	}

	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if targetSet[t.ID] {
			continue
		}
		t.Deps = without(t.Deps, targetSet)
	}
	assetsByID, err := a.assetsByOwner(targets)
	if err != nil {
		return nil, err
	}
	for id := range targetSet {
		idx.Remove(id)
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	if err := a.unlinkBodies(refs.Links, linkRe, targetSet); err != nil {
		return nil, err
	}
	for _, t := range targets {
		if err := a.deleteBody(t.ID); err != nil {
			return nil, err
		}
		for _, name := range assetsByID[t.ID] {
			if err := a.Store.DeleteAsset(name); err != nil {
				return nil, err
			}
		}
	}
	return rep, nil
}

// RemoveEpic deletes one box — shard, body, assets — resolving ref like every
// epic command (exact id, unique prefix, unique title substring). Its
// references are its members (the `epic` field of each), the boxes whose deps
// name it, and the live [[e-id]] links in any body but its own.
func (a *App) RemoveEpic(ref string, o RemoveOpts) (*RemoveEpicReport, error) {
	id, err := a.ResolveEpic(ref)
	if err != nil {
		return nil, err
	}
	e, ok, err := a.Store.LoadEpic(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &core.Error{Code: core.CodeValidation, Kind: core.KindEpicNotFound, Subject: id, Msg: fmt.Sprintf("unknown epic %q", ref)}
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	targetSet := map[string]bool{id: true}
	refs := newReferences()
	for i := range idx.Tasks {
		if idx.Tasks[i].Epic == id {
			refs.Members = append(refs.Members, MemberRef{Task: idx.Tasks[i].ID, Epic: id})
		}
	}
	for i := range epics {
		if epics[i].ID == id {
			continue
		}
		if contains(epics[i].Deps, id) {
			refs.EpicDeps = append(refs.EpicDeps, DepEdge{From: epics[i].ID, To: id})
		}
	}
	linkRe := core.LinkPattern(a.Cfg.EpicIDPrefix)
	links, err := a.linksInto(targetSet, linkRe)
	if err != nil {
		return nil, err
	}
	refs.Links = links

	rep := &RemoveEpicReport{DryRun: !o.Apply, Force: o.Force, Epic: e, References: refs}
	if !refs.Empty() && !o.Force {
		return nil, referencedErr(id, refs)
	}
	if !o.Apply {
		return rep, nil
	}
	if err := a.Store.Writable(); err != nil {
		return nil, err
	}
	if err := a.guardRepos(id, e.Repos, ""); err != nil {
		return nil, err
	}
	if err := a.guardReferenceOwners(idx, epics, refs); err != nil {
		return nil, err
	}

	for i := range idx.Tasks {
		if idx.Tasks[i].Epic == id {
			idx.Tasks[i].Epic = ""
		}
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	for i := range epics {
		if epics[i].ID == id || !contains(epics[i].Deps, id) {
			continue
		}
		epics[i].Deps = without(epics[i].Deps, targetSet)
		if err := a.Store.SaveEpic(&epics[i]); err != nil {
			return nil, err
		}
	}
	if err := a.unlinkBodies(refs.Links, linkRe, targetSet); err != nil {
		return nil, err
	}
	if err := a.Store.DeleteEpic(id); err != nil {
		return nil, err
	}
	if err := a.deleteBody(id); err != nil {
		return nil, err
	}
	assets, err := a.Store.ListAssets()
	if err != nil {
		return nil, err
	}
	for _, as := range assets {
		if strings.HasPrefix(as.Name, id+"-") {
			if err := a.Store.DeleteAsset(as.Name); err != nil {
				return nil, err
			}
		}
	}
	return rep, nil
}

// linksInto scans every body but the targets' own for live [[id]] links naming
// a target — the reverse of Backlinks, over BOTH entity kinds' bodies (the
// shared bodies/ dir holds task and epic prose alike). Sorted by body id, then
// by target, so the report is stable.
func (a *App) linksInto(targetSet map[string]bool, re *regexp.Regexp) ([]LinkRef, error) {
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	out := []LinkRef{}
	for _, bid := range bodyIDs {
		if targetSet[bid] {
			continue
		}
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		var hits []string
		for _, ref := range core.ExtractLinks(body, re) {
			if targetSet[ref] {
				hits = append(hits, ref)
			}
		}
		sort.Strings(hits)
		for _, ref := range hits {
			out = append(out, LinkRef{Body: bid, To: ref})
		}
	}
	return out, nil
}

// unlinkBodies rewrites each linking body once, de-linking every [[id]] that
// names a target. Goes through saveBody so the edit rides the body journal
// (autocommit / a plain sync publish it).
func (a *App) unlinkBodies(links []LinkRef, re *regexp.Regexp, targetSet map[string]bool) error {
	done := map[string]bool{}
	for _, l := range links {
		if done[l.Body] {
			continue
		}
		done[l.Body] = true
		body, err := a.Store.LoadBody(l.Body)
		if err != nil {
			return err
		}
		text, n := core.UnlinkIDs(body, re, targetSet)
		if n == 0 {
			continue
		}
		if err := a.saveBody(l.Body, text); err != nil {
			return err
		}
	}
	return nil
}

// guardReferenceOwners runs the session guard over every entity a --force
// removal edits besides the targets: the tasks losing a dep edge or a box, the
// boxes losing a dep edge, and the owners of the de-linked bodies (a task, a
// box, or nobody for an orphan body). epics may be nil when no box is in play.
func (a *App) guardReferenceOwners(idx *core.Index, epics []core.Epic, refs References) error {
	epicByID := make(map[string]*core.Epic, len(epics))
	for i := range epics {
		epicByID[epics[i].ID] = &epics[i]
	}
	guardOwner := func(id string) error {
		if t, i := idx.Find(id); i >= 0 {
			return a.guardTask(t)
		}
		if e := epicByID[id]; e != nil {
			return a.guardRepos(e.ID, e.Repos, "")
		}
		return nil
	}
	for _, d := range refs.Deps {
		if err := guardOwner(d.From); err != nil {
			return err
		}
	}
	for _, m := range refs.Members {
		if err := guardOwner(m.Task); err != nil {
			return err
		}
	}
	for _, d := range refs.EpicDeps {
		if err := guardOwner(d.From); err != nil {
			return err
		}
	}
	for _, l := range refs.Links {
		if err := guardOwner(l.Body); err != nil {
			return err
		}
	}
	return nil
}

// referencedErr is the refusal: exit 2, kind `referenced`, every reference in
// details.references (the same object the report carries), and the escape
// named — --force, or dropping the references by hand first.
func referencedErr(subject string, refs References) *core.Error {
	return &core.Error{
		Code:    core.CodeValidation,
		Kind:    core.KindReferenced,
		Subject: subject,
		Msg: fmt.Sprintf("still referenced — %s; drop the references first, or pass --force to sever them "+
			"(dep edges dropped, [[links]] de-linked to the bare id, members unfiled)", refs.Summary()),
		Details: map[string]any{"references": refs},
	}
}

// without returns set minus the ids in drop, preserving order.
func without(set []string, drop map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, s := range set {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}
