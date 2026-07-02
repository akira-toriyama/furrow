package core

import (
	"sort"
	"strings"
)

// IsRepoShaped reports whether s has the owner/repo shape a Task.Repos entry
// must carry (the single shape gate — see repoShapeRe in validate.go; strict
// writers reuse this instead of growing a second, divergent regexp).
func IsRepoShaped(s string) bool { return repoShapeRe.MatchString(s) }

// RepoMatches resolves q against a universe of owner/repo identifiers and
// returns every entry it names: an exact match, or a short-name suffix match
// at a "/" boundary ("furrow" matches "akira-toriyama/furrow" but never
// "org/my-furrow"). Matching is case-insensitive; the returned entries keep
// the universe's canonical casing, sorted and deduped so ambiguity is
// deterministic. An empty q matches nothing. Pure — the caller supplies the
// universe (all tasks' repos plus the board-derived repos).
func RepoMatches(q string, universe []string) []string {
	if q == "" {
		return nil
	}
	lq := strings.ToLower(q)
	seen := map[string]bool{}
	var out []string
	for _, c := range universe {
		lc := strings.ToLower(c)
		if (lc == lq || strings.HasSuffix(lc, "/"+lq)) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
