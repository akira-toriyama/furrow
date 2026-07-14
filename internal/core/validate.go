package core

import (
	"fmt"
	"regexp"
	"sort"
)

// Problem is one lint finding. Severity is "error" (breaks an invariant) or
// "warn" (suspicious but tolerated). The CLI exits non-zero only on errors.
//
// Code is a stable kebab-case classifier for machine triage — the id field is
// contextual (a task id, an asset name, or a literal like "config"/"global-config"),
// so an agent branches on Code, never regexes the message prose. The closed
// vocabulary: empty-id, id-pattern, duplicate-id, unknown-lane, body-path,
// parent-missing, dep-missing, repo-shape, value-range, effort-range, dep-cycle,
// reconcile-gap, done-unclosed, missing-body, orphan-body, shard-misnamed,
// conflict-marker, dangling-link, asset-missing, orphan-asset, oversized-asset,
// label-required, config-clamp, alias-shadow, archive-backlog.
type Problem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	ID       string `json:"id"`
	Msg      string `json:"message"`
}

const (
	SevError = "error"
	SevWarn  = "warn"
)

// repoShapeRe is the owner/repo shape for a Task.Repos entry: a GitHub-style
// owner (alphanumeric and inner hyphens) + "/" + a repo name (word chars, dots,
// hyphens). Kept permissive on purpose — it guards against obvious typos (bare
// names, URLs, spaces), not against every invalid GitHub name.
var repoShapeRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?/[A-Za-z0-9._-]+$`)

// Validate runs the in-memory consistency rules that need only the Index plus
// the configured lane order and id pattern. The filesystem-level check (the
// index<->body 1:1 mapping) lives in the app layer, which has the store; it
// appends its findings to these.
//
// Rules:
//   - id must be non-empty and match idPattern (frozen-id shape)
//   - ids must be unique
//   - status must be a known lane (else warn — the task still works, sorts last)
//   - body path must equal the canonical bodies/<id>.md
//   - every dep / parent must reference an existing id
func Validate(idx *Index, laneOrder []string, idPattern *regexp.Regexp) []Problem {
	var out []Problem
	known := laneRank(laneOrder)
	seen := map[string]int{}
	ids := map[string]bool{}
	for _, t := range idx.Tasks {
		ids[t.ID] = true
	}

	for _, t := range idx.Tasks {
		switch {
		case t.ID == "":
			out = append(out, Problem{SevError, "empty-id", t.ID, "task has an empty id"})
		case idPattern != nil && !idPattern.MatchString(t.ID):
			out = append(out, Problem{SevError, "id-pattern", t.ID, fmt.Sprintf("id %q does not match the configured id pattern", t.ID)})
		}

		seen[t.ID]++
		if seen[t.ID] == 2 { // report each duplicate id once
			out = append(out, Problem{SevError, "duplicate-id", t.ID, fmt.Sprintf("duplicate id: %s", t.ID)})
		}

		if _, ok := known[t.Status]; !ok {
			out = append(out, Problem{SevWarn, "unknown-lane", t.ID, fmt.Sprintf("status %q is not a configured lane", t.Status)})
		}

		if want := BodyPath(t.ID); t.Body != want {
			out = append(out, Problem{SevError, "body-path", t.ID, fmt.Sprintf("body path %q should be %q", t.Body, want)})
		}

		if t.Parent != "" && !ids[t.Parent] {
			out = append(out, Problem{SevError, "parent-missing", t.ID, fmt.Sprintf("parent %q does not exist", t.Parent)})
		}
		for _, dep := range t.Deps {
			if !ids[dep] {
				out = append(out, Problem{SevError, "dep-missing", t.ID, fmt.Sprintf("dep %q does not exist", dep)})
			}
		}

		// repos entries are owner/repo identifiers (e.g. akira-toriyama/furrow).
		// A malformed entry still loads and filters — hence warn, not error —
		// but it will never match a derived repo, so surface it.
		for _, r := range t.Repos {
			if !repoShapeRe.MatchString(r) {
				out = append(out, Problem{SevWarn, "repo-shape", t.ID, fmt.Sprintf("repo %q is not owner/repo-shaped", r)})
			}
		}
	}

	// Deterministic order: errors before warns, then by id, then by message —
	// so two runs over the same index print identically.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Severity != out[b].Severity {
			return out[a].Severity < out[b].Severity // "error" < "warn"
		}
		if out[a].ID != out[b].ID {
			return out[a].ID < out[b].ID
		}
		return out[a].Msg < out[b].Msg
	})
	return out
}

// EstimateProblems warns about any value/effort outside the 1..5 scale. It is a
// backstop for hand-edits: the marshaller clamps on every write (Canonicalize),
// so an out-of-range estimate can only reach disk by editing index.json by hand
// — and would be silently rounded on the next write. Run this on the RAW index
// (before Canonicalize) so the stray is still visible. Findings are warnings,
// not errors: the data still loads and the clamp keeps it sane.
func EstimateProblems(idx *Index) []Problem {
	var out []Problem
	for _, t := range idx.Tasks {
		if t.Value != nil && (*t.Value < EstimateMin || *t.Value > EstimateMax) {
			out = append(out, Problem{SevWarn, "value-range", t.ID, fmt.Sprintf("value %d is outside %d..%d; it will be clamped on the next write", *t.Value, EstimateMin, EstimateMax)})
		}
		if t.Effort != nil && (*t.Effort < EstimateMin || *t.Effort > EstimateMax) {
			out = append(out, Problem{SevWarn, "effort-range", t.ID, fmt.Sprintf("effort %d is outside %d..%d; it will be clamped on the next write", *t.Effort, EstimateMin, EstimateMax)})
		}
	}
	return out
}

// HasErrors reports whether any problem is an error (vs. only warnings).
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == SevError {
			return true
		}
	}
	return false
}
