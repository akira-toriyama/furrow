package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// LintErrorSummary counts lint ERRORS by code — the terse ride-along `furrow
// sync` prints beside its revisit summary. Warnings are deliberately excluded:
// the sync line exists so an error (a state someone must fix, e.g.
// epic-required after a migration) surfaces in the loop that always runs, and
// folding warnings in would train the reader to ignore the line.
type LintErrorSummary struct {
	Errors int            `json:"errors"`
	Codes  map[string]int `json:"codes"`
}

// LintErrorCounts runs the ordinary lint pass and reduces it to error counts by
// code. It is exactly a.Lint() plus counting — no second rule set — so the sync
// summary can never disagree with `furrow lint`.
func (a *App) LintErrorCounts() (LintErrorSummary, error) {
	ps, err := a.Lint()
	if err != nil {
		return LintErrorSummary{}, err
	}
	sum := LintErrorSummary{Codes: map[string]int{}}
	for _, p := range ps {
		if p.Severity == core.SevError {
			sum.Errors++
			sum.Codes[p.Code]++
		}
	}
	return sum, nil
}

// Lint runs the full consistency check: core's in-memory rules plus the
// filesystem-level index<->body 1:1 mapping (every task has a body file and
// vice versa). Config clamp warnings are surfaced too, so `furrow lint` is the
// one place that tells you everything that is off.
//
// The filesystem-side checks mirror how the core rules are already split — one
// named sweep per file kind (lintRecordKeys, lintStoreShape, lintBodyContent,
// lintConfigProblems below), stitched here in dependency order: the store shape
// yields the live id set the body scan resolves links against.
func (a *App) Lint() ([]core.Problem, error) {
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	// The estimate range check runs on the RAW (pre-clamp) index: Canonicalize
	// would otherwise round a hand-edited out-of-range value/effort away before
	// we could warn about it. Everything else is order-independent, so we
	// canonicalize after and validate as before.
	ps := core.EstimateProblems(idx)
	core.Canonicalize(idx, a.Cfg.Lanes)
	ps = append(ps, core.Validate(idx, a.Cfg.Lanes, a.Cfg.IDPattern())...)
	// Dependency cycles: prevented at mutation time, but a concurrent merge of
	// two half-edges on separate shards can slip one in silently (the tasks then
	// wait on each other forever and never surface in `next`). lint is the backstop.
	ps = append(ps, core.CycleProblems(idx)...)
	// The epic rules need a SECOND store read (the boxes), which is why they are
	// their own entry point rather than another Validate parameter — Validate's
	// contract is "everything derivable from the Index alone".
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil, err
	}
	ps = append(ps, core.EpicProblems(idx, epics, a.Cfg.Terminal, a.Cfg.EpicIDPattern())...)
	for _, e := range epics {
		if p, ok := unknownKeyProblem(e.ID, "epic shard "+core.EpicPath(e.ID), e.ExtraKeys()); ok {
			ps = append(ps, p)
		}
	}

	// One pass over the task shards: collect the done set and flag per-shard
	// findings (unknown keys, done-unclosed) on the way.
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
		if p, ok := unknownKeyProblem(t.ID, "task shard "+core.TaskPath(t.ID), t.ExtraKeys()); ok {
			ps = append(ps, p)
		}
		// A title with an interior control character (newline/tab/…) injects
		// structure into the body's "# " heading and splits table rows. Add and
		// retitle now normalize it, but a bulk import or a hand-edited shard can
		// still slip one in — flag it (a `furrow retitle` normalizes it away).
		if core.TitleHasControl(t.Title) {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "control-char", ID: t.ID, Msg: "title contains a control character (newline/tab/etc.); `furrow retitle` will normalize it"})
		}
		// A blank entry in a set-valued field. Every mutating flag now refuses one
		// (requireNonBlank), so a new one cannot be created — but an older binary
		// wrote them freely (`set --add-label ""`, and any CSV trailing comma), and
		// a blank sorts FIRST, so it leads every rendered label list. This is the
		// backstop for what is already on disk; `furrow label <id> --rm ""`
		// cannot clear it (that is now exit 2), so the message names the fix.
		for _, f := range []struct {
			field string
			vals  []string
		}{{"labels", t.Labels}, {"refs", t.Refs}, {"repos", t.Repos}, {"deps", t.Deps}} {
			if hasBlank(f.vals) {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "blank-entry", ID: t.ID, Msg: "an empty string in " + f.field + " (written before blanks were refused); remove it from the shard by hand"})
			}
		}
		if t.Status == a.Cfg.DoneLane {
			doneIDs[t.ID] = true
			// A done task with no closed timestamp is a zombie: `archive` skips it
			// forever and its close date is lost. New ones can't be created (Add
			// stamps, Move backfills); this catches pre-fix or hand-edited leaks.
			if t.Closed == nil {
				ps = append(ps, core.Problem{Severity: core.SevError, Code: "done-unclosed", ID: t.ID, Msg: "task is in the done lane but has no closed timestamp (a `furrow done` will backfill it)"})
			}
		}
	}
	// A label that NAMES a repo (a full owner/repo, or a short name the board's
	// repo universe resolves) is the repos-pivot leak resurfacing: a repo is a
	// first-class field, not a tag. The read-side -l did-you-mean guard
	// deliberately never second-guesses a label that EXISTS — so the moment one
	// such label lands on one task, that guard goes quiet for the whole board
	// (13 leaked instances rode that silence, 2026-07-26). This warn is where
	// the "no repo-named labels" invariant actually lives; `add`/`set` stay
	// permissive because a colliding tag can be legitimate and a hard refusal
	// on the write path would ban it outright.
	universe := repoUniverse(idx, a.BoardRepos)
	for _, t := range idx.Tasks {
		for _, l := range t.Labels {
			if len(core.RepoMatches(l, universe)) > 0 {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "repo-as-label", ID: t.ID,
					Msg: fmt.Sprintf("label %q names a repo — repos are first-class, not tags (`furrow repo %s --add %s` then `furrow label %s --rm %s`)", l, t.ID, l, t.ID, l)})
			}
		}
	}
	// Reconcile gaps (warn): a non-terminal task whose done dependency closed
	// after the task was last touched. This is the structural backstop for
	// reconcile-on-close — the always-on (hook/CI) twin of the `dep_done` revisit
	// signal, so an epic whose slice shipped never silently keeps a stale body.
	ps = append(ps, core.StaleDepProblems(idx, a.Cfg.Terminal, doneIDs)...)

	// ready-blocked (ERROR): a task sitting in an actionable ([next].lanes) lane
	// with an unsatisfied dep — `furrow next` will never hand it out, so its lane
	// is lying. `brief` shows the same set as `blocked` for whoever runs brief;
	// this is the always-on twin. The lane vocabulary is the board config's, so
	// the rule follows a [next].lanes override automatically.
	nextLanes := make(map[string]bool, len(a.Cfg.NextLanes))
	for _, l := range a.Cfg.NextLanes {
		nextLanes[l] = true
	}
	ps = append(ps, core.ReadyBlockedProblems(idx, nextLanes, doneIDs)...)

	// Due dates (due-overdue = ERROR, due-today = warn). Board-wide on purpose:
	// no epic scope and no `next` lane filter, because the work that carries a
	// date is usually parked outside the focus — the case this exists for sat in
	// `waiting`, under a box nobody had activated, where `next` could never have
	// surfaced it. `brief` leads with the same set (App.Due), and both go through
	// core.DueStateOf, so the session-start read and the checker cannot disagree
	// about the day boundary or the skipped lanes.
	ps = append(ps, core.DueProblems(idx, a.Clock.Now(), a.loc(), a.dueSkipLanes())...)

	// Provenance ([lint].provenance_markers, OFF by default): warn on an open,
	// non-terminal task whose body carries none of the board's provenance
	// markers. The rule exists because a tracker entry is read as FACT by every
	// later session — a 2026-07-26 sweep task-ified 7 single-source observations
	// unrefuted, and a later refutation pass killed 6 of 11 — and prose rules
	// alone demonstrably do not stop it. The check only pressures the writer to
	// state "where this came from / how it was verified"; it cannot judge
	// correctness. The marker VOCABULARY is the operator's (a board convention,
	// set in the board's own config so it syncs), never furrow's: shipping
	// default wording would bless one language and one house style.
	if len(a.Cfg.LintProvenanceMarkers) > 0 {
		for i := range idx.Tasks {
			t := &idx.Tasks[i]
			if a.Cfg.IsTerminal(t.Status) {
				continue
			}
			body, err := a.Store.LoadBody(t.ID)
			if err != nil {
				return nil, err
			}
			if !containsAnyFold(body, a.Cfg.LintProvenanceMarkers) {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "provenance-missing", ID: t.ID,
					Msg: fmt.Sprintf("body records no provenance (none of the [lint].provenance_markers: %s) — say where this came from and how it was verified, e.g. `furrow note %s \"...\"`",
						strings.Join(a.Cfg.LintProvenanceMarkers, ", "), t.ID)})
			}
		}
	}

	rps, err := a.lintRecordKeys()
	if err != nil {
		return nil, err
	}
	ps = append(ps, rps...)

	shapePs, hasTask, hasEpic, bodyIDs, err := a.lintStoreShape(idx, epics)
	if err != nil {
		return nil, err
	}
	ps = append(ps, shapePs...)

	bodyPs, err := a.lintBodyContent(hasTask, hasEpic, bodyIDs)
	if err != nil {
		return nil, err
	}
	ps = append(ps, bodyPs...)

	ps = append(ps, a.lintConfigProblems(idx)...)

	// A board still on an older layout than this binary is read-only (every write
	// hits the store's gate). Warn, don't error: that state is the legitimate
	// middle of a flag day, and erroring would red every repo's board-lint CI for
	// the whole window. The write gate is already the hard stop — this is just the
	// thing that makes the state visible before someone runs into it.
	//
	// schemaState covers BOTH stores (the archive carries its own meta.json), and
	// it knows that an unstamped but EMPTY board is version 0 yet writable.
	if bv, state, _ := a.schemaState(); state == SchemaOutdated {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "schema-outdated", ID: "meta",
			Msg: fmt.Sprintf("board is schema v%d; this furrow writes v%d — writes are refused until `furrow upgrade` runs (a flag day: bump every pinned caller FIRST)", bv, core.SchemaVersion)})
	}

	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Severity != ps[j].Severity {
			return ps[i].Severity < ps[j].Severity
		}
		if ps[i].ID != ps[j].ID {
			return ps[i].ID < ps[j].ID
		}
		return ps[i].Msg < ps[j].Msg
	})
	return ps, nil
}

