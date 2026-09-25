package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newLintCmd() *cobra.Command {
	var (
		codes    []string
		exclude  []string
		severity string
	)
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check index<->body consistency, lanes, deps, links, assets, and config",
		Long: "Validate the store: id shape and uniqueness, status lanes, body path, the\n" +
			"index<->body 1:1 mapping, dep references and dependency cycles — dep-cycle\n" +
			"(error), a dated task waiting on a dependency promised later than its own\n" +
			"due (due-inversion, warn), the epic linkage — an open task filed under no\n" +
			"box once the board has any (epic-required, error), under a missing box\n" +
			"(epic-missing, error),\n" +
			"or under a closed one (epic-closed, warn: the box closed with work left\n" +
			"under it), and cycles in the epic dep graph (epic-dep-cycle, error),\n" +
			"git conflict markers left in a body — conflict-marker, a half-merged progress\n" +
			"record (error; `furrow sync` refuses to commit one, this catches the ones\n" +
			"already on the board), dangling [[id]] body links (warn), reconcile gaps — an open task whose done\n" +
			"dependency closed after its last update (warn), asset hygiene — a body's\n" +
			"asset reference whose file is missing (asset-missing), orphan and\n" +
			"oversized assets (warn; a task's `refs` entries are verbatim pointers —\n" +
			"file:line or URL — and are NEVER checked for existence), an outdated board layout —\n" +
			"schema-outdated, i.e. writes are refused until `furrow upgrade` runs (warn,\n" +
			"never an error: a read-only board is the legitimate middle of a flag day), and\n" +
			"config clamp warnings.\n" +
			"Exits 2 if any errors are found; warnings alone exit 0. --json prints the\n" +
			"findings as ONE top-level array of {severity, code, id, message} on stdout\n" +
			"(empty when the board is clean); with an error among them the exit-2\n" +
			"envelope goes to stderr beside it — read stdout for the findings and the\n" +
			"exit code for the verdict.\n\n" +
			"Every problem carries a stable kebab-case `code` — branch on that, never on the\n" +
			"message (the `id` field is contextual: a task id, an asset name, an\n" +
			"`owner/repo`, `meta`, `alias`, `archive`, `config`, or `global-config`).\n\n" +
			"Narrow the output with --code (allow-list), --exclude-code (deny-list; wins\n" +
			"over --code), and --severity error|warn (exact level). An unknown --code /\n" +
			"--exclude-code token is exit 2 with the known codes as candidates (a closed\n" +
			"vocabulary, like a lane). Config's [lint].ignore_codes suppresses codes on\n" +
			"every run (an unknown entry there only warns — clamp-don't-reject), and the\n" +
			"[lint.severity] table re-levels one per board (`due-overdue = \"warn\"` where\n" +
			"no CI consumes the red; promoting a warn to error also works) — every\n" +
			"consumer, this exit code included, sees the effective level. THE FILTER\n" +
			"DRIVES THE EXIT CODE: a problem filtered out is treated as if lint never found\n" +
			"it, so excluding or ignoring the last error exits 0 (the point — silence a\n" +
			"permanently-dead check so it stops reddening CI), and --severity warn always\n" +
			"exits 0 (errors, if any, are hidden by the filter).",
		Example: "  furrow lint\n" +
			"  furrow lint --severity error         # errors only (the CI gate)\n" +
			"  furrow lint --exclude-code reconcile-gap,epic-no-active\n" +
			"  furrow lint --code dangling-link --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// Validate the flag vocabulary up front: an explicit CLI arg is
			// exit-2-with-candidates on a typo (symmetric with an unknown lane),
			// never a silent empty result. [lint].ignore_codes stays lenient (config
			// policy) — app.Lint warns about an unknown entry there instead.
			codes = splitCSV(codes)
			exclude = splitCSV(exclude)
			if err := validateLintCodes(codes); err != nil {
				return err
			}
			if err := validateLintCodes(exclude); err != nil {
				return err
			}
			if severity != "" && severity != core.SevError && severity != core.SevWarn {
				return &core.Error{
					Code:       core.CodeValidation,
					Kind:       core.KindValidation,
					Msg:        fmt.Sprintf("unknown severity %q (valid: %s, %s)", severity, core.SevError, core.SevWarn),
					Candidates: []string{core.SevError, core.SevWarn},
				}
			}

			// A board [alias] that shadows a builtin is inert; the CLI owns the
			// command set, so it raises that finding and hands it to the app to
			// level, filter and sort with the rest. The filter drives BOTH the
			// printout AND the exit code — a problem removed by it is as if lint
			// never found it (see the Long help).
			ps, hasErrors, err := a.LintFiltered(app.LintFilter{Codes: codes, ExcludeCodes: exclude, Severity: severity},
				aliasShadowProblems(cmd.Root(), a.Cfg.Alias)...)
			if err != nil {
				return err
			}

			// A problem stream is list-shaped: emitList's contract (one per
			// line, [] never null, the human table otherwise).
			emitList(ps, func() {
				if len(ps) == 0 {
					fmt.Fprintln(out, "ok — no problems")
				}
				for _, p := range ps {
					fmt.Fprintf(out, "%-5s  %-16s  %-8s  %s\n", p.Severity, p.Code, p.ID, p.Msg)
				}
			})
			if hasErrors {
				// Errors make lint fail (validation), but we already printed the
				// findings, so return a quiet error that only sets the exit code.
				return &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: "lint found errors"}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&codes, "code", nil, "show only these lint codes (OR; comma-separated or repeated); unknown = exit 2 + candidates")
	cmd.Flags().StringArrayVar(&exclude, "exclude-code", nil, "hide these lint codes (OR; comma-separated or repeated; wins over --code); unknown = exit 2 + candidates")
	cmd.Flags().StringVar(&severity, "severity", "", "show only this severity (error|warn); note --severity warn hides errors and so exits 0")
	return cmd
}

// splitCSV flattens a repeatable comma-OR flag (--code, --exclude-code) into a
// trimmed, empty-dropped slice — the same "-s a,b == -s a -s b" union the lane
// filter uses, kept here rather than in the app so the code vocabulary check
// (validateLintCodes) can run on a clean token list before the store is even read.
func splitCSV(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.TrimSpace(tok); tok != "" {
				out = append(out, tok)
			}
		}
	}
	return out
}

// validateLintCodes rejects the first token that is not a known lint code, with
// the full vocabulary in Candidates — the did-you-mean guard a closed vocabulary
// gets (symmetric with an unknown lane), so a typo'd --code is a loud exit 2, not
// a silent empty listing.
func validateLintCodes(codes []string) error {
	for _, c := range codes {
		if !core.IsLintCode(c) {
			return &core.Error{
				Code:       core.CodeValidation,
				Kind:       core.KindValidation,
				Msg:        fmt.Sprintf("unknown lint code %q (see `furrow lint` for the vocabulary)", c),
				Candidates: core.LintCodeList(),
			}
		}
	}
	return nil
}
