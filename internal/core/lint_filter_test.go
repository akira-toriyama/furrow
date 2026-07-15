package core

import (
	"reflect"
	"testing"
)

func codes(ps []Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func sampleProblems() []Problem {
	return []Problem{
		{Severity: SevError, Code: "dep-cycle", ID: "t-a", Msg: "cycle"},
		{Severity: SevWarn, Code: "reconcile-gap", ID: "t-b", Msg: "gap"},
		{Severity: SevWarn, Code: "dangling-link", ID: "t-c", Msg: "link"},
		{Severity: SevError, Code: "missing-body", ID: "t-d", Msg: "body"},
	}
}

func TestFilterProblemsZeroValueIsNoOp(t *testing.T) {
	ps := sampleProblems()
	got := FilterProblems(ps, ProblemFilter{})
	if !reflect.DeepEqual(codes(got), codes(ps)) {
		t.Fatalf("zero filter changed the set: %v", codes(got))
	}
}

func TestFilterProblemsIgnoreCodes(t *testing.T) {
	got := FilterProblems(sampleProblems(), ProblemFilter{IgnoreCodes: []string{"reconcile-gap"}})
	want := []string{"dep-cycle", "dangling-link", "missing-body"}
	if !reflect.DeepEqual(codes(got), want) {
		t.Fatalf("ignore reconcile-gap: got %v want %v", codes(got), want)
	}
}

func TestFilterProblemsAllowList(t *testing.T) {
	got := FilterProblems(sampleProblems(), ProblemFilter{Codes: []string{"dangling-link", "dep-cycle"}})
	want := []string{"dep-cycle", "dangling-link"} // original order preserved
	if !reflect.DeepEqual(codes(got), want) {
		t.Fatalf("allow-list: got %v want %v", codes(got), want)
	}
}

func TestFilterProblemsExcludeWinsOverAllow(t *testing.T) {
	// A code named in BOTH --code and --exclude-code is dropped: an explicit
	// exclusion is the stronger intent.
	got := FilterProblems(sampleProblems(), ProblemFilter{
		Codes:        []string{"dep-cycle", "dangling-link"},
		ExcludeCodes: []string{"dangling-link"},
	})
	want := []string{"dep-cycle"}
	if !reflect.DeepEqual(codes(got), want) {
		t.Fatalf("exclude-over-allow: got %v want %v", codes(got), want)
	}
}

func TestFilterProblemsSeverityIsExact(t *testing.T) {
	errs := FilterProblems(sampleProblems(), ProblemFilter{Severity: SevError})
	if !reflect.DeepEqual(codes(errs), []string{"dep-cycle", "missing-body"}) {
		t.Fatalf("severity=error: got %v", codes(errs))
	}
	// --severity warn is an EXACT match: only warnings, never "warn and above".
	warns := FilterProblems(sampleProblems(), ProblemFilter{Severity: SevWarn})
	if !reflect.DeepEqual(codes(warns), []string{"reconcile-gap", "dangling-link"}) {
		t.Fatalf("severity=warn: got %v", codes(warns))
	}
}

func TestFilterProblemsSeverityErrorDrivesEmptyExit(t *testing.T) {
	// The load-bearing exit-code claim: filtering out every error leaves a set
	// with no errors, so HasErrors is false and lint exits 0.
	ps := FilterProblems(sampleProblems(), ProblemFilter{ExcludeCodes: []string{"dep-cycle", "missing-body"}})
	if HasErrors(ps) {
		t.Fatalf("excluding both errors must leave HasErrors=false, got %v", codes(ps))
	}
	// And --severity warn hides errors, so it too reports no errors.
	if HasErrors(FilterProblems(sampleProblems(), ProblemFilter{Severity: SevWarn})) {
		t.Fatal("severity=warn must hide errors so HasErrors=false")
	}
}

func TestIsLintCodeAndList(t *testing.T) {
	for _, c := range []string{"dep-cycle", "reconcile-gap", "alias-shadow", "unknown-shard-key"} {
		if !IsLintCode(c) {
			t.Errorf("expected %q to be a known lint code", c)
		}
	}
	if IsLintCode("no-such-code") || IsLintCode("") {
		t.Error("unknown / empty code must not be a known lint code")
	}
	list := LintCodeList()
	if len(list) == 0 {
		t.Fatal("LintCodeList is empty")
	}
	for i := 1; i < len(list); i++ {
		if list[i-1] >= list[i] {
			t.Fatalf("LintCodeList is not sorted/unique at %d: %v", i, list)
		}
	}
}
