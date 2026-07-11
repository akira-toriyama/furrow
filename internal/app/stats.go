package app

import "sort"

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
	laneCounts := map[string]int{}
	repoCounts := map[string]int{}
	labelCounts := map[string]int{}
	total, drafts := 0, 0
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
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
	return Stats{
		Total:   total,
		Drafts:  drafts,
		ByLane:  laneHistogram(a.Cfg.Lanes, laneCounts),
		ByRepo:  sortedCounts(repoCounts),
		ByLabel: sortedCounts(labelCounts),
	}, nil
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