// lintRecordKeys is the unknown-key sweep over the OTHER two machine-written
// file kinds (the task shards are swept in Lint's task pass). The passthrough
// parks unknown keys in repo review shards and meta.json too, and their
// published schemas had to flip to additionalProperties:true along with the
// task shard's — which removed the only thing that ever rejected a typo in
// them. Without this sweep, a fat-fingered key in a repo shard or in meta.json
// would be preserved forever and reported by NOTHING: a detection regression
// hiding inside a data-preservation fix.
func (a *App) lintRecordKeys() ([]core.Problem, error) {
	var ps []core.Problem
	repos, err := a.Store.ListRepos()
	if err != nil {
		return nil, err
	}
	for i := range repos {
		if p, ok := unknownKeyProblem(repos[i].Repo, "repo review shard "+core.RepoRecordPath(repos[i].Repo), repos[i].ExtraKeys()); ok {
			ps = append(ps, p)
		}
	}
	meta, err := a.Store.LoadMeta()
	if err != nil {
		return nil, err
	}
	if p, ok := unknownKeyProblem("meta", "meta.json", meta.ExtraKeys()); ok {
		ps = append(ps, p)
	}
	return ps, nil
}

// lintStoreShape checks {tasks/, epics/} <-> bodies/ 1:1 + shard filename/id
// integrity — all by directory enumeration. Sharding makes a duplicate filename
// impossible; a duplicate id can only appear as two shards carrying the same id
// field, which the fold (Load) surfaces to core.Validate as a "duplicate id".
// It returns the live task and epic id sets and the body id list so the
// body-content scan reuses them instead of re-listing.
func (a *App) lintStoreShape(idx *core.Index, epics []core.Epic) ([]core.Problem, map[string]bool, map[string]bool, []string, error) {
	var ps []core.Problem
	taskFileIDs, err := a.Store.ListTaskIDs()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	hasBody := map[string]bool{}
	for _, id := range bodyIDs {
		hasBody[id] = true
	}
	// A task's identity is its id, so key the body 1:1 check on the folded task
	// ids (idx.Tasks), NOT on the raw shard filenames. A single misnamed shard
	// (filename != the id it carries) then reports exactly once — as the
	// filename-integrity error below — instead of cascading into a phantom
	// "missing body" (for the wrong filename) and a phantom "orphan body" (for
	// the real id whose shard is merely misnamed).
	hasTask := map[string]bool{}
	for _, t := range idx.Tasks {
		hasTask[t.ID] = true
		if !hasBody[t.ID] {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "missing-body", ID: t.ID, Msg: fmt.Sprintf("task has no body file (%s)", core.BodyPath(t.ID))})
		}
	}
	// Epics share bodies/ with the tasks (ids are prefix-disjoint), so the same
	// 1:1 contract holds: every epic owns a body (EpicAdd seeds one, the v6
	// migration renames or seeds one), and an epic's body is nobody's orphan.
	hasEpic := map[string]bool{}
	for i := range epics {
		hasEpic[epics[i].ID] = true
		if !hasBody[epics[i].ID] {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "missing-body", ID: epics[i].ID, Msg: fmt.Sprintf("epic has no body file (%s)", core.BodyPath(epics[i].ID))})
		}
	}
	for _, id := range bodyIDs {
		if !hasTask[id] && !hasEpic[id] {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "orphan-body", ID: id, Msg: fmt.Sprintf("orphan body file %s has no task or epic", core.BodyPath(id))})
		}
	}
	// Shard filename integrity: every shard file's name must equal the id it
	// carries. A folded task's id always came from some shard file, so if a
	// filename is not itself a task id, that shard is misnamed (a hand-edit
	// hazard the monolith couldn't have had, when the id was a field not a name).
	for _, id := range taskFileIDs {
		if !hasTask[id] {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "shard-misnamed", ID: id, Msg: fmt.Sprintf("task shard %s's filename does not match the id it carries", core.TaskPath(id))})
		}
	}
	return ps, hasTask, hasEpic, bodyIDs, nil
}

