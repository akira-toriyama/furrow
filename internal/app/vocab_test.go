package app

import (
	"reflect"
	"testing"
)

// Every registered vocabulary must be non-empty: an empty list would make every
// completeness claim in scripts/check-docs-vocab.sh vacuously green — the guard
// would pass while checking nothing.
func TestVocabulariesAllNonEmpty(t *testing.T) {
	vs := Vocabularies()
	if len(vs) == 0 {
		t.Fatal("Vocabularies() is empty")
	}
	for name, members := range vs {
		if len(members) == 0 {
			t.Errorf("vocabulary %q is empty", name)
		}
		for _, m := range members {
			if m == "" {
				t.Errorf("vocabulary %q contains an empty member", name)
			}
		}
	}
}

// The revisit summary shape is reflected from RevisitSummary's json tags; this
// frozen copy is the teeth (like config's TestTopLevelKeysFrozen): adding a
// field fails here first, with the docs follow-through named, instead of
// surfacing as a confusing check-docs-vocab.sh failure about prose.
func TestRevisitSummaryKeysFrozen(t *testing.T) {
	want := []string{"dep_done", "stale", "unreviewed", "children_done", "stuck_container"}
	got := revisitSummaryKeys()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RevisitSummary's json key vocabulary changed:\n  got:  %v\n  want: %v\nIf deliberate: update this list AND the documented summary shapes (README's\nsync section, CLAUDE.md's sync note, the glossary's sync row) — then run\nscripts/check-docs-vocab.sh, which fails until every claim names the new key.",
			got, want)
	}
}
