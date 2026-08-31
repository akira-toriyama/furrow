package core

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// The revisit signal vocabulary has two homes that must not drift: the Revisit*
// string constants and RevisitCodeList (the registry `furrow vocab
// revisit-codes` and the docs drift guard read). This greps the const
// declarations out of revisit.go — the same discipline as
// TestLintCodeRegistryCoversEmitted — so adding a new Revisit* constant
// without registering it fails here instead of silently weakening every
// completeness claim in scripts/check-docs-vocab.sh.
func TestRevisitCodeListMatchesConstants(t *testing.T) {
	src, err := os.ReadFile("revisit.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\tRevisit[A-Za-z]+\s+= "([a-z_]+)"`)
	var declared []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	if len(declared) == 0 {
		t.Fatal("no Revisit* constants found in revisit.go — the extraction regexp rotted, fix the test")
	}

	registered := RevisitCodeList()
	sortedDeclared := append([]string(nil), declared...)
	sortedRegistered := append([]string(nil), registered...)
	sort.Strings(sortedDeclared)
	sort.Strings(sortedRegistered)

	if len(sortedDeclared) != len(sortedRegistered) {
		t.Fatalf("RevisitCodeList has %d codes, the const block declares %d:\n  registry: %v\n  declared: %v\nregister every Revisit* constant in RevisitCodeList (and sweep the docs claims in scripts/check-docs-vocab.sh)",
			len(sortedRegistered), len(sortedDeclared), registered, declared)
	}
	for i := range sortedDeclared {
		if sortedDeclared[i] != sortedRegistered[i] {
			t.Fatalf("RevisitCodeList and the Revisit* constants disagree:\n  registry: %v\n  declared: %v", registered, declared)
		}
	}
}
