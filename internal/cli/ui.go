package cli

import "github.com/akira-toriyama/furrow/internal/core"

// runUI launches the interactive TUI. NOTE: the bubbletea TUI is ROADMAP Phase 6
// and not wired up yet; this stub keeps the command present and honest rather
// than pretending. Replace this with internal/tui.Run when Phase 6 lands.
func runUI() error {
	return &core.Error{
		Code: core.CodeValidation,
		Msg:  "the TUI (furrow ui) is not implemented yet — see ROADMAP Phase 6; use the CLI for now",
	}
}
