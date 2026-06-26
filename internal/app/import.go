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

	// validate every lane/title before writing anything.
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
	}

	now := a.Clock.Now()
	created := make([]core.Task, 0, len(specs))
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
			Labels: s.Labels, Parent: s.Parent, Deps: s.Deps, Refs: s.Refs,
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
		created = append(created, t)
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	return created, nil
}
