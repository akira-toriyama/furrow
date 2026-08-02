package core

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// The error-kind vocabulary has two homes that must not drift: the Kind*
// string constants and the errorKinds registry behind ErrorKindList (which
// `furrow vocab error-kinds` and the docs drift guard read). This greps the
// const declarations out of error_kinds.go — the same discipline as
// TestRevisitCodeListMatchesConstants — so adding a Kind* constant without
// registering it fails here instead of silently weakening every completeness
// claim in scripts/check-docs-vocab.sh.
func TestErrorKindListMatchesConstants(t *testing.T) {
	src, err := os.ReadFile("error_kinds.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\tKind[A-Za-z]+\s+= "([a-z][a-z0-9-]*)"`)
	var declared []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	if len(declared) == 0 {
		t.Fatal("no Kind* constants found in error_kinds.go — the extraction regexp rotted, fix the test")
	}

	registered := ErrorKindList()
	sortedDeclared := append([]string(nil), declared...)
	sortedRegistered := append([]string(nil), registered...)
	sort.Strings(sortedDeclared)
	sort.Strings(sortedRegistered)

	if len(sortedDeclared) != len(sortedRegistered) {
		t.Fatalf("ErrorKindList has %d kinds, the const block declares %d:\n  registry: %v\n  declared: %v\nregister every Kind* constant in errorKinds (and sweep the docs claims in scripts/check-docs-vocab.sh)",
			len(sortedRegistered), len(sortedDeclared), registered, declared)
	}
	for i := range sortedDeclared {
		if sortedDeclared[i] != sortedRegistered[i] {
			t.Fatalf("ErrorKindList and the Kind* constants disagree:\n  registry: %v\n  declared: %v", registered, declared)
		}
	}
}

// Every constructor stamps a generic kind, so an Error built through them can
// never reach the renderer kindless.
func TestConstructorsStampGenericKinds(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		kind string
	}{
		{"NotFound", NotFound("t-1"), KindNotFound},
		{"Validationf", Validationf("t-1", "bad"), KindValidation},
		{"Internalf", Internalf("", "boom"), KindInternal},
	}
	for _, c := range cases {
		if c.err.Kind != c.kind {
			t.Errorf("%s: kind = %q, want %q", c.name, c.err.Kind, c.kind)
		}
		if !IsErrorKind(c.err.Kind) {
			t.Errorf("%s: kind %q is not registered", c.name, c.err.Kind)
		}
	}
	if got := NotFound("t-1").Subject; got != "t-1" {
		t.Errorf("NotFound subject = %q, want the missing id", got)
	}
	// KindForCode maps each exit class to its generic kind (the summary-error
	// fallback), never to an unregistered string.
	for code, want := range map[Code]string{CodeNotFound: KindNotFound, CodeValidation: KindValidation, CodeInternal: KindInternal, Code(7): KindInternal} {
		if got := KindForCode(code); got != want {
			t.Errorf("KindForCode(%d) = %q, want %q", code, got, want)
		}
	}
}
