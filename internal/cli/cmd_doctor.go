package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newDoctorCmd is the machine-wide board-setup health check — `furrow boards`'
// opinionated sibling. `boards` lists what is configured; `doctor` says what is
// WRONG with it and how to fix it, which is the diagnosis a machine with furrow
// installed but no [[board]] scope could never produce on its own (every use
// was a bare exit 2 naming nothing — the 2026-07-16 hole).
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [dir...]",
		Short: "Diagnose this machine's board setup: config, boards, scopes, git freshness",
		Long: "Check the machine's board setup end to end, read-only and network-free (it\n" +
			"never fetches), independent of cwd like `furrow boards`:\n\n" +
			"  - the user-level config parses, and at least one usable [[board]] exists\n" +
			"    (none = the classic half-set-up machine: furrow and even the board are\n" +
			"    installed, but every checkout resolves to nothing — exit 2 at the moment\n" +
			"    of use, naming nothing; doctor names it and hands over the fix)\n" +
			"  - every board is on disk, readable, and on this binary's schema\n" +
			"    (schema-outdated -> `furrow upgrade`; schema-too-new -> update furrow)\n" +
			"  - every scope directory exists\n" +
			"  - a git-backed board's freshness vs its upstream, from LOCAL knowledge\n" +
			"    only (as of the last fetch): behind warns \"sync before reading\", ahead\n" +
			"    warns \"unpushed writes\", an in-progress rebase/merge warns too\n" +
			"  - where discovery would NOT pick the board inside its own scopes (a local\n" +
			"    .furrow or pointer wins — info, never unhealthy: opting out is legitimate)\n" +
			"  - discovery is simulated at cwd (informational) and at every dir given as\n" +
			"    an argument (an assertion: a dir that resolves to no board is an error\n" +
			"    with the fix — add it to a board's scopes)\n\n" +
			"Every finding carries a stable kebab-case `code` — branch on it, never on the\n" +
			"message (the `id` field is contextual: a store path, a scope path, a dir, an\n" +
			"env var name, or `config`). Severity `info` is a fact worth seeing, not a\n" +
			"problem to fix.\n\n" +
			"Exit: 0 = healthy (info-only findings included) / 1 = problems found (any\n" +
			"error or warn; id `doctor-unhealthy`) — so it can sit in shell init or CI.\n" +
			"--json emits one report object {config, env_furrow_dir, env_furrow_board,\n" +
			"boards, resolutions, problems, healthy}; --ndjson emits it as one compact line.",
		Example: "  furrow doctor                       # check the machine, simulate at cwd\n" +
			"  furrow doctor ~/ws/github.com/me    # assert: this dir must resolve to a board\n" +
			"  furrow doctor --json | jq -e '.healthy'   # shell-init / CI pre-flight",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// An asserted dir must exist — a typo'd path is bad usage (exit 2,
			// symmetric with an unknown lane), not a board-setup finding.
			dirs := make([]string, 0, len(args))
			for _, a := range args {
				abs, err := filepath.Abs(a)
				if err != nil {
					return core.Validationf("", "resolve %q: %v", a, err)
				}
				if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
					return core.Validationf("", "%q is not an existing directory (doctor asserts board resolution AT a dir; fix the path)", a)
				}
				dirs = append(dirs, abs)
			}
			cwd, err := os.Getwd()
			if err != nil {
				return core.Internalf("", "getwd: %v", err)
			}
			r, err := app.Doctor(cmd.Context(), cwd, dirs)
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(r)
			} else {
				printDoctorHuman(r)
			}
			if !r.Healthy {
				n := 0
				for _, p := range r.Problems {
					if p.Severity != app.SevInfo {
						n++
					}
				}
				// The findings are already printed; this quiet error only sets
				// the health-check exit code (1) and the stderr envelope.
				return &core.Error{Code: core.CodeUnhealthy, ID: "doctor-unhealthy",
					Msg: fmt.Sprintf("doctor found %d problem(s)", n)}
			}
			return nil
		},
	}
}

// printDoctorHuman renders the report: the config line, one block per board
// (the `boards` shape plus the git column), the resolution simulations, then
// the findings — or the ok line when the machine is clean.
func printDoctorHuman(r *app.DoctorReport) {
	fmt.Fprintf(out, "config: %s\n", r.Config)
	if r.EnvDir != "" {
		fmt.Fprintf(out, "FURROW_DIR: %s\n", r.EnvDir)
	}
	if r.EnvBoard != "" {
		fmt.Fprintf(out, "FURROW_BOARD: %s\n", r.EnvBoard)
	}
	for _, b := range r.Boards {
		state := b.SchemaState
		if !b.Exists {
			state = "missing (not on disk)"
		}
		repo := b.Repo
		if repo == "" {
			repo = "(none)"
		}
		fmt.Fprintf(out, "\n%s\n", b.Store)
		fmt.Fprintf(out, "  repo: %s  state: %s  writable: %t\n", repo, state, b.Writable)
		fmt.Fprintf(out, "  scopes: %s\n", strings.Join(b.Scopes, ", "))
		fmt.Fprintf(out, "  git: %s\n", gitLine(b.Git))
	}
	fmt.Fprintln(out, "\nresolution:")
	for _, res := range r.Resolutions {
		label := "cwd"
		if res.Asserted {
			label = "dir"
		}
		if !res.Resolved {
			fmt.Fprintf(out, "  %s %s -> (no board)\n", label, res.Dir)
			continue
		}
		scope := res.ScopeRepo
		if scope == "" {
			scope = "no repo scope"
		}
		fmt.Fprintf(out, "  %s %s -> %s (%s, %s)\n", label, res.Dir, res.Store, res.Source, scope)
	}
	fmt.Fprintln(out)
	if len(r.Problems) == 0 {
		fmt.Fprintln(out, "ok — no problems")
		return
	}
	for _, p := range r.Problems {
		fmt.Fprintf(out, "%-5s  %-22s  %s\n", p.Severity, p.Code, p.Msg)
	}
	if r.Healthy {
		fmt.Fprintln(out, "ok — notes only, no problems")
	}
}

// gitLine renders a board's git column: the state, with the counts when an
// upstream was actually compared.
func gitLine(g app.DoctorGit) string {
	if g.State != app.GitOK {
		return g.State
	}
	return fmt.Sprintf("ok (ahead %d, behind %d — as of the last fetch)", g.Ahead, g.Behind)
}