// lintBodyContent scans every body ONCE for all three content findings —
// conflict markers, dangling [[id]] links, and asset references — then settles
// the asset ledger (orphan / oversized). Keep it one pass: the [[id]] link
// check and the asset-ref check deliberately share the same body read, and the
// "referenced" set the pass accumulates is what the orphan check consumes.
func (a *App) lintBodyContent(hasTask, hasEpic map[string]bool, bodyIDs []string) ([]core.Problem, error) {
	var ps []core.Problem
	// Dangling [[t-x]] / [[e-x]] links (warn): a body's [[id]] reference to an
	// id that exists in neither the hot store nor the archive is a typo or a
	// since-deleted record. It breaks nothing (hence warn), but backlinks
	// (`show --backlinks`) and agents follow these links, so a broken one should
	// surface. Known ids are the hot tasks and epics plus both archive kinds, so
	// a link to an archived task — or to an epic the v6 migration filed in the
	// archive store — is fine.
	known := make(map[string]bool, len(hasTask)+len(hasEpic))
	for id := range hasTask {
		known[id] = true
	}
	for id := range hasEpic {
		known[id] = true
	}
	arcIDs, err := a.archivedIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range arcIDs {
		known[id] = true
	}
	// Asset consistency (warn): the on-disk asset set is needed up front (to spot
	// a body pointing at a missing file) and a "referenced" set is accumulated
	// during the body scan below (to spot an on-disk asset nobody points at). All
	// three asset findings are warn, never error: a broken/oversized/orphan asset
	// never corrupts the store, but a blob committed raw stays in git history
	// forever (history can't be un-committed), so lint is the place to catch it
	// before it lands — never a reason to fail an otherwise-clean board.
	assets, err := a.Store.ListAssets()
	if err != nil {
		return nil, err
	}
	onDisk := make(map[string]bool, len(assets))
	for _, as := range assets {
		onDisk[as.Name] = true
	}
	referenced := map[string]bool{}

	linkRe := core.LinkPattern(a.Cfg.IDPrefix)
	epicLinkRe := core.LinkPattern(a.Cfg.EpicIDPrefix)
	for _, bid := range bodyIDs {
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		// Conflict markers (ERROR): a body carrying git's <<<<<<< / ======= / >>>>>>>
		// is a half-merged progress record — and since the body is where furrow keeps
		// "what's done, what's next", the half that is missing is usually the half
		// someone had just written. It reaches the board when a rebase's autostash
		// re-apply conflicts: git leaves the merged mess in the working tree, exits 0,
		// and the next sync commits it. That is not "suspicious but tolerated" (warn) —
		// it is broken data sitting on the board, so it fails lint. `furrow sync`
		// refuses to commit one in the first place (guardBodyMarkers); this is the
		// backstop for the bodies that got in before the guard existed, or by hand.
		if lines := core.ConflictMarkerLines(body); len(lines) > 0 {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "conflict-marker", ID: bid,
				Msg: fmt.Sprintf("body %s carries git conflict markers on line(s) %s — a half-merged record; resolve them (the other half may still be in `git stash`)",
					core.BodyPath(bid), joinInts(lines))})
		}
		for _, ref := range core.ExtractLinks(body, linkRe) {
			if !known[ref] {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "dangling-link", ID: bid, Msg: fmt.Sprintf("body links to %s via [[%s]] but no such task exists", ref, ref)})
			}
		}
		for _, ref := range core.ExtractLinks(body, epicLinkRe) {
			if !known[ref] {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "dangling-link", ID: bid, Msg: fmt.Sprintf("body links to %s via [[%s]] but no such epic exists", ref, ref)})
			}
		}
		// assets/<name> refs share this one body scan with the [[id]] links. A ref
		// to an absent file dangles; either way the name counts as referenced, so
		// it is not later flagged orphan.
		for _, name := range core.ExtractAssetRefs(body) {
			referenced[name] = true
			if !onDisk[name] {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "asset-missing", ID: bid, Msg: fmt.Sprintf("body references asset %s but %s is missing", core.AssetRef(name), core.AssetPath(name))})
			}
		}
	}

	// orphan (no body references it) + oversized (>= DefaultAssetWarnBytes), keyed
	// to the owning task id when the asset's "<id>-" prefix still names a live task
	// (else the asset basename, so a leftover from a deleted task stays identifiable).
	for _, as := range assets {
		owner := assetOwner(as.Name, hasTask)
		if owner == "" {
			owner = as.Name
		}
		if !referenced[as.Name] {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "orphan-asset", ID: owner, Msg: fmt.Sprintf("asset %s is not referenced by any task body", core.AssetPath(as.Name))})
		}
		if as.Size >= core.DefaultAssetWarnBytes {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "oversized-asset", ID: owner, Msg: fmt.Sprintf("asset %s is %s, over the %s warning threshold — Git-LFS-track it or shrink it", core.AssetPath(as.Name), humanBytes(as.Size), humanBytes(core.DefaultAssetWarnBytes))})
		}
	}
	return ps, nil
}

