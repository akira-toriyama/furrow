package core

import "sort"

// lintCodes is the authoritative registry of every stable kebab-case `code` that
// `furrow lint` can emit. It is the closed vocabulary two features check against:
//
//   - `lint --code`/`--exclude-code` VALIDATE their tokens against it — an unknown
//     token is exit 2 with these as `candidates` (symmetric with an unknown lane:
//     clamp-don't-reject is a config-file policy, never for an explicit CLI arg).
//   - `[lint].ignore_codes` is CLAMPED against it — an unknown entry is a harmless
//     no-op (it matches nothing) that `furrow lint` warns about, never an error.
//
// Codes are produced across three layers — core (this package: validate.go,
// cycles.go, staledep.go), app (lint.go), and cli (alias-shadow) — so this list,
// NOT the scattered emission sites, is the single source of truth. EVERY new lint
// code MUST be registered here, or `lint --code <new>` would reject it as unknown
// and `ignore_codes = ["<new>"]` would warn about a code that really exists.
// TestLintCodeRegistryCoversEmitted (internal/cli) greps the tree and fails if an
// emitted code is missing here.
var lintCodes = map[string]bool{
	"alias-shadow":         true,
	"archive-backlog":      true,
	"asset-missing":        true,
	"body-path":            true,
	"config-clamp":         true,
	"conflict-marker":      true,
	"dangling-link":        true,
	"dep-cycle":            true,
	"dep-missing":          true,
	"dep-mirrors-children": true,
	"done-unclosed":        true,
	"duplicate-id":         true,
	"effort-range":         true,
	"empty-id":             true,
	"id-pattern":           true,
	"label-required":       true,
	"missing-body":         true,
	"orphan-asset":         true,
	"orphan-body":          true,
	"oversized-asset":      true,
	"parent-cycle":         true,
	"parent-done":          true,
	"parent-missing":       true,
	"reconcile-gap":        true,
	"repo-shape":           true,
	"schema-outdated":      true,
	"shard-misnamed":       true,
	"unknown-lane":         true,
	"unknown-shard-key":    true,
	"unknown-type":         true,
	"value-range":          true,
}

// IsLintCode reports whether code is a known lint code (see lintCodes).
func IsLintCode(code string) bool { return lintCodes[code] }

// LintCodeList returns the known lint codes, sorted — the did-you-mean
// `candidates` array for an unknown `lint --code`/`--exclude-code` token.
func LintCodeList() []string {
	out := make([]string, 0, len(lintCodes))
	for c := range lintCodes {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ProblemFilter narrows a lint problem set. The zero value filters nothing.
//
// It drives BOTH the printed problems AND lint's exit code: a problem removed
// here is treated as if lint never found it, so excluding or ignoring the last
// error makes `furrow lint` exit 0. That is the whole point — a permanently-dead
// check (a `reconcile-gap` that will always fire, say) can be silenced so it stops
// reddening CI, without hand-grepping `--json`.
type ProblemFilter struct {
	// IgnoreCodes drops these codes entirely ([lint].ignore_codes: a check the
	// operator has permanently opted out of). An entry naming no real code matches
	// nothing here — the warning about the typo is raised by app.Lint.
	IgnoreCodes []string
	// Codes is an allow-list: when non-empty, ONLY these codes survive (--code).
	Codes []string
	// ExcludeCodes drops these codes (--exclude-code). It wins over Codes when a
	// code appears in both — an explicit exclusion is the stronger intent.
	ExcludeCodes []string
	// Severity, when non-empty, keeps only problems of exactly this severity
	// ("error" | "warn"): --severity error is the errors-only CI view, --severity
	// warn the warnings-only view. An EXACT match (not a floor), since there are
	// only two levels and "only warns" is the useful reading of the second.
	Severity string
}

// FilterProblems returns the subset of ps that survives f, in the original order.
// Application order per problem: ignore, then allow-list (Codes), then exclude,
// then severity — so ExcludeCodes overrides Codes, and Severity narrows whatever
// the code filters left.
func FilterProblems(ps []Problem, f ProblemFilter) []Problem {
	ignore := setFromSlice(f.IgnoreCodes)
	allow := setFromSlice(f.Codes)
	exclude := setFromSlice(f.ExcludeCodes)
	out := make([]Problem, 0, len(ps))
	for _, p := range ps {
		if ignore[p.Code] {
			continue
		}
		if len(allow) > 0 && !allow[p.Code] {
			continue
		}
		if exclude[p.Code] {
			continue
		}
		if f.Severity != "" && p.Severity != f.Severity {
			continue
		}
		out = append(out, p)
	}
	return out
}

// setFromSlice builds a membership set, or nil for an empty input (so callers can
// cheaply test len()==0 / a nil map reads false for any key).
func setFromSlice(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
