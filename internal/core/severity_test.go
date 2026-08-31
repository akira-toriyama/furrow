package core

import (
	"reflect"
	"testing"
)

// ApplySeverity is a transform, not a filter: levels move, nothing is dropped,
// the input is not aliased, and re-applying is a no-op — the property the CLI
// leans on when it levels its late-born alias-shadow findings separately.
func TestApplySeverity(t *testing.T) {
	in := []Problem{
		{Severity: SevError, Code: "due-overdue", ID: "t-1"},
		{Severity: SevWarn, Code: "orphan-body", ID: "b-1"},
		{Severity: SevWarn, Code: "dangling-link", ID: "t-2"},
	}
	ov := map[string]string{
		"due-overdue":   SevWarn,
		"orphan-body":   SevError,
		"no-such":       SevWarn, // unknown code: matches nothing, harmless
		"dangling-link": "loud",  // invalid level: config validates these away, but a raw caller must not corrupt Severity
	}
	got := ApplySeverity(in, ov)
	want := []Problem{
		{Severity: SevWarn, Code: "due-overdue", ID: "t-1"},
		{Severity: SevError, Code: "orphan-body", ID: "b-1"},
		{Severity: SevWarn, Code: "dangling-link", ID: "t-2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ApplySeverity = %+v, want %+v", got, want)
	}
	if in[0].Severity != SevError {
		t.Error("input slice was mutated; ApplySeverity must copy")
	}
	if again := ApplySeverity(got, ov); !reflect.DeepEqual(again, want) {
		t.Errorf("not idempotent: %+v", again)
	}
	if out := ApplySeverity(in, nil); &out[0] != &in[0] {
		t.Error("nil overrides should return the input slice as-is")
	}
}
