package cli

import (
	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// scopedLabel resolves the effective label filter for a read command (ls/next/
// revisit) when a pointer or central board is active. With no explicit --label it
// returns the board's DefaultLabel — but only when AutoFilter is on (a pointer
// always scopes; a central board honors its per-board auto_filter, default true).
// When auto_filter is off it returns flagLabel, so the whole board shows even
// though `add` still tags with the label. An explicit --label always wins,
// including --label "" ("the whole board"). The filtering is silent: PR2 retires
// the old scope banner now that auto_filter is an explicit, discoverable config
// field, so stdout stays pure data and stderr stays quiet.
func scopedLabel(cmd *cobra.Command, a *app.App, flagLabel string) string {
	if cmd.Flags().Changed("label") || a.DefaultLabel == "" || !a.AutoFilter {
		return flagLabel
	}
	return a.DefaultLabel
}
