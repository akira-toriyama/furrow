// The pull's conflict resolver: a rebase that stopped on machine-written
// shards is finished by merging each at the field grain (core.MergeShard)
// and continuing, commit by commit; anything else that conflicts — a body,
// meta.json, a shard both sides changed in the same field — leaves the rebase
// to the abort-and-report path. Nothing here touches the store's files
// directly: the merged bytes go down through fsstore's atomic write.

package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/akira-toriyama/furrow/v9/internal/gitrepo"
	"github.com/akira-toriyama/furrow/v9/internal/store/fsstore"
)

// shardKindOf classifies a repo-relative path under the board dir (spec) as a
// mergeable shard — tasks/, epics/ and repos/ *.json, in the hot store or its
// archive/ — returning its kind. meta.json is deliberately not one: its
// schema_version is the flag-day gate, and two sides disagreeing about it is
// never a field to merge. Bodies have git's union driver.
func shardKindOf(spec, path string) (core.ShardKind, bool) {
	rel, ok := strings.CutPrefix(path, spec+"/")
	if !ok {
		return 0, false
	}
	rel = strings.TrimPrefix(rel, "archive/")
	dir, file, found := strings.Cut(rel, "/")
	if !found || !strings.HasSuffix(file, ".json") || strings.Contains(file, "/") {
		return 0, false
	}
	switch dir {
	case "tasks":
		return core.ShardTask, true
	case "epics":
		return core.ShardEpic, true
	case "repos":
		return core.ShardRepo, true
	}
	return 0, false
}

// shardConflict is one conflicted path the resolver could not merge: a
// non-shard, a shard a side deleted, or a shard with Fields both sides changed
// differently (the keys, sorted; empty for the first two).
type shardConflict struct {
	Path   string
	Fields []string
}

// resolveShardConflicts drives a rebase that PullRebase left stopped on
// conflicts to completion, one stop at a time: every conflicted path must be a
// shard whose two sides merge cleanly, or the whole attempt is given up (the
// caller aborts the rebase, restoring the pre-pull board — a merge is
// all-or-nothing per sync, never a half-resolved stop left for a human).
//
// stop is the error the rebase stopped with: when the FIRST stop turns out not
// to be a conflict (a hook refused the very first pick), it is what the caller
// gets back, unchanged — the honest report is git's own.
//
// Returns the paths it merged (sorted, deduplicated across stops) and, when it
// gave up, the paths it could not merge; err is a git failure that stopped the
// rebase for a reason other than a conflict (a hook refusing the commit), or
// the cancellation of ctx — either way the caller's cue to abort. On a nil err
// with no unresolved paths the rebase has finished and the working tree is the
// rebased board.
func (a *App) resolveShardConflicts(ctx context.Context, r *gitrepo.Repo, spec string, stop error) (merged []string, unresolved []shardConflict, err error) {
	seen := map[string]bool{}
	// Every git probe here fails open under a cancelled ctx (RebaseInProgress
	// reads false, so the loop would end as if the rebase had finished), which
	// would make a half-rebased board look done; name the cancellation
	// instead, before each probe, so the caller aborts.
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	for first := true; r.RebaseInProgress(ctx); first = false {
		if ctx.Err() != nil {
			return merged, nil, ctx.Err()
		}
		paths := r.ConflictedPaths(ctx)
		if len(paths) == 0 {
			if first {
				return merged, nil, stop
			}
			// Stopped on a later pick, but not on a conflict (a `break`/`edit`
			// instruction, an interrupted continue): not ours to finish.
			return merged, nil, core.Internalf("", "the rebase stopped without conflicted paths")
		}
		type resolution struct {
			path string
			data []byte
		}
		var resolved []resolution
		for _, p := range paths {
			kind, ok := shardKindOf(spec, p)
			if !ok {
				unresolved = append(unresolved, shardConflict{Path: p})
				continue
			}
			base, ours, theirs := r.IndexStages(ctx, p)
			if len(ours) == 0 || len(theirs) == 0 { // deleted (or emptied) on one side, changed on the other
				unresolved = append(unresolved, shardConflict{Path: p})
				continue
			}
			data, fields, merr := core.MergeShard(kind, base, ours, theirs)
			if merr == nil && len(fields) > 0 {
				// theirs is ONE replayed local commit, not the local branch:
				// when the branch's tip (ORIG_HEAD, the pre-rebase head) already
				// agrees with upstream on every disputed field — the operator
				// followed the remedy and set it to the other side's value —
				// the dispute is settled in its favour and the pick merges
				// (t-kge6: without this, the aligning commit was never reached
				// and the sync wedged for good).
				if tip := r.FileAt(ctx, "ORIG_HEAD", p); len(tip) > 0 {
					if settledTheirs, settled, serr := core.SettleFromTip(ours, theirs, tip, fields); serr == nil && settled {
						data, fields, merr = core.MergeShard(kind, base, ours, settledTheirs)
					}
				}
			}
			if merr != nil || len(fields) > 0 {
				unresolved = append(unresolved, shardConflict{Path: p, Fields: fields})
				continue
			}
			resolved = append(resolved, resolution{p, data})
		}
		if len(unresolved) > 0 {
			return merged, unresolved, nil
		}
		for _, res := range resolved {
			if err := fsstore.WriteFileAtomic(filepath.Join(r.Toplevel(), filepath.FromSlash(res.path)), res.data); err != nil {
				return merged, nil, err
			}
			if !seen[res.path] {
				seen[res.path] = true
				merged = append(merged, res.path)
			}
		}
		if err := r.Stage(ctx, paths...); err != nil {
			return merged, nil, err
		}
		var step error
		if r.IndexHasChanges(ctx) {
			step = r.ContinueRebase(ctx)
		} else {
			step = r.SkipRebase(ctx) // the upstream already carried every change
		}
		if step != nil {
			// The next replayed commit conflicting is the loop's normal case;
			// a stop with nothing conflicted is a failure to go on.
			if !r.RebaseInProgress(ctx) || len(r.ConflictedPaths(ctx)) == 0 {
				return merged, nil, step
			}
		}
	}
	sort.Strings(merged)
	return merged, nil, nil
}

// conflictPaths lists the unresolved conflicts' paths, sorted — the
// sync-conflict envelope's `details.paths`.
func conflictPaths(cs []shardConflict) []string {
	paths := make([]string, 0, len(cs))
	for _, c := range cs {
		paths = append(paths, c.Path)
	}
	sort.Strings(paths)
	return paths
}

// conflictFields maps each unresolved shard to the fields both sides changed
// differently — the envelope's `details.fields`, present only when some shard
// was refused for that reason (a body or a deleted shard has no field to name).
func conflictFields(cs []shardConflict) map[string][]string {
	var out map[string][]string
	for _, c := range cs {
		if len(c.Fields) == 0 {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[c.Path] = c.Fields
	}
	return out
}
