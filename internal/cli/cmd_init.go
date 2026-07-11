package cli

import (
	"fmt"
	"os"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a .furrow store in the current directory",
		Long:  "Create .furrow/ (config.toml + empty tasks/ + meta.json + bodies/) in the current directory.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return core.Internalf("", "getwd: %v", err)
			}
			a, err := app.Init(cwd)
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
