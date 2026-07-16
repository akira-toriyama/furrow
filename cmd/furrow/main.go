// Command furrow is a git-native, plain-text task tracker: one JSON shard
// plus one markdown body per task, driven from a cobra CLI.
//
// All logic lives in internal/{core,config,store,app,cli}; main only maps
// the CLI's resolved exit code to the process. See docs/architecture.md.
package main

import (
	"os"

	"github.com/akira-toriyama/furrow/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
