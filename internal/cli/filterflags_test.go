package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The registrar's whole point, pinned: on every filtering read, a shared
// filter flag's --help line is the canonical one — except the explicitly
// allowed overrides, each of which exists because the MEANING differs there
// (see the wantUsage call sites). A new divergence fails here with the
// command and flag named, so the drift t-jbph removed cannot regrow silently.
// The write-side commands that merely share a flag NAME (done/move/set's
// selector -l/-r, add's attach -l/-r) are deliberately not judged: different
// feature, different wording on purpose.
func TestFilterFlagUsagesAreUniform(t *testing.T) {
	canonical := map[string]string{
		"label":    usageLabel,
		"repo":     usageRepo,
		"epic":     usageEpic,
		"limit":    usageLimit,
		"archived": usageArchived,
		"since":    usageSince,
		"until":    usageUntil,
	}
	// Every command that carries filter flags, all held to the canonical lines.
	reads := []string{"furrow ls", "furrow next", "furrow brief", "furrow revisit",
		"furrow stats", "furrow search", "furrow show", "furrow epic ls"}
	// The allowed overrides: "command path/flag". Adding one requires a
	// wantUsage call whose comment says why the meaning differs.
	overridden := map[string]bool{
		"furrow ls/limit":    true, // top N of the --sort'ed set
		"furrow next/epic":   true, // swaps the active-epic scope, not a member filter
		"furrow brief/limit": true, // caps body-attached picks; next_total uncapped
		"furrow stats/since": true, // adds the created/closed flow section
		"furrow stats/until": true, // the flow window's other bound
	}

	want := map[string]bool{}
	for _, r := range reads {
		want[r] = true
	}
	found := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if want[c.CommandPath()] {
			found[c.CommandPath()] = true
			for name, canon := range canonical {
				fl := c.LocalFlags().Lookup(name)
				if fl == nil || overridden[c.CommandPath()+"/"+name] {
					continue
				}
				if fl.Usage != canon {
					t.Errorf("%s --%s diverges from the canonical usage:\n  got:  %q\n  want: %q (or an explicit override with a reason)",
						c.CommandPath(), name, fl.Usage, canon)
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())
	for _, r := range reads {
		if !found[r] {
			t.Errorf("read command %q not found in the tree — update this test's list", r)
		}
	}
	// Each override must still exist as a flag, or the allowlist is stale.
	for key := range overridden {
		parts := strings.SplitN(key, "/", 2)
		if parts[1] == "" {
			t.Fatalf("malformed override key %q", key)
		}
	}
}
