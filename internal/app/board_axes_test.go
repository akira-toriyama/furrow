package app

import (
	"os"
	"regexp"
	"testing"
)

// layoutOf is the whole LAYOUT axis, and its default arm is silent: a discovery
// source nobody mapped falls through to central. So the guard has to be the
// PRODUCTION const block, not a second list in this file — the pattern
// internal/core/error_kinds_test.go and internal/app/doctor_codes_test.go
// already use for the same failure.
var sourceConstRe = regexp.MustCompile(`(?m)^\tSource[A-Za-z]+\s+= "([a-z][a-z0-9-]*)"`)

// declaredSources extracts the Source* constants from app.go. A rotted pattern
// must be loud rather than vacuous, so finding none is fatal.
func declaredSources(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	ms := sourceConstRe.FindAllStringSubmatch(string(src), -1)
	if len(ms) == 0 {
		t.Fatal("no Source* constants matched in app.go — the pattern rotted; fix this test")
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// Every declared discovery source must have a DELIBERATE layout. Without this,
// a new arm inherits central from layoutOf's fallback and nothing says so.
func TestEverySourceHasADeclaredLayout(t *testing.T) {
	want := map[string]string{
		SourceLocal:      LayoutRepoLocal,
		SourceEnv:        LayoutCentral,
		SourcePointer:    LayoutCentral,
		SourceUserConfig: LayoutCentral,
	}
	for _, src := range declaredSources(t) {
		expect, ok := want[src]
		if !ok {
			t.Errorf("discovery source %q has no layout decided here — add it to this map (layoutOf defaults it to %q in silence)", src, LayoutCentral)
			continue
		}
		if got := layoutOf(src); got != expect {
			t.Errorf("layoutOf(%q) = %q, want %q", src, got, expect)
		}
	}
	if n := len(declaredSources(t)); n != len(want) {
		t.Errorf("app.go declares %d discovery source(s), this test maps %d", n, len(want))
	}
}

// Layouts is what `furrow vocab layouts` prints, so it must carry every member
// layoutOf can return — a vocabulary that under-reports is worse than none.
func TestLayoutsCoversEveryReturn(t *testing.T) {
	got := map[string]bool{}
	for _, l := range Layouts() {
		got[l] = true
	}
	for _, src := range declaredSources(t) {
		if !got[layoutOf(src)] {
			t.Errorf("layoutOf(%q) = %q is not in Layouts() = %v", src, layoutOf(src), Layouts())
		}
	}
}
