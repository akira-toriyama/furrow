package app

import (
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ImportPlan is what an import's ONE shared epic resolves to, computed against
// the board before anything is written. It exists so a dry-run preview states
// the same outcome `--yes` produces: the ref is resolved (an unresolvable `-e`
// fails at preview, not after N bodies have hit disk), and the active-box
// inheritance AddMany would apply is run here instead, so the caller can feed
// the RESOLVED id back into every AddSpec and the two paths cannot diverge.
type ImportPlan struct {
	// Epic is the epic id every imported task is filed under; "" = unfiled.
	Epic string
	// Inherited reports that Epic came from the scope's single active box
	// rather than an explicit ref — the disclosure `add` owes a bare capture.
	Inherited bool
	// Declared reports that the board has at least one box, which is exactly
	// the gate lint's epic-required uses: on a board that never declared one,
	// an unfiled task is not a defect.
	Declared bool
}

// PlanImport resolves the shared epic for an import. noEpic is the explicit
// "unfiled, on purpose" (`-e ”`), which suppresses the inheritance a bare
// import gets — `add`'s NoEpic contract, applied to the whole batch.
func (a *App) PlanImport(epicRef string, noEpic bool) (ImportPlan, error) {
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return ImportPlan{}, err
	}
	p := ImportPlan{Declared: len(epics) > 0}
	switch {
	case epicRef != "":
		id, rerr := a.ResolveEpic(epicRef)
		if rerr != nil {
			return ImportPlan{}, rerr
		}
		p.Epic = id
	case !noEpic:
		p.Epic = a.inheritableEpic()
		p.Inherited = p.Epic != ""
	}
	return p, nil
}

// UnfiledImportWarning is lint's epic-required, said BEFORE the write instead of
// after it: an import that lands open tasks under no box on a board that has
// boxes exits 0 and turns `furrow lint` red, and nothing in migrate's own
// warnings used to mention it. statuses are the lanes the parsed tasks carry
// ("" = the default lane). Terminal lanes are excluded for the same reason
// epic-required exempts them — a Done archive predating the box is not a defect
// — so the count is what lint will actually report, not the import size.
// Returns "" when there is nothing to say.
func (a *App) UnfiledImportWarning(p ImportPlan, statuses []string) string {
	if !p.Declared || p.Epic != "" {
		return ""
	}
	open := 0
	for _, s := range statuses {
		if s == "" {
			s = a.Cfg.DefaultLane
		}
		if !a.Cfg.IsTerminal(s) {
			open++
		}
	}
	if open == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d of %d imported task(s) land in an open lane with no epic — furrow lint will report epic-required for each; re-run with -e <epic>, or file them after the fact with `furrow set <id>... -e <epic>`",
		open, len(statuses))
}

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
	return a.addMany(specs, true)
}

// addMany is the ONE implementation of the task-creation rule — single Add is a
// one-element call of this (t-dk6w), so the bulk-vs-single divergence class
// add_parity_test.go records (t-adx9: --value/--effort dropped; t-ek9y: --type
// dropped; --check dropped; an unfolded title) is structurally impossible: a
// rule added here exists on both paths by construction. prefixed controls the
// "spec %d (%q): " error prefix — the bulk callers keep it (a failing spec must
// be nameable in a batch), single Add drops it (its errors keep the classic
// single-task wording).
func (a *App) addMany(specs []AddSpec, prefixed bool) ([]core.Task, error) {
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
	// One `now` for the whole batch — it stamps created/updated below AND binds
	// every `--due` offset, so a bulk `--due +1d` is the same instant for every
	// task in the write. Parsed here, in the pre-flight, so a malformed spelling
	// fails before the first body hits disk (single Add's contract).
	now := a.Clock.Now()
	inheritedEpic, inheritComputed := "", false
	dues := make([]*time.Time, len(specs))
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
			if !prefixed {
				return err
			}
			if fe := core.AsError(err); fe != nil {
				return fe.WithPrefixf("spec %d (%q): ", i, s.Title)
			}
			return err
		}
		if s.Title == "" {
			if prefixed {
				return nil, core.Validationf("", "spec %d has an empty title", i)
			}
			return nil, core.Validationf("", "title must not be empty")
		}
		lane := s.Status
		if lane == "" {
			lane = a.Cfg.DefaultLane
		}
		if !a.Cfg.IsLane(lane) {
			return nil, specf(a.unknownLaneErr("", lane))
		}
		if a.Cfg.LabelsRequired && len(s.Labels) == 0 {
			return nil, specf(core.Validationf("", "a label is required ([labels].required); add -l <label>"))
		}
		if s.Draft && len(s.Repos) > 0 {
			return nil, specf(core.Validationf("", "--draft cannot be combined with an explicit repo (-r): a draft is attached to no repo"))
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
				return nil, specf(core.Validationf("", "dependency %q does not exist", dep))
			}
		}
		// Mirror single Add's epic resolution. Without this the bulk path would
		// store the raw REFERENCE (a title substring, an id prefix) as if it were an
		// epic id — a task filed under a box that does not exist, reported only by
		// lint. This is the exact bulk-vs-single divergence add_parity_test.go
		// exists to catch, and --type had it before this field did.
		switch {
		case s.Epic != "":
			resolved, err := a.ResolveEpic(s.Epic)
			if err != nil {
				return nil, specf(err)
			}
			specs[i].Epic = resolved
		case !s.NoEpic:
			// Mirror single Add's active-epic inheritance (computed once — the
			// scope's single active box is the same for the whole batch).
			if !inheritComputed {
				inheritedEpic = a.inheritableEpic()
				inheritComputed = true
			}
			specs[i].Epic = inheritedEpic
		}
		// Mirror single Add's due binding, for the same reason the epic ref is
		// resolved here: the bulk path must not be the one door a raw spelling —
		// or a malformed one — reaches a shard through.
		if s.Due != "" {
			d, err := ParseDue(s.Due, now, a.loc())
			if err != nil {
				return nil, specf(err)
			}
			dues[i] = &d
		}
	}

	ids := make([]string, 0, len(specs))
	for i, s := range specs {
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
			Epic: s.Epic, Due: dues[i],
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
	// Return the tasks as a subsequent read emits them: Save normalized each of
	// them in place ([]-not-null slices, sorted+deduped sets, UTC whole-second
	// stamps — see Store.Save's contract), so bulk-add's JSON is deep-equal to a
	// following `ls` with no re-canonicalize and no redundant store reload. This
	// used to call core.Canonicalize explicitly because the memstore twin skipped
	// the normalization fsstore performs, i.e. the tasks were canonical in
	// production and `null`-slice-shaped in every test.
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