// lintConfigProblems collects the config-driven findings: the archive-backlog
// nudge, the required-label rule, and every clamp warning (board config,
// [lint].ignore_codes typos, user-level config).
func (a *App) lintConfigProblems(idx *core.Index) []core.Problem {
	var ps []core.Problem
	// archive-backlog nudge ([lint].archive_done, off by default): warn when the
	// pile of archivable done tasks (closed before the archive cutoff) reaches the
	// threshold — a prompt to run `furrow archive` so the hot board stays legible.
	// Visualization only; the pressure valve (archive) already exists.
	if a.Cfg.LintArchiveDone > 0 {
		cutoff := a.Clock.Now().AddDate(0, 0, -a.Cfg.ArchiveOlderThanDays)
		if n := len(Archivable(idx, a.Cfg.DoneLane, cutoff)); n >= a.Cfg.LintArchiveDone {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "archive-backlog", ID: "archive", Msg: fmt.Sprintf("%d done task(s) closed over %dd ago are ready to archive (>= [lint].archive_done=%d) — run `furrow archive --yes`", n, a.Cfg.ArchiveOlderThanDays, a.Cfg.LintArchiveDone)})
		}
	}

	// required-label rule (config [labels].required).
	if a.Cfg.LabelsRequired {
		for _, t := range idx.Tasks {
			if len(t.Labels) == 0 {
				ps = append(ps, core.Problem{Severity: core.SevError, Code: "label-required", ID: t.ID, Msg: "task has no label ([labels].required)"})
			}
		}
	}

	// surface config clamp warnings as lint warns.
	for _, w := range a.Warnings {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "config-clamp", ID: "config", Msg: w})
	}

	// [lint].ignore_codes clamp: config stays core-free, so it cannot tell a typo'd
	// ignore code from a real one — that check lands here, where the code vocabulary
	// lives. An unknown entry suppresses nothing (it matches no real code), so this
	// is the only signal that "ignore_codes = ["reconile-gap"]" is a dead no-op.
	for _, code := range a.Cfg.LintIgnoreCodes {
		if !core.IsLintCode(code) {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "config-clamp", ID: "config",
				Msg: fmt.Sprintf("lint.ignore_codes entry %q is not a known lint code; it suppresses nothing", code)})
		}
	}

	// surface user-level (home) config clamp warnings too. Discovery drops these
	// on its inert path — a half-written ~/.config/furrow/config.toml whose boards
	// all clamp away leaves no board AND no signal — so lint is where they land
	// (running it once is explicit, unlike spamming every command's stderr).
	for _, w := range GlobalConfigWarnings() {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "config-clamp", ID: "global-config", Msg: w})
	}
	return ps
}

