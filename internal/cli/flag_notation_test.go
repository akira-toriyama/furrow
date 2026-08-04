package cli

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestRepeatableFlagNotationFrozen pins the comma rule (t-ezv6 leg a):
//
//	a repeatable flag holding IDENTIFIERS (labels, repos, refs, deps, lanes,
//	lint codes, ids) splits on comma; one holding FREE TEXT or a PATH-LIKE
//	value (checklist items, key=value meta, scope dirs) takes each value
//	verbatim.
//
// Comma is the reserved separator for identifier sets everywhere else in the
// CLI (-s/-l comma-OR, and names containing a comma are documented as
// unsupported), so a repeatable identifier flag that did NOT split made the
// same spelling mean different things per command — `label --add "a,b"` was
// two labels where `set --add-label "a,b"` was one (fixed in #197), and the
// epic flags re-grew the same split-brain. pflag encodes the choice in the
// value type: stringSlice splits on comma, stringArray is verbatim. This test
// freezes every repeatable string flag's type, so a new flag fails here until
// it is classified under the rule (and named in the docs' enumerations if the
// rule itself moves).
//
// The filter flags (-s/-l, lint --code/--exclude-code, next --lanes) are
// stringArray by DESIGN and still comma-OR: they join repeats and hand the
// split to the one filter parser downstream (see joinOrFilter) — splitting at
// the flag layer too would double-split. The rule is about what a comma MEANS
// to the user, not which layer implements it.
func TestRepeatableFlagNotationFrozen(t *testing.T) {
	want := map[string]string{
		// identifier sets: comma splits (stringSlice)
		"add --dep":            "stringSlice",
		"add --label":          "stringSlice",
		"add --ref":            "stringSlice",
		"add --repo":           "stringSlice",
		"archive --repo":       "stringSlice",
		"epic add --label":     "stringSlice",
		"epic add --repo":      "stringSlice",
		"epic set --add-label": "stringSlice",
		"epic set --add-repo":  "stringSlice",
		"epic set --rm-label":  "stringSlice",
		"epic set --rm-repo":   "stringSlice",
		"label --add":          "stringSlice",
		"label --rm":           "stringSlice",
		"migrate --label":      "stringSlice",
		"ref --add":            "stringSlice",
		"ref --rm":             "stringSlice",
		"repo --add":           "stringSlice",
		"repo --rm":            "stringSlice",
		"set --add-label":      "stringSlice",
		"set --rm-label":       "stringSlice",
		"sync --body":          "stringSlice",

		// free text / path-like: verbatim (stringArray)
		"add --check":         "stringArray",
		"check --add":         "stringArray",
		"config init --scope": "stringArray",
		"epic add --meta":     "stringArray",
		"epic set --meta":     "stringArray",
		"epic set --rm-meta":  "stringArray",

		// filter flags: stringArray + downstream comma-OR split (joinOrFilter)
		"brief --label":       "stringArray",
		"epic ls --label":     "stringArray",
		"lint --code":         "stringArray",
		"lint --exclude-code": "stringArray",
		"ls --label":          "stringArray",
		"ls --status":         "stringArray",
		"next --label":        "stringArray",
		"next --lanes":        "stringArray",
		"revisit --label":     "stringArray",
		"search --label":      "stringArray",
		"search --status":     "stringArray",
		"stats --label":       "stringArray",
		"stats --status":      "stringArray",
	}

	got := map[string]string{}
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			typ := f.Value.Type()
			if typ != "stringSlice" && typ != "stringArray" {
				return
			}
			got[strings.TrimSpace(path)+" --"+f.Name] = typ
		})
		for _, sub := range c.Commands() {
			walk(sub, strings.TrimSpace(path+" "+sub.Name()))
		}
	}
	root := newRootCmd()
	for _, sub := range root.Commands() {
		walk(sub, sub.Name())
	}

	var problems []string
	for k, typ := range got {
		w, ok := want[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("new repeatable flag %q (%s): classify it — identifier set -> StringSliceVar, free text/path -> StringArrayVar — and add it here", k, typ))
			continue
		}
		if w != typ {
			problems = append(problems, fmt.Sprintf("%q is %s, want %s (the comma rule: identifiers split, free text doesn't)", k, typ, w))
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			problems = append(problems, fmt.Sprintf("expected flag %q no longer exists; remove it from this table", k))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
