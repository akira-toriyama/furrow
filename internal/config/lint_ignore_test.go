package config

import (
	"reflect"
	"testing"
)

// TestLintIgnoreCodesParsing pins [lint].ignore_codes: it parses to a trimmed,
// deduped slice, defaults to empty, and is stored VERBATIM — config is core-free,
// so it never validates a code against the vocabulary (an unknown entry is the
// app-layer lint's job to warn about). No warnings here regardless of content.
func TestLintIgnoreCodesParsing(t *testing.T) {
	cfg, warn, err := Load(writeTOML(t, "[lint]\nignore_codes = [\" reconcile-gap \", \"dep-mirrors-children\", \"reconcile-gap\", \"\", \"totally-made-up\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// trimmed, empty dropped, later dup dropped, unknown kept verbatim (no clamp).
	want := []string{"reconcile-gap", "dep-mirrors-children", "totally-made-up"}
	if !reflect.DeepEqual(cfg.LintIgnoreCodes, want) {
		t.Fatalf("ignore_codes = %v, want %v", cfg.LintIgnoreCodes, want)
	}
	if len(warn) != 0 {
		t.Errorf("config must not warn about ignore_codes content (that is app.Lint's job): %v", warn)
	}
	if len(Default().LintIgnoreCodes) != 0 {
		t.Errorf("default ignore_codes should be empty, got %v", Default().LintIgnoreCodes)
	}
}
