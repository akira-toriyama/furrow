package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// batchFields is a closed vocabulary that `add --help` also enumerates in prose,
// so the enumeration is a second hand-kept copy of it. v11's `anchor` shipped in
// the registry and the decoder while the prose still listed sixteen fields, which
// told a reader a valid field was invalid.
//
// The check is scoped to the enumeration itself, between the two markers below:
// searching the whole Long text cannot discriminate, because `--repeat`'s
// paragraph calls a series' first date its "anchor" in an unrelated sense, and a
// whole-text search therefore passed on the very omission this test exists for.
func TestAddHelpEnumeratesEveryBatchField(t *testing.T) {
	const (
		opens  = "per-task fields:"
		closes = "An unknown field"
	)
	// `epic add` is also named "add", and its help names no batch field: take the
	// root's own child, not the first match in a walk.
	var add *cobra.Command
	for _, c := range newRootCmd().Commands() {
		if c.Name() == "add" {
			add = c
			break
		}
	}
	if add == nil {
		t.Fatal("no top-level `add` command in the tree")
	}
	i := strings.Index(add.Long, opens)
	j := strings.Index(add.Long, closes)
	if i < 0 || j <= i {
		t.Fatalf("cannot locate the batch field enumeration in `add --help` between %q and %q — the markers moved, so this test is no longer reading the list it guards", opens, closes)
	}
	list := add.Long[i+len(opens) : j]
	for _, f := range batchFields {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(f) + `\b`).MatchString(list) {
			t.Errorf("batch field %q is not named in the per-task field list in `add --help` (cmd_add.go)", f)
		}
	}
}
