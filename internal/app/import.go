package app

import (
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
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
	// ID is a pre-minted id (AddBatch's, drawn through uniqueIDExcluding so the
	// batch can reference itself before the write); "" lets addMany mint one.
	// Internal: no front-end may choose an id.
	ID string
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
// specErr prefixes a batch spec's error with WHICH spec failed (prefixed
// callers only). A closed-vocabulary gate must reuse the SAME constructor
// single Add uses and only prefix its message: rebuilding it with Validationf
// would drop Candidates (the bulk-vs-single divergence class of t-adx9/t-ek9y,
// in the error shape instead of the write).
func specErr(prefixed bool, i int, title string, err error) error {
	if !prefixed {
		return err
	}
	if fe := core.AsError(err); fe != nil {
		return fe.WithPrefixf("spec %d (%q): ", i, title)
	}
	return err
}

func (a *App) addMany(specs []AddSpec, prefixed bool) ([]core.Task, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	b := a.newAddBatch(idx, specs)
	// Validate every spec (and resolve every ref) before writing anything, so a
	// bad spec fails before the first body hits disk.
	for i := range specs {
		if err := a.validateAddSpec(b, i, &specs[i], prefixed); err != nil {
			return nil, err
		}
	}
	// One guard call for the whole batch, on the union of every spec's repos
	// AFTER the board-scope union above — so a bare `add` inside a checkout is
	// judged on the repo it silently attaches. Before the first body hits disk.
	var batchRepos []string
	for _, s := range specs {
		batchRepos = unionRepos(batchRepos, s.Repos)
	}
	if err := a.guardRepos("", batchRepos, "--draft"); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(specs))
	// Bodies are composed here and written AFTER the last spec passed, in one
	// staged write: a per-spec saveBody left the earlier specs' bodies on disk
	// when a later spec was refused (an `orphan-body` the next sync published —
	// t-t2fv), while the error said nothing was written.
	bodies := make(map[string]string, len(specs))
	for i := range specs {
		t, body, err := a.composeAddTask(b, i, &specs[i], prefixed)
		if err != nil {
			return nil, err
		}
		bodies[t.ID] = body
		idx.Add(t)
		ids = append(ids, t.ID)
	}
	if err := a.saveBodies(bodies); err != nil {
		return nil, err
	}
	if err := a.saveIndex(idx); err != nil {
		// The index refused (a duplicate id on disk, a marshal failure): the
		// bodies just staged belong to tasks that do not exist, so take them
		// back out — best effort, the refusal is the error worth reporting.
		for id := range bodies {
			_ = a.deleteBody(id)
		}
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

// addBatch is the state one addMany call shares across its specs: the index
// being written, what every spec resolves against, and the per-spec results
// the validation pass hands the construction pass.
type addBatch struct {
	idx      *core.Index
	universe []string // the repo universe every -r resolves against
	// now is the batch's one instant — it stamps created/updated AND binds
	// every `--due` offset, so a bulk `--due +1d` is the same instant for
	// every task in the write.
	now time.Time
	// preminted is the ids AddBatch drew before the write (drawn through
	// uniqueIDExcluding), which a dep may name before the task exists.
	preminted map[string]bool
	dues      []*time.Time // per spec, the bound --due (nil = none)
	// inheritedEpic is the scope's single active box, computed once for the
	// batch on the first spec that needs it (nil = not yet computed).
	inheritedEpic *string
	// epics is the epic set read once for the batch, on the first --anchor
	// (nil = not yet read).
	epics []core.Epic
}

func (a *App) newAddBatch(idx *core.Index, specs []AddSpec) *addBatch {
	b := &addBatch{idx: idx, universe: repoUniverse(idx, a.BoardRepos), now: a.Clock.Now(),
		preminted: map[string]bool{}, dues: make([]*time.Time, len(specs))}
	for _, s := range specs {
		if s.ID != "" {
			b.preminted[s.ID] = true
		}
	}
	return b
}

// validateAddSpec is addMany's pre-flight on spec i, writing the resolved
// forms (folded title, default label, resolved repos/epic/anchor) back into s
// and the bound due into b.dues[i]. Every rule here mirrors single Add's, in
// single Add's error precedence — the bulk-vs-single divergence class
// add_parity_test.go records (t-adx9: --value/--effort dropped; t-ek9y:
// --type dropped) is structurally impossible because Add IS a one-spec call.
func (a *App) validateAddSpec(b *addBatch, i int, s *AddSpec, prefixed bool) error {
	// Union the board's literal label tag into the spec up front, so the
	// LabelsRequired check below and the created task both see it.
	s.Labels = a.withDefaultLabel(s.Labels)
	// Fold the title exactly as single Add does. A bulk title is ordinary user
	// input (`furrow add --stdin`, a migrate import), not a hand-edited shard,
	// so it must not be the one door that lets a control character through: the
	// title is spliced into the body's "# " heading and printed by ls, which is
	// the body-injection/escape-sequence vector NormalizeTitle closes. Written
	// back into the spec so the construction pass stores the folded form and
	// every error message below quotes it.
	s.Title = core.NormalizeTitle(s.Title)
	// specf prefixes an error with WHICH spec failed. A closed-vocabulary gate
	// must reuse the SAME constructor single Add uses (unknownLaneErr /
	// resolveRepoArgs) and only prefix its message: rebuilding
	// it with Validationf would drop Candidates, so `add --stdin -s ghots` used
	// to exit 2 with no did-you-mean list while `add -s ghots` had one — the
	// bulk-vs-single divergence class of t-adx9/t-ek9y, in the error shape
	// instead of the write.
	specf := func(err error) error { return specErr(prefixed, i, s.Title, err) }
	if s.Title == "" {
		if prefixed {
			return core.Validationf("", "spec %d has an empty title", i)
		}
		return core.Validationf("", "title must not be empty")
	}
	if why := core.TitleTooLong(s.Title); why != "" {
		return specf(core.Validationf("", "%s", why))
	}
	lane := s.Status
	if lane == "" {
		lane = a.Cfg.DefaultLane
	}
	if !a.Cfg.IsLane(lane) {
		return specf(a.unknownLaneErr("", lane))
	}
	if a.Cfg.LabelsRequired && len(s.Labels) == 0 {
		return specf(core.Validationf("", "a label is required ([labels].required); add -l <label>"))
	}
	if s.Draft && len(s.Repos) > 0 {
		return specf(core.Validationf("", "--draft cannot be combined with an explicit repo (-r): a draft is attached to no repo"))
	}
	if err := s.requireNonBlankValues(""); err != nil {
		return specf(err)
	}
	repos, err := resolveRepoArgs(s.Repos, "", b.universe)
	if err != nil {
		return specf(err)
	}
	s.Repos = a.withBoardRepo(repos, s.Draft)
	// A dep must pre-exist (validated against the pre-batch index) or name an
	// id this batch pre-minted (AddBatch, which resolves keys to ids before
	// calling here) — a dangling one would silently drop the task out of
	// `next`. Checked after repo resolution so the error precedence matches
	// single Add.
	for _, dep := range s.Deps {
		if !b.idx.Has(dep) && !b.preminted[dep] {
			return specf(core.Validationf("", "dependency %q does not exist", dep))
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
			return specf(err)
		}
		s.Epic = resolved
	case !s.NoEpic:
		// Mirror single Add's active-epic inheritance (computed once — the
		// scope's single active box is the same for the whole batch).
		if b.inheritedEpic == nil {
			inherited := a.inheritableEpic()
			b.inheritedEpic = &inherited
		}
		s.Epic = *b.inheritedEpic
	}
	// Mirror single Add's due binding, for the same reason the epic ref is
	// resolved here: the bulk path must not be the one door a raw spelling —
	// or a malformed one — reaches a shard through.
	if s.Due != "" {
		d, err := parseDueAt(s.Due, b.now, a.loc())
		if err != nil {
			return specf(err)
		}
		b.dues[i] = &d
	}
	// The anchor pointer resolves against the epic set read once for the
	// batch, and its two invariants (a due to follow with, no repeat rule)
	// are judged here so a bad spec fails before the first body hits disk.
	if s.Anchor != "" {
		if b.epics == nil {
			if b.epics, err = a.Store.LoadEpics(); err != nil {
				return err
			}
		}
		resolved, err := a.resolveAnchorRef(s.Anchor, b.epics)
		if err != nil {
			return specf(err)
		}
		if b.dues[i] == nil {
			return specf(core.Validationf("", "--anchor needs a --due: the pointer says what the due follows, and there is no due"))
		}
		if s.Repeat != "" {
			return specf(core.Validationf("", "a repeating task follows its own repeat_anchor, not a box's day; --anchor and --repeat cannot ride together"))
		}
		s.Anchor = resolved
	}
	return nil
}

// composeAddTask is addMany's construction pass on a VALIDATED spec: the task
// as it will be written (id minted here unless pre-minted) and its body (the
// spec's verbatim, or the seeded "# <title>" heading). Nothing is saved.
func (a *App) composeAddTask(b *addBatch, i int, s *AddSpec, prefixed bool) (core.Task, string, error) {
	specf := func(err error) error { return specErr(prefixed, i, s.Title, err) }
	lane := s.Status
	if lane == "" {
		lane = a.Cfg.DefaultLane
	}
	id := s.ID
	if id == "" {
		var err error
		if id, err = a.uniqueIDExcluding(b.idx, b.preminted); err != nil {
			return core.Task{}, "", err
		}
	}
	var prio int
	if s.Priority != nil {
		prio = *s.Priority
	} else {
		prio = b.idx.NextPriority(lane, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
	}
	t := core.Task{
		ID: id, Title: s.Title, Status: lane, Priority: prio,
		Value: cloneIntp(s.Value), Effort: cloneIntp(s.Effort),
		Labels: s.Labels, Repos: s.Repos, Deps: s.Deps, Refs: s.Refs,
		Checklist: seedChecklist(s.Checklist),
		Created:   b.now, Updated: b.now, Body: core.BodyPath(id),
		Epic: s.Epic, Due: b.dues[i], Anchor: s.Anchor,
	}
	if s.Priority != nil {
		a.noteSharedPriority(b.idx, &t)
	}
	// Mirror Add: a task born in the done lane is closed at birth, so bulk
	// `add --stdin -s done` doesn't leak the same closed:null zombie. A
	// per-task copy keeps each Closed pointer distinct.
	if lane == a.Cfg.DoneLane {
		c := b.now
		t.Closed = &c
	}
	if s.Repeat != "" {
		// A task born in the done lane is closed at birth, and only a CLOSE
		// advances a series — so the rule would sit on a task that can never
		// fire it, a series with no live occurrence and nothing to say so.
		if lane == a.Cfg.DoneLane {
			return core.Task{}, "", specf(core.Validationf("", "a task created in the %q lane is closed at birth, so a --repeat rule on it could never fire — create it open, or drop --repeat", lane))
		}
		if err := a.bindRepeat("", &t, s.Repeat); err != nil { // no task exists yet to name
			return core.Task{}, "", specf(err)
		}
	}
	body := s.Body
	if body == "" {
		body = "# " + t.Title + "\n"
	}
	return t, body, nil
}
