package config

import (
	"strings"
	"testing"
)

// [lint.severity] load semantics: a valid entry lands trimmed in LintSeverity;
// the LEVEL vocabulary (error|warn) is validated HERE, clamp-don't-reject, so
// `config set`'s regression guard can refuse a typo'd level before it is
// written; the CODE vocabulary is core's and deliberately NOT checked here
// (app.Lint warns about a dead code — the ignore_codes split).
func TestLintSeverityLoad(t *testing.T) {
	doc := `
[lint.severity]
due-overdue = "warn"
"  spaced  " = "error"
bad-level = "loud"
"" = "warn"
`
	c, warn, err := LoadBytes([]byte(doc), "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LintSeverity["due-overdue"]; got != "warn" {
		t.Errorf("due-overdue = %q, want warn", got)
	}
	if got := c.LintSeverity["spaced"]; got != "error" {
		t.Errorf("trimmed key = %q, want error (keys are trimmed)", got)
	}
	if _, ok := c.LintSeverity["bad-level"]; ok {
		t.Errorf("bad-level survived the clamp: %v", c.LintSeverity)
	}
	var badLevel, blankKey bool
	for _, w := range warn {
		if strings.Contains(w, `lint.severity.bad-level "loud" is not a level`) {
			badLevel = true
		}
		if strings.Contains(w, "lint.severity entry with an empty code") {
			blankKey = true
		}
	}
	if !badLevel || !blankKey {
		t.Errorf("clamp warnings missing (badLevel=%v blankKey=%v): %v", badLevel, blankKey, warn)
	}
	// A code core would reject loads fine — that vocabulary is not this layer's.
	doc2 := "[lint.severity]\nno-such-code = \"warn\"\n"
	c2, warn2, err := LoadBytes([]byte(doc2), "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c2.LintSeverity["no-such-code"] != "warn" || len(warn2) != 0 {
		t.Errorf("unknown code should load without warning here; got %v / %v", c2.LintSeverity, warn2)
	}
}

// The absent table stays nil — a board that never opts in allocates nothing and
// (via ApplySeverity's len guard) pays nothing.
func TestLintSeverityAbsent(t *testing.T) {
	c, _, err := LoadBytes([]byte("[lint]\narchive_done = 3\n"), "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.LintSeverity != nil {
		t.Errorf("LintSeverity = %v, want nil when the table is absent", c.LintSeverity)
	}
}

// The dynamic sub-table is a settable-key vocabulary member: ResolveKey peels
// the operator-chosen code off `lint.severity.<code>` (the section is DOTTED,
// so the alias-era first-dot split would have failed), and the vocabulary
// renders it as `lint.severity.<name>`.
func TestResolveKeyNestedDynamic(t *testing.T) {
	keys := BoardKeys()
	k, name, ok := ResolveKey(keys, "lint.severity.due-overdue")
	if !ok || !k.Dynamic || k.Section != "lint.severity" || name != "due-overdue" {
		t.Fatalf("ResolveKey(lint.severity.due-overdue) = %+v, %q, %v", k, name, ok)
	}
	if _, _, ok := ResolveKey(keys, "lint.severity."); ok {
		t.Error("an empty dynamic name resolved; want a miss")
	}
	// The alias behavior this generalizes must survive unchanged.
	if k, name, ok := ResolveKey(keys, "alias.triage"); !ok || !k.Dynamic || name != "triage" {
		t.Errorf("ResolveKey(alias.triage) = %+v, %q, %v", k, name, ok)
	}
	var vocab []string
	for _, k := range keys {
		vocab = append(vocab, k.Dotted())
	}
	joined := strings.Join(vocab, " ")
	if !strings.Contains(joined, "lint.severity.<name>") {
		t.Errorf("vocabulary misses lint.severity.<name>: %s", joined)
	}
}

// SetInSection with the dotted section edits (or creates) the literal
// [lint.severity] table — the write path `config set lint.severity.<code>`
// rides, so the round trip from a fresh template must be loadable and honored.
func TestSetNestedDynamicRoundTrip(t *testing.T) {
	res, err := SetInSection(Template, "lint.severity", "due-overdue", `"warn"`)
	if err != nil {
		t.Fatal(err)
	}
	c, warn, err := LoadBytes([]byte(res.Doc), "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warn {
		if strings.Contains(w, "lint.severity") {
			t.Errorf("round trip gained a severity clamp warning: %s", w)
		}
	}
	if c.LintSeverity["due-overdue"] != "warn" {
		t.Errorf("round trip lost the entry: %v", c.LintSeverity)
	}
}
