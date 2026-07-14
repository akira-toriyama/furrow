package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newLintCmd() *cobra.Command {
	return &cobra.Command{
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
			"`config`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			ps, err := a.Lint()
			if err != nil {
				return err
			}
			// A board [alias] that shadows a builtin is inert; surface it here (the
			// CLI owns the command set, so this warning can't live in app.Lint).
			ps = append(ps, aliasShadowProblems(a.Cfg.Alias)...)
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
}