// archivedIDs returns the task AND epic ids in the sibling archive store
// (.furrow/archive/), or nil for a store that is not file-backed (an in-memory
// app has no archive on disk). The dangling-link check treats these as known,
// so a [[id]] pointing at an archived task — or at an epic the v6 migration
// filed in the archive store — is not flagged. Construction mirrors Archive's;
// both List*IDs read shard filenames only (no parse), and a missing dir yields
// nil.
func (a *App) archivedIDs() ([]string, error) {
	if a.Dir == "" {
		return nil, nil
	}
	arc := fsstore.New(filepath.Join(a.Dir, "archive"), a.Cfg.Lanes, a.Cfg.IDPrefix, a.Cfg.EpicIDPrefix, a.Cfg.IDWidth)
	ids, err := arc.ListTaskIDs()
	if err != nil {
		return nil, err
	}
	eids, err := arc.ListEpicIDs()
	if err != nil {
		return nil, err
	}
	return append(ids, eids...), nil
}

// assetOwner returns the task id that owns asset name — the id in taskIDs for
// which name is "<id>-…" — or "" when none matches (a leftover asset whose task
// is gone). Frozen ids can never be one another's "<id>-" prefix, so at most one
// id matches; lint uses this to file an orphan/oversized finding under its task.
func assetOwner(name string, taskIDs map[string]bool) string {
	for id := range taskIDs {
		if strings.HasPrefix(name, id+"-") {
			return id
		}
	}
	return ""
}

