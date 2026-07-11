package cli

import (
	"fmt"
	"os"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newConfigCmd is furrow's first parent command: a `config` namespace for the
// user-level (home) config that declares central boards. Its subcommands write
// the template (`config init`) and locate it (`config path`).
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and write the user-level furrow config (central boards)",
		Long: "Manage ~/.config/furrow/config.toml — the per-machine config that says which\n" +
			"central board backs the repos under your tree. `config init` writes the\n" +
			"template; `config path` prints where it lives. (Board rules live in each\n" +
			"board's own .furrow/config.toml, not here.)",
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newConfigInitCmd(), newConfigPathCmd())
	return cmd
}

// newConfigPathCmd prints the resolved path to the user-level config and, when
// that file is half-written, surfaces its clamp warnings on stderr (stdout stays
// the bare path so it still pipes cleanly).
func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the resolved path to the user-level furrow config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := app.GlobalConfigPath()
			if err != nil {
				return err
			}
			for _, w := range app.GlobalConfigWarnings() {
				fmt.Fprintln(errOut, w)
			}
			if jsonMode() {
				emitObject(map[string]string{"path": p})
				return nil
			}
			fmt.Fprintln(out, p)
			return nil
		},
	}
}

// newConfigInitCmd writes the user-level config template, deriving the central
// board's path/scopes from the nearest .furrow when run inside a board.
func newConfigInitCmd() *cobra.Command {
	var pathFlag string
	var scopeFlags []string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the user-level furrow config (central-board template)",
		Long: "Create ~/.config/furrow/config.toml. Run inside a board it fills the board\n" +
			"path (nearest .furrow) and scope (that board repo's parent) in for you;\n" +
			"--path/--scope override, and elsewhere it writes a placeholder to edit. It\n" +
			"never overwrites an existing config.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return core.Internalf("", "getwd: %v", err)
			}
			p, derived, err := app.InitGlobalConfig(cwd, pathFlag, scopeFlags)
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(map[string]any{"path": p, "derived": derived})
				return nil
			}
			if derived {
				fmt.Fprintf(out, "wrote %s (board filled in from context — review it)\n", p)
			} else {
				fmt.Fprintf(out, "wrote %s (placeholder — set the board path and scopes)\n", p)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pathFlag, "path", "", "central .furrow path (overrides context derivation)")
	cmd.Flags().StringArrayVar(&scopeFlags, "scope", nil, "scope dir the board activates under (repeatable; overrides derivation)")
	return cmd
}
