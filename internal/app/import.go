package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
)

// AddSpec is one task to bulk-create via AddMany (e.g. from migrate). Like Add,
// a nil Priority means "append in lane". Body follows Add's policy exactly —
// taken verbatim when set, a "# <title>" heading seeded when empty — which is
// what lets migrate supply a full markdown body.
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
		// Fold the title exactly as single Add does. A bulk title is ordinary user
		// input (`furrow add --stdin`, a migrate import), not a hand-edited shard,
		// so it must not be the one door that lets a control character through: the
		// title is spliced into the body's "# " heading and printed by ls, which is
		// the body-injection/escape-sequence vector NormalizeTitle closes. Written
		// back into specs[i] so the construction loop below stores the folded form
		// and every error message below quotes it.
		s.Title = core.NormalizeTitle(s.Title)
		specs[i].Title = s.Title
		// specf prefixes an error with WHICH spec failed. A closed-vocabulary gate
		// must reuse the SAME constructor single Add uses (unknownLaneErr /
		// unknownTypeErr / resolveRepoArgs) and only prefix its message: rebuilding
		// it with Validationf would drop Candidates, so `add --stdin -s ghots` used
		// to exit 2 with no did-you-mean list while `add -s ghots` had one — the
		// bulk-vs-single divergence class of t-adx9/t-ek9y, in the error shape
		// instead of the write.
		specf := func(err error) error {
			if fe := core.AsError(err); fe != nil {
				return fe.WithPrefixf("spec %d (%q): ", i, s.Title)
			}
			return err
		}
		if s.Title == "" {
			return nil, core.Validationf("", "spec %d has an empty title", i)
		}
		lane := s.Status
		if lane == "" {
			lane = a.Cfg.DefaultLane
		}
		if !a.Cfg.IsLane(lane) {
			return nil, specf(a.unknownLaneErr("", lane))
		}
		if a.Cfg.LabelsRequired && len(s.Labels) == 0 {
			return nil, core.Validationf("", "spec %d (%q): a label is required ([labels].required)", i, s.Title)
		}
		if s.Draft && len(s.Repos) > 0 {
			return nil, core.Validationf("", "spec %d (%q): --draft cannot be combined with an explicit repo (-r)", i, s.Title)
		}
		if err := s.requireNonBlankValues(""); err != nil {
			return nil, specf(err)
		}
		repos, err := resolveRepoArgs(s.Repos, "", universe)
		if err != nil {
			return nil, specf(err)
		}
		specs[i].Repos = a.withBoardRepo(repos, s.Draft)
		// A dep must pre-exist (validated against the pre-batch index):
		// batch ids are minted below, so an intra-batch reference is impossible —
		// a dangling one would silently drop the task out of `next`. Checked after
		// repo resolution so the error precedence matches single Add.
		for _, dep := range s.Deps {
			if !idx.Has(dep) {
				return nil, core.Validationf("", "spec %d (%q): dependency %q does not exist", i, s.Title, dep)
			}
		}
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
			ID: id, Title: s.Title, Status: lane, Priority: prio,
			Value: cloneIntp(s.Value), Effort: cloneIntp(s.Effort),
			Labels: s.Labels, Repos: s.Repos, Deps: s.Deps, Refs: s.Refs,
			Checklist: seedChecklist(s.Checklist),
			Created:   now, Updated: now, Body: core.BodyPath(id),
			Epic: s.Epic,
		}
		// Mirror Add: a task born in the done lane is closed at birth, so bulk
		// `add --stdin -s done` doesn't leak the same closed:null zombie. A
		// per-iteration copy keeps each Closed pointer distinct.
		if lane == a.Cfg.DoneLane {
			c := now
			t.Closed = &c
		}
		body := s.Body
		if body == "" {
			body = "# " + t.Title + "\n"
		}
		if err := a.saveBody(id, body); err != nil {
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
