package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// Lint runs the full consistency check: core's in-memory rules plus the
// filesystem-level index<->body 1:1 mapping (every task has a body file and
// vice versa). Config clamp warnings are surfaced too, so `furrow lint` is the
// one place that tells you everything that is off.
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

	// Reconcile gaps (warn): a non-terminal task whose done dependency closed
	// after the task was last touched. This is the structural backstop for
	// reconcile-on-close — the always-on (hook/CI) twin of the `dep_done` revisit
	// signal, so an epic whose slice shipped never silently keeps a stale body.
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
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
	ps = append(ps, core.StaleDepProblems(idx, a.Cfg.Terminal, doneIDs)...)

	// tasks/ <-> bodies/ 1:1 + shard filename/id integrity — all by directory
	// enumeration. Sharding makes a duplicate filename impossible; a duplicate id
	// can only appear as two shards carrying the same id field, which the fold
	// (Load) surfaces to core.Validate above as a "duplicate id".
	taskFileIDs, err := a.Store.ListTaskIDs()
	if err != nil {
		return nil, err
	}
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
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
	for _, id := range bodyIDs {
		if !hasTask[id] {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "orphan-body", ID: id, Msg: fmt.Sprintf("orphan body file %s has no task", core.BodyPath(id))})
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

	// Dangling [[t-x]] links (warn): a body's [[id]] reference to an id that
	// exists in neither the hot store nor the archive is a typo or a since-deleted
	// task. It breaks nothing (hence warn), but backlinks (`show --backlinks`) and
	// agents follow these links, so a broken one should surface. Known ids are the
	// hot tasks (hasTask) plus the archive, so a link to an archived task is fine.
	known := make(map[string]bool, len(hasTask))
	for id := range hasTask {
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
	for _, bid := range bodyIDs {
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		for _, ref := range core.ExtractLinks(body, linkRe) {
			if !known[ref] {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "dangling-link", ID: bid, Msg: fmt.Sprintf("body links to %s via [[%s]] but no such task exists", ref, ref)})
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

	// surface user-level (home) config clamp warnings too. Discovery drops these
	// on its inert path — a half-written ~/.config/furrow/config.toml whose boards
	// all clamp away leaves no board AND no signal — so lint is where they land
	// (running it once is explicit, unlike spamming every command's stderr).
	for _, w := range GlobalConfigWarnings() {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "config-clamp", ID: "global-config", Msg: w})
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

// archivedIDs returns the ids in the sibling archive store (.furrow/archive/),
// or nil for a store that is not file-backed (an in-memory app has no archive on
// disk). The dangling-link check treats these as known, so a [[id]] pointing at
// an archived task is not flagged. Construction mirrors Archive's; ListTaskIDs
// reads shard filenames only (no parse), and a missing archive dir yields nil.
func (a *App) archivedIDs() ([]string, error) {
	if a.Dir == "" {
		return nil, nil
	}
	arc := fsstore.New(filepath.Join(a.Dir, "archive"), a.Cfg.Lanes, a.Cfg.IDPrefix, a.Cfg.IDWidth)
	return arc.ListTaskIDs()
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
