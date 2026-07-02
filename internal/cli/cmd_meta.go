package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/schema"
	"github.com/akira-toriyama/furrow/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the furrow version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				printJSON(map[string]string{"version": version.Version})
				return nil
			}
			fmt.Fprintf(out, "furrow %s\n", version.Version)
			return nil
		},
	}
}

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema [task|meta]",
		Short: "Print the JSON Schema for a task shard or meta.json",
		Long: "Print the JSON Schema (draft 2020-12) for the store's files. With no\n" +
			"argument (or \"task\") it prints the schema for one .furrow/tasks/<id>.json\n" +
			"shard; \"meta\" prints the schema for .furrow/meta.json. These are the single\n" +
			"source of truth; docs/schema/furrow.task.v2.json and furrow.meta.v1.json are\n" +
			"committed copies and CI diffs them so they cannot drift.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"task", "meta"},
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := "task"
			if len(args) == 1 {
				kind = args[0]
			}
			// schema consts are already valid JSON text; print verbatim (not via
			// the JSON encoder) so the bytes match the committed file exactly.
			switch kind {
			case "task":
				fmt.Fprint(out, schema.TaskV2)
			case "meta":
				fmt.Fprint(out, schema.MetaV1)
			default:
				return core.Validationf("", "unknown schema kind %q (want \"task\" or \"meta\")", kind)
			}
			return nil
		},
	}
}

func newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Launch the interactive TUI (bubbletea)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI()
		},
	}
}
