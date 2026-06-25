// Command furrow is a repo-local, plain-text task tracker: a JSON index plus
// per-task markdown bodies, driven from a cobra CLI and a bubbletea TUI.
//
// All logic lives in internal/{core,config,store,app,cli,tui}; main only maps
// the CLI's resolved exit code to the process. See docs/architecture.md.
package main

import (
	"os"

	"github.com/akira-toriyama/furrow/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
