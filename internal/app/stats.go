package app

import (
	"sort"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// StatCount is one (key, count) row of a distribution — a lane, a repo, or a
// label and how many scoped tasks carry it.
type StatCount struct {
	Key   string
	Count int
}

// Stats is the board's shape within a query scope: the total task count, how
// many are drafts (repo-less), and the distribution across lanes, repos, and
// labels. ByLane is in configured lane order (every lane, 0 included, so it is a
// complete histogram); ByRepo and ByLabel are the used vocabulary with counts,
// most-used first. It answers "what lanes/labels/repos exist and how big" before
// an agent guesses a -s/-l/-r value.
type Stats struct {
	Total   int
	Drafts  int
	ByLane  []StatCount
	ByRepo  []StatCount
	ByLabel []StatCount
	// Window is the activity FLOW within --since/--until (nil without a
	// window): which scoped tasks were CREATED and which were CLOSED there,
	// ids included so every count is verifiable one `furrow show` away.
	Window *StatsWindow
}

// StatsWindow reports the board's flow inside a time window. It is the machine
// side of the session budget check (created ≤ closed − 1, the Stop hook's
// arithmetic): the close declares counts in prose, this read supplies the
// board's actuals to compare against. Created holds the ids whose `created`
// falls in [Since, Until] (same inclusive bounds as the `ls` window), Closed
// those whose `closed` does; a task created AND closed inside the window is in
// both. The scan deliberately ignores the UPDATED-window filter the same flags
// apply to the distributions — a task closed inside the window and touched
// after it must still count as closed — and it unions the archive store, so an
// archive sweep inside the window cannot deflate Closed.
type StatsWindow struct {
	Since   *time.Time
	Until   *time.Time
	Created []string
	Closed  []string
}

// Stats aggregates the tasks passing the query's scope (the same -s/-l/-r
// semantics as List) into lane/repo/label distributions. It respects the board
// scope, so a bare `stats` describes this repo's slice; `-r ”` describes the
// whole board (the vocabulary-discovery call). A -s naming an unknown lane fails
// fast (validateLaneFilter), symmetric with List/Search.
func (a *App) Stats(o QueryOpts) (Stats, error) {
	if err := a.validateLaneFilter(o.Status); err != nil {
		return Stats{}, err
	}
	idx, err := a.load()
	if err != nil {
		return Stats{}, err
	}
	// Compile -q once; the distributions then describe the QUERIED slice, the
	// same AND semantics as List (`stats -q is:stale` = the stale board's shape).
	qpred, err := a.queryPred(o.Query, idx, a.Cfg.RevisitStaleDays, nil)
	if err != nil {
		return Stats{}, err
	}
	laneCounts := map[string]int{}
	repoCounts := map[string]int{}
	labelCounts := map[string]int{}
	total, drafts := 0, 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		if qpred != nil {
			ok, err := qpred(t)
			if err != nil {
				return Stats{}, err
			}
			if !ok {
				continue
			}
		}
		total++
		if len(t.Repos) == 0 {
			drafts++
		}
		laneCounts[t.Status]++
		for _, r := range t.Repos {
			repoCounts[r]++
		}
		for _, l := range t.Labels {
			labelCounts[l]++
		}
	}
	s := Stats{
		Total:   total,
		Drafts:  drafts,
		ByLane:  laneHistogram(a.Cfg.Lanes, laneCounts),
		ByRepo:  sortedCounts(repoCounts),
		ByLabel: sortedCounts(labelCounts),
	}
	if o.Since != nil || o.Until != nil {
		w, err := a.statsWindow(o, idx)
		if err != nil {
			return Stats{}, err
		}
		s.Window = w
	}
	return s, nil
}

// statsWindow computes the created/closed flow for Stats.Window. The scope
// filters (-s/-l/-r/-e/-q) apply, but the updated-window itself is stripped —
// membership is decided by `created`/`closed` alone (see StatsWindow). Hot
// tasks are scanned from the already-loaded index; the archive store is
// unioned when one can exist (file-backed), with the query recompiled against
// the archive's own index and body loader so `-q` terms read the right bodies.
// Ids never collide across the two stores (archive moves a shard, it does not
// copy it).
func (a *App) statsWindow(o QueryOpts, idx *core.Index) (*StatsWindow, error) {
	scope := o
	scope.Since, scope.Until = nil, nil

	w := &StatsWindow{Since: o.Since, Until: o.Until, Created: []string{}, Closed: []string{}}
	type stamp struct {
		id string
		at time.Time
	}
	var created, closed []stamp

	scan := func(src *core.Index, loadBody func(string) (string, error)) error {
		qpred, err := a.queryPred(scope.Query, src, a.Cfg.RevisitStaleDays, loadBody)
		if err != nil {
			return err
		}
		for i := range src.Tasks {
			t := &src.Tasks[i]
			if !scope.match(t) {
				continue
			}
			if qpred != nil {
				ok, err := qpred(t)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
			}
			if inWindow(t.Created, o.Since, o.Until) {
				created = append(created, stamp{t.ID, t.Created})
			}
			if t.Closed != nil && inWindow(*t.Closed, o.Since, o.Until) {
				closed = append(closed, stamp{t.ID, *t.Closed})
			}
		}
		return nil
	}

	if err := scan(idx, nil); err != nil {
		return nil, err
	}
	if a.Dir != "" { // a non-file-backed store cannot have an archive
		arc, err := a.archiveStore()
		if err != nil {
			return nil, err
		}
		arcIdx, err := arc.Load() // a missing archive dir loads empty
		if err != nil {
			return nil, err
		}
		core.Canonicalize(arcIdx, a.Cfg.Lanes)
		if err := scan(arcIdx, arc.LoadBody); err != nil {
			return nil, err
		}
	}

	chrono := func(ss []stamp) []string {
		sort.Slice(ss, func(i, j int) bool {
			if !ss[i].at.Equal(ss[j].at) {
				return ss[i].at.Before(ss[j].at)
			}
			return ss[i].id < ss[j].id
		})
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.id
		}
		return out
	}
	w.Created = chrono(created)
	w.Closed = chrono(closed)
	return w, nil
}

// inWindow reports whether ts falls inside the inclusive [since, until]
// bounds; a nil bound is unbounded on that side (matching QueryOpts.match's
// updated-window semantics, so the two windows never disagree on an edge).
func inWindow(ts time.Time, since, until *time.Time) bool {
	if since != nil && ts.Before(*since) {
		return false
	}
	if until != nil && ts.After(*until) {
		return false
	}
	return true
}

// laneHistogram emits the configured lanes in order (each with its count, 0
// included) followed by any off-vocabulary status still present (sorted), so the
// rows are a stable, complete histogram whose counts sum to Total even if a task
// carries a status no longer in the config.
func laneHistogram(lanes []string, counts map[string]int) []StatCount {
	out := make([]StatCount, 0, len(counts))
	seen := map[string]bool{}
	for _, lane := range lanes {
		out = append(out, StatCount{Key: lane, Count: counts[lane]})
		seen[lane] = true
	}
	extra := []string{}
	for k := range counts {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, StatCount{Key: k, Count: counts[k]})
	}
	return out
}

// sortedCounts turns a count map into rows ordered by count (descending) then
// key (ascending) — the used vocabulary, most-used first, deterministic on ties.
func sortedCounts(m map[string]int) []StatCount {
	out := make([]StatCount, 0, len(m))
	for k, v := range m {
		out = append(out, StatCount{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}
