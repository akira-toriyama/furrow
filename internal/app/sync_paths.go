// The commit side's rules: which of the store's dirty files are furrow's to
// commit unnamed (machineSyncPath), how a dirty set splits into commit /
// pending bodies / foreign files (partitionSync, shared with autocommit), and
// the body-marker guard that refuses to commit a half-merged body.

package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// partitionSync splits the dirty .furrow paths into what the auto-commit should
// stage. Machine-written files — the ALLOWLIST of shapes furrow itself writes,
// defined by machineSyncPath — are always committed: they are deterministic and
// complete by construction. A hand-edited bodies/<id>.md is committed only when
// it is brand-new (an add/retitle seed, still untracked) or explicitly opted in
// (opts.Bodies or opts.AllBodies); an otherwise-modified body is left
// uncommitted and returned in pendingBodies, so a shared checkout never sweeps a
// co-located operator's in-progress prose under the wrong author. Everything
// else — an editor swap file, a backup ~, a crashed atomicWrite's .tmp-* — is
// FOREIGN: never committed (a push cannot be un-published), returned in foreign
// so the caller can disclose the skip instead of silently publishing junk (the
// old rule was "everything that is not a body", which pushed vim swaps).
// commitPaths are the pathspecs to stage; committedBodies/pendingBodies are
// affected ids and foreign is repo-relative paths, all sorted (nil when empty,
// so SyncProgress omits them).
func partitionSync(spec string, changes []gitrepo.Change, opts SyncOpts) (commitPaths, committedBodies, pendingBodies, foreign []string) {
	bodiesPrefix := spec + "/bodies/"
	named := make(map[string]bool, len(opts.Bodies))
	for _, id := range opts.Bodies {
		named[id] = true
	}
	for _, ch := range changes {
		if body, isBody := strings.CutPrefix(ch.Path, bodiesPrefix); isBody && strings.HasSuffix(body, ".md") && !strings.Contains(body, "/") {
			id := strings.TrimSuffix(body, ".md")
			if ch.Untracked || opts.AllBodies || named[id] {
				commitPaths = append(commitPaths, ch.Path)
				committedBodies = append(committedBodies, id)
			} else {
				pendingBodies = append(pendingBodies, id)
			}
			continue
		}
		if machineSyncPath(spec, ch.Path) {
			commitPaths = append(commitPaths, ch.Path)
			continue
		}
		foreign = append(foreign, ch.Path)
	}
	sort.Strings(committedBodies)
	sort.Strings(pendingBodies)
	sort.Strings(foreign)
	return commitPaths, committedBodies, pendingBodies, foreign
}

// machineSyncPath says whether a dirty repo-relative path under the board dir
// (spec) is a file furrow itself writes, and so is always safe to auto-commit.
// The shapes are exactly what fsstore owns: the three shard kinds
// (tasks|epics|repos/*.json), meta.json, config.toml, the board-level git
// dotfiles (.gitattributes is scaffolded by init; a hand-added .gitignore is
// the same board-level class), and attach's bodies/assets/ blobs. The archive/
// store nests the same shapes one level down — plus its bodies/*.md, which only
// `furrow archive` moves, so they are machine-written there. Top-level
// bodies/*.md is deliberately NOT here: that is the hand-editable class
// partitionSync routes through the body opt-in rules.
func machineSyncPath(spec, path string) bool {
	rel, ok := strings.CutPrefix(path, spec+"/")
	if !ok {
		return false
	}
	if arel, isArchive := strings.CutPrefix(rel, "archive/"); isArchive {
		if body, isBody := strings.CutPrefix(arel, "bodies/"); isBody && strings.HasSuffix(body, ".md") && !strings.Contains(body, "/") {
			return true // an archived body moves only by `furrow archive`
		}
		return machineSyncRel(arel)
	}
	return machineSyncRel(rel)
}

// machineSyncRel is machineSyncPath's per-store rule, on a path relative to one
// store root (the board dir or its archive/).
func machineSyncRel(rel string) bool {
	switch rel {
	case "meta.json", "config.toml", ".gitattributes", ".gitignore":
		return true
	}
	dir, file, found := strings.Cut(rel, "/")
	if !found {
		return false
	}
	switch dir {
	case "tasks", "epics", "repos":
		return strings.HasSuffix(file, ".json") && !strings.Contains(file, "/")
	case "bodies":
		// attach's blobs; bodies/*.md is the caller's hand-editable class. A
		// `.tmp-*` under assets/ is a crashed write's staging file, never a
		// blob to publish (t-rns9).
		asset, isAsset := strings.CutPrefix(file, "assets/")
		return isAsset && !strings.Contains(asset, "/") && !strings.HasPrefix(asset, ".tmp-")
	}
	return false
}

// guardBodyMarkers refuses to auto-commit a body that still carries git conflict
// markers. It is the PREVENTION half of the conflict-marker rule; `furrow lint`'s
// conflict-marker finding is the DETECTION half, for a board that already has one.
//
// The two halves are not redundant: a failed autostash re-apply leaves markers in
// the working tree (git merges the stash in and keeps what conflicts), and the very
// next sync would sweep that body into a commit — at which point the corruption is
// on every other machine and in git history, which cannot be un-published. Lint
// tells you afterwards; this refuses beforehand.
//
// Validation (exit 2, do-not-retry): the fix is an edit, not a re-run.
func (a *App) guardBodyMarkers(ids []string) error {
	type markedBody struct {
		ID    string `json:"id"`
		Path  string `json:"path"`
		Lines []int  `json:"lines"`
	}
	bad := []markedBody{}
	for _, id := range ids {
		body, err := a.Store.LoadBody(id)
		if err != nil {
			return err
		}
		if lines := core.ConflictMarkerLines(body); len(lines) > 0 {
			bad = append(bad, markedBody{ID: id, Path: core.BodyPath(id), Lines: lines})
		}
	}
	if len(bad) == 0 {
		return nil
	}
	named := make([]string, len(bad))
	for i, b := range bad {
		named[i] = fmt.Sprintf("%s (line %s)", b.Path, joinInts(b.Lines))
	}
	return &core.Error{
		Code: core.CodeValidation,
		Kind: core.KindBodyConflictMarker,
		Msg: fmt.Sprintf("refusing to commit %d body file(s) still carrying git conflict markers: %s — "+
			"a half-merged body is half a progress record, and a commit cannot be un-published. Resolve the "+
			"markers (the other half is often still in `git stash`), then re-run furrow sync",
			len(bad), strings.Join(named, ", ")),
		Details: map[string]any{"bodies": bad},
	}
}
