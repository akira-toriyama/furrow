package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A backtick in a pflag usage string is not formatting: pflag's UnquoteUsage
// extracts the backticked word as the flag's value placeholder, so a Go-doc
// style `updated` made 17 help screens print lies like `--expect-updated
// updated` and `--since ls` (and hung placeholders on bool flags, which take no
// value at all). Nothing in this repo uses the placeholder feature on purpose —
// if a flag ever should, name the placeholder deliberately and exempt it here.
func TestFlagUsageStringsCarryNoBacktick(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		check := func(f *pflag.Flag) {
			if strings.Contains(f.Usage, "`") {
				t.Errorf("%s --%s: usage contains a backtick, which pflag renders as a value placeholder: %q",
					c.CommandPath(), f.Name, f.Usage)
			}
		}
		c.LocalFlags().VisitAll(check)
		c.PersistentFlags().VisitAll(check)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())
}
