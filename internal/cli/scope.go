package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// scopedLabel resolves the effective label filter for a read command (ls/next/
// revisit) when a per-repo pointer is active. With no explicit --label and a
// pointer default_label, it returns that label and announces the scope on stderr
// — so the filtering is never silent and stdout stays pure data. An explicit
// --label always wins, including --label "" which means "the whole board".
// Returns flagLabel unchanged when no pointer label is set.
func scopedLabel(cmd *cobra.Command, a *app.App, flagLabel string) string {
	if cmd.Flags().Changed("label") || a.DefaultLabel == "" {
		return flagLabel
	}
	fmt.Fprintf(errOut, "furrow: board=%s scope=label=%s (-l '' for all)\n", a.Dir, a.DefaultLabel)
	return a.DefaultLabel
}
