package cli

import (
	"fmt"
	"os"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newConfigCmd is the `config` namespace over furrow's config files — the
// resolved board's .furrow/config.toml and the user-level one that declares
// central boards.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and write furrow's config files (board rules and central boards)",
		Long: "Manage furrow's two config files: the resolved board's .furrow/config.toml\n" +
			"(lanes, next, aliases — the rules every command reads) and\n" +
			"~/.config/furrow/config.toml (the per-machine [[board]] entries that say\n" +
			"which central board backs the repos under your tree). `config set` writes\n" +
			"one key of either surgically; `config init` writes the user-level template;\n" +
			"`config path` prints where the user-level file lives.",
		// `furrow config` alone prints help (exit 0); an unknown subcommand
		// (`config show`) is exit 2 with the known names in candidates, matching
		// the root's unknown-command contract instead of swallowing it as exit-0
		// help prose.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return unknownSubcommandErr(cmd, args[0])
		},
	}
	cmd.AddCommand(newConfigInitCmd(), newConfigPathCmd(), newConfigSetCmd())
	return cmd
}

// newConfigSetCmd is the config WRITER — the one command allowed to touch a
// config.toml (both files stay read-only to every other command). The edit is
// git-config-style surgical: only the key's value span changes, comments and
// ordering survive byte-for-byte.
func newConfigSetCmd() *cobra.Command {
	var user bool
	var board string
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one config key — the board's config.toml, or --user for a [[board]] entry",
		Long: "Write one key surgically (comments, ordering, and every other byte survive).\n" +
			"The default target is the RESOLVED BOARD's .furrow/config.toml — the same\n" +
			"board every other command operates on; --user targets a [[board]] entry of\n" +
			"~/.config/furrow/config.toml instead (--board <ref> picks the entry by path\n" +
			"or scope — exact, else unique substring; omit it when one entry exists).\n\n" +
			"<key> is dotted (lanes.default, next.lanes, alias.<name>; bare for a\n" +
			"top-level key like standalone). A list value is comma-split\n" +
			"(next.lanes ready,in-progress). The writer is STRICT where the reader is\n" +
			"lenient: an unknown key is exit 2 with the vocabulary in candidates, and a\n" +
			"value the reader would clamp away with a warning is refused before the\n" +
			"write — what you set is exactly what a read will honor, or nothing changed.\n" +
			"A board write rides the next `furrow sync` like every machine-written file.",
		Example: "  furrow config set lanes.default ready\n" +
			"  furrow config set next.lanes ready,in-progress\n" +
			"  furrow config set alias.triage \"ls -s inbox\"\n" +
			"  furrow config set --user --board projects autocommit true",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if board != "" && !user {
				return core.Validationf("config", "--board picks a [[board]] entry of the USER config; add --user (the default target, the board's own config.toml, has no entries)")
			}
			var edit *app.ConfigEdit
			var err error
			if user {
				edit, err = app.ConfigSetUser(board, args[0], args[1])
			} else {
				var a *app.App
				if a, err = openApp(); err != nil {
					return err
				}
				edit, err = a.ConfigSetBoard(args[0], args[1])
			}
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(edit)
				return nil
			}
			if len(edit.Changed) == 0 {
				fmt.Fprintf(out, "unchanged %s  (%s)\n", edit.Key, edit.File)
				return nil
			}
			fmt.Fprintf(out, "set %s = %s  (%s)\n", edit.Key, args[1], edit.File)
			return nil
		},
	}
	cmd.Flags().BoolVar(&user, "user", false, "target the user-level config's [[board]] entries instead of the board's config.toml")
	cmd.Flags().StringVar(&board, "board", "", "which [[board]] entry to edit (path or scope; exact, else unique substring) — requires --user")
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
