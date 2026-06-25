package cli

import (
	"fmt"

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
		Use:   "schema",
		Short: "Print the JSON Schema for .furrow/index.json",
		Long: "Print the JSON Schema (draft 2020-12) for the index. This is the single\n" +
			"source of truth; docs/schema/furrow.index.v1.json is a committed copy and CI\n" +
			"diffs the two so they cannot drift.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// schema is already valid JSON text; print it verbatim (not via the
			// JSON encoder) so the bytes match the committed file exactly.
			fmt.Fprint(out, schema.IndexV1)
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
