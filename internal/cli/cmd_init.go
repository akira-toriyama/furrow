package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a .furrow store (at FURROW_DIR/FURROW_BOARD if set, else in dir or the current directory)",
		Long: "Create a furrow store (config.toml + empty tasks/ + meta.json + bodies/).\n\n" +
			"Where it lands follows the same precedence every other command reads by:\n" +
			"FURROW_DIR (the store directory itself, created verbatim) wins, then\n" +
			"FURROW_BOARD, then <dir>/.furrow for an explicit argument, then\n" +
			"./.furrow. An argument that contradicts a set override is exit 2 —\n" +
			"init never silently creates a board somewhere the next command will\n" +
			"not look.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			argStore := ""
			if len(args) == 1 {
				abs, err := filepath.Abs(args[0])
				if err != nil {
					return core.Validationf("", "resolve %q: %v", args[0], err)
				}
				argStore = filepath.Join(abs, app.DirName)
			}
			// The env overrides name the store dir VERBATIM, in the same
			// precedence discovery reads them — creating anywhere else would
			// hand the next command a board it cannot see (measured: init used
			// to build ./.furrow under a set FURROW_DIR, exit 0, and the very
			// next add was exit 2 on the still-missing env path).
			store := argStore
			for _, env := range []string{app.EnvDir, app.EnvBoard} {
				v := os.Getenv(env)
				if v == "" {
					continue
				}
				abs, err := filepath.Abs(v)
				if err != nil {
					return core.Validationf("", "%s=%q is not a valid path: %v", env, v, err)
				}
				if argStore != "" && argStore != abs {
					return core.Validationf("", "%s=%q is set but the argument asks for %q — unset the override or drop the argument; init never creates a board the next command will not discover", env, abs, argStore)
				}
				store = abs
				break // FURROW_DIR outranks FURROW_BOARD, as in discovery
			}
			var (
				a   *app.App
				err error
			)
			if store != "" {
				a, err = app.InitAt(store)
			} else {
				cwd, werr := os.Getwd()
				if werr != nil {
					return core.Internalf("", "getwd: %v", werr)
				}
				a, err = app.Init(cwd)
			}
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(map[string]string{"dir": a.Dir})
				return nil
			}
			fmt.Fprintf(out, "initialized furrow store at %s\n", a.Dir)
			return nil
		},
	}
}