// unknownKeyProblem is the one wording for the three machine-written file kinds
// (task shard, repo review shard, meta.json), which all park unknown top-level
// keys now (core/passthrough.go). ok=false when the record carried none — the
// normal case — so callers stay a two-line `if`.
//
// furrow PRESERVES a key it does not know rather than silently destroying it on
// the next write. But preserving is not understanding, and silence is not safety,
// so say so. Two causes, both worth seeing:
//
//   - A field a NEWER furrow wrote without bumping the layout version, so no gate
//     fired. This binary carries it faithfully and IGNORES it: the task may be
//     sorted, filtered, or closed as if the field were not there. Update furrow.
//   - A typo in a hand-edited file ("lables"). It is now PERMANENT — nothing ever
//     removes an extra, because auto-deleting a key we don't understand IS the bug
//     the passthrough fixes. CLAUDE.md says never hand-edit these files; this is why.
//
// A warning, never an error: the data is intact, and a board being read by a
// slightly older binary must not red anyone's CI.
func unknownKeyProblem(id, what string, keys []string) (core.Problem, bool) {
	if len(keys) == 0 {
		return core.Problem{}, false
	}
	return core.Problem{Severity: core.SevWarn, Code: "unknown-shard-key", ID: id,
		Msg: fmt.Sprintf("%s carries %d key(s) this furrow does not know (%s) — preserved on write, but IGNORED: update furrow, or fix the hand-edit",
			what, len(keys), strings.Join(keys, ", "))}, true
}

// joinInts renders line numbers for a message: "3, 7, 11". Shared by the
// conflict-marker lint rule and sync's pre-commit guard, so the two ways an
// operator meets the same defect read the same way.
func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// humanBytes renders a byte count as a compact IEC size (B/KiB/MiB/…) for the
// oversized-asset lint message — e.g. 5<<20 -> "5.0 MiB". Presentation only.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// hasBlank reports whether any entry is empty or whitespace-only — the shape
// requireNonBlank refuses at write time and lint flags after the fact.
func hasBlank(vals []string) bool {
	for _, v := range vals {
		if strings.TrimSpace(v) == "" {
			return true
		}
	}
	return false
}

// containsAnyFold reports whether s contains any of the needles as a
// case-insensitive substring (core.ContainsFold — search's matcher, so a
// marker matches exactly the way a `furrow search` for it would).
func containsAnyFold(s string, needles []string) bool {
	for _, n := range needles {
		if core.ContainsFold(s, n) {
			return true
		}
	}
	return false
}
