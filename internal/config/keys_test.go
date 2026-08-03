package config

import (
	"reflect"
	"testing"
)

// TopLevelKeys is reflection over the raw decode struct, so this expected list
// is deliberately a frozen copy — the same teeth as TestShardFieldsGolden:
// growing the config vocabulary should fail one test whose message names the
// follow-through. When you add a section here, the docs that enumerate the
// config vocabulary (README's annotated example, docs/architecture.md's table,
// CLAUDE.md's Configuration note) must gain it too — scripts/check-docs-vocab.sh
// enforces that, and this test is what tells you it is about to.
func TestTopLevelKeysFrozen(t *testing.T) {
	want := []string{
		"lanes", "priority", "ids", "archive", "ui", "next",
		"labels", "revisit", "review", "lint", "due", "alias", "standalone", "default_repo",
	}
	got := TopLevelKeys()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config's top-level key vocabulary changed:\n  got:  %v\n  want: %v\nIf the change is deliberate: update this list AND sweep the documented config\nenumerations (README example, docs/architecture.md table, CLAUDE.md) — then\nrun scripts/check-docs-vocab.sh, which fails until every claim names the new key.",
			got, want)
	}
}
