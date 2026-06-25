package cli

import (
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/tui"
)

// runUI launches the interactive bubbletea TUI against the discovered store.
// It requires a terminal; in a non-interactive context it refuses (the CLI is
// the non-TTY interface).
func runUI() error {
	if !isTTY() {
		return &core.Error{Code: core.CodeValidation, Msg: "furrow ui needs a terminal; use the CLI in non-interactive contexts"}
	}
	a, err := openApp()
	if err != nil {
		return err
	}
	return tui.Run(a)
}
