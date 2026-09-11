// Command furrow is a git-native, plain-text task tracker: one JSON shard
// plus one markdown body per task, driven from a cobra CLI.
//
// All logic lives under internal/; main only maps the CLI's resolved exit
// code to the process. See docs/architecture.md.
package main

import (
	"os"

	// The board declares its calendar as an IANA zone ([due].timezone), so
	// time.LoadLocation must resolve the same names everywhere furrow runs. A
	// host without tzdata would otherwise fall back to the process zone with only
	// a warning, and the same board would keep two different calendars — the
	// operator's machine and the UTC CI runner that closes tasks through the
	// identical write path.
	_ "time/tzdata"

	"github.com/akira-toriyama/furrow/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
