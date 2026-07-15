package cli

import (
	"fmt"
	"strings"

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
			"index<->body 1:1 mapping, dep/parent references, dependency and hierarchy\n" +
			"cycles — dep-cycle / parent-cycle (both error; a parent cycle has no root, so\n" +
			"every task in it belongs to no tree), an open task still under a done parent\n" +
			"— parent-done (warn: the epic closed with work left under it),\n" +
			"git conflict markers left in a body — conflict-marker, a half-merged progress\n" +
			"record (error; `furrow sync` refuses to commit one, this catches the ones\n" +
			"already on the board), dangling [[id]] body links (warn), reconcile gaps — an open task whose done\n" +
			"dependency closed after its last update (warn), asset hygiene — dangling\n" +
			"refs, orphan and oversized assets (warn), an outdated board layout —\n" +
			"schema-outdated, i.e. writes are refused until `furrow upgrade` runs (warn,\n" +
			"never an error: a read-only board is the legitimate middle of a flag day), and\n" +
			"config clamp warnings.\n" +
			"Exits 2 if any errors are found; warnings alone exit 0.\n\n" +
			"Every problem carries a stable kebab-case `code` — branch on that, never on the\n" +
			"message (the `id` field is contextual: a task id, an asset name, `meta`, or\n" +
			"`config`).\n\n" +
			"Narrow the output with --code (allow-list), --exclude-code (deny-list; wins\n" +
			"over --code), and --severity error|warn (exact level). An unknown --code /\n" +
			"--exclude-code token is exit 2 with the known codes as candidates (a closed\n" +
			"vocabulary, like a lane). Config's [lint].ignore_codes suppresses codes on\n" +
			"every run (an unknown entry there only warns — clamp-don't-reject). THE FILTER\n" +
			"DRIVES THE EXIT CODE: a problem filtered out is treated as if lint never found\n" +
			"it, so excluding or ignoring the last error exits 0 (the point — silence a\n" +
			"permanently-dead check so it stops reddening CI), and --severity warn always\n" +
			"exits 0 (errors, if any, are hidden by the filter).",
		Example: "  furrow lint\n" +
			"  furrow lint --severity error         # errors only (the CI gate)\n" +
			"  furrow lint --exclude-code reconcile-gap,dep-mirrors-children\n" +
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
					Msg:        fmt.Sprintf("unknown severity %q (valid: %s, %s)", severity, core.SevError, core.SevWarn),
					Candidates: []string{core.SevError, core.SevWarn},
				}
			}

			ps, err := a.Lint()
			if err != nil {
				return err
			}
			// A board [alias] that shadows a builtin is inert; surface it here (the
			// CLI owns the command set, so this warning can't live in app.Lint).
			ps = append(ps, aliasShadowProblems(a.Cfg.Alias)...)

			// Filter drives BOTH the printout AND the exit code below — a problem
			// removed here is as if lint never found it (see the Long help).
			ps = core.FilterProblems(ps, core.ProblemFilter{
				IgnoreCodes:  a.Cfg.LintIgnoreCodes,
				Codes:        codes,
				ExcludeCodes: exclude,
				Severity:     severity,
			})

			switch {
			case flagNDJSON:
				// A problem stream is list-shaped, so --ndjson is one problem per
				// compact line (an empty store simply emits nothing).
				for _, p := range ps {
					printNDJSONValue(p)
				}
			case flagJSON:
				if ps == nil {
					ps = []core.Problem{}
				}
				printJSON(ps)
			default:
				if len(ps) == 0 {
					fmt.Fprintln(out, "ok — no problems")
				}
				for _, p := range ps {
					fmt.Fprintf(out, "%-5s  %-16s  %-8s  %s\n", p.Severity, p.Code, p.ID, p.Msg)
				}
			}
			if core.HasErrors(ps) {
				// Errors make lint fail (validation), but we already printed the
				// findings, so return a quiet error that only sets the exit code.
				return &core.Error{Code: core.CodeValidation, Msg: "lint found errors"}
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
				Msg:        fmt.Sprintf("unknown lint code %q (see `furrow lint` for the vocabulary)", c),
				Candidates: core.LintCodeList(),
			}
		}
	}
	return nil
}
