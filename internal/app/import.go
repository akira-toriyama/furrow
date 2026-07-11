package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// AddSpec is one task to bulk-create via AddMany (e.g. from migrate). Like Add,
// a nil Priority means "append in lane"; unlike Add, Body is taken verbatim
// (migrate supplies the full markdown body).
type AddSpec struct {
	Title string
	AddOpts
}

// AddMany creates several tasks and saves the index ONCE, so a migrate import is
// a single atomic write rather than N. Bodies are written first (an interrupted
// import leaves at worst orphan body files, which `furrow lint` reports). All
// lanes are validated up front so a bad spec fails before anything is written.
func (a *App) AddMany(specs []AddSpec) ([]core.Task, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}

	// Union the board's literal label tag into every spec up front, so the
	// LabelsRequired check below and the created tasks both see it.
	for i := range specs {
		specs[i].Labels = a.withDefaultLabel(specs[i].Labels)
	}

	// validate every lane/title (and resolve every repo) before writing
	// anything, so a bad spec fails before the first body hits disk.
	universe := repoUniverse(idx, a.BoardRepos)
	for i, s := range specs {
		if strings.TrimSpace(s.Title) == "" {
			return nil, core.Validationf("", "spec %d has an empty title", i)
		}
		lane := s.Status
		if lane == "" {
			lane = a.Cfg.DefaultLane
		}
		if !a.Cfg.IsLane(lane) {
			return nil, core.Validationf("", "spec %d (%q): unknown lane %q", i, s.Title, lane)
		}
		if a.Cfg.LabelsRequired && len(s.Labels) == 0 {
			return nil, core.Validationf("", "spec %d (%q): a label is required ([labels].required)", i, s.Title)
		}
		if s.Draft && len(s.Repos) > 0 {
			return nil, core.Validationf("", "spec %d (%q): --draft cannot be combined with an explicit repo (-r)", i, s.Title)
		}
		repos, err := resolveRepoArgs(s.Repos, "", universe)
		if err != nil {
			return nil, err
		}
		specs[i].Repos = a.withBoardRepo(repos, s.Draft)
	}

	now := a.Clock.Now()
	ids := make([]string, 0, len(specs))
	for _, s := range specs {
		lane := s.Status
		if lane == "" {
			lane = a.Cfg.DefaultLane
		}
		id, err := a.uniqueID(idx)
		if err != nil {
			return nil, err
		}
		var prio int
		if s.Priority != nil {
			prio = *s.Priority
		} else {
			prio = idx.NextPriority(lane, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
		}
		t := core.Task{
			ID: id, Title: strings.TrimSpace(s.Title), Status: lane, Priority: prio,
			Value: cloneIntp(s.Value), Effort: cloneIntp(s.Effort),
			Labels: s.Labels, Repos: s.Repos, Parent: s.Parent, Deps: s.Deps, Refs: s.Refs,
			Created: now, Updated: now, Body: core.BodyPath(id),
		}
		body := s.Body
		if body == "" {
			body = "# " + t.Title + "\n"
		}
		if err := a.Store.SaveBody(id, body); err != nil {
			return nil, err
		}
		idx.Add(t)
		ids = append(ids, id)
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	// Return the tasks as a subsequent read emits them. Save canonicalizes the
	// index only as a side effect of fsstore marshalling each shard in place;
	// the memstore twin doesn't, so canonicalize here explicitly ([]-not-null
	// slices, sorted+deduped sets) and return those. This keeps bulk-add's JSON
	// deep-equal to a following `ls` for any Store — without the pre-Save
	// structs' `null` slices leaking out, and without a redundant store reload.
	core.Canonicalize(idx, a.Cfg.Lanes)
	created := make([]core.Task, 0, len(ids))
	for _, id := range ids {
		t, _ := idx.Find(id)
		if t == nil {
			return nil, core.Internalf(id, "bulk-added task missing after save")
		}
		created = append(created, *t)
	}
	return created, nil
}
