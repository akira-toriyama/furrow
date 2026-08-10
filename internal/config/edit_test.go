package config

import (
	"strings"
	"testing"
)

const editFixture = `# header comment
standalone = false

[lanes]
# which lanes exist
order = ["inbox", "done"] # inline note
default = "inbox"

[next]
lanes = [
  "ready",
  "in-progress",
]

[alias]
triage = "ls -s inbox"
`

func mustSet(t *testing.T, doc, section, key, rendered string) SetResult {
	t.Helper()
	res, err := SetInSection(doc, section, key, rendered)
	if err != nil {
		t.Fatalf("SetInSection(%s.%s): %v", section, key, err)
	}
	return res
}

// TestSetReplacePreservesEverythingElse: only the value span moves — the
// comment lines, the inline trailing comment, and every other section survive
// byte-for-byte.
func TestSetReplacePreservesEverythingElse(t *testing.T) {
	res := mustSet(t, editFixture, "lanes", "default", `"ready"`)
	if !res.Existed || res.Unchanged {
		t.Fatalf("expected an existing changed key, got %+v", res)
	}
	if res.OldText != `"inbox"` {
		t.Errorf("OldText = %q, want %q", res.OldText, `"inbox"`)
	}
	want := strings.Replace(editFixture, `default = "inbox"`, `default = "ready"`, 1)
	if res.Doc != want {
		t.Errorf("edit must be surgical:\n--- got ---\n%s\n--- want ---\n%s", res.Doc, want)
	}
}

// TestSetKeepsTrailingComment: replacing a value keeps the line's `# inline
// note`.
func TestSetKeepsTrailingComment(t *testing.T) {
	res := mustSet(t, editFixture, "lanes", "order", `["a", "b"]`)
	if !strings.Contains(res.Doc, `order = ["a", "b"]  # inline note`) {
		t.Errorf("trailing comment must survive a value replace:\n%s", res.Doc)
	}
}

// TestSetCollapsesMultilineArray: a value spanning lines is one span; the
// replacement is one line and the closing bracket lines are gone.
func TestSetCollapsesMultilineArray(t *testing.T) {
	res := mustSet(t, editFixture, "next", "lanes", `["backlog", "ready"]`)
	if !strings.Contains(res.Doc, `lanes = ["backlog", "ready"]`) {
		t.Errorf("multi-line array must collapse to the new value:\n%s", res.Doc)
	}
	if strings.Contains(res.Doc, `"in-progress",`) {
		t.Errorf("the old multi-line items must be gone:\n%s", res.Doc)
	}
	if res.OldText == "" || !strings.Contains(res.OldText, "ready") {
		t.Errorf("OldText should carry the old span, got %q", res.OldText)
	}
}

// TestSetInsertsMissingKeyIntoSection: lands after the section's last
// non-blank line, before the blank separating the next section.
func TestSetInsertsMissingKeyIntoSection(t *testing.T) {
	res := mustSet(t, editFixture, "lanes", "done", `"done"`)
	if res.Existed {
		t.Fatal("done was absent from [lanes]")
	}
	if !strings.Contains(res.Doc, "default = \"inbox\"\ndone = \"done\"\n\n[next]") {
		t.Errorf("insert must join the section body:\n%s", res.Doc)
	}
}

// TestSetAppendsMissingSection at EOF.
func TestSetAppendsMissingSection(t *testing.T) {
	res := mustSet(t, editFixture, "priority", "step", "20")
	if !strings.HasSuffix(res.Doc, "[priority]\nstep = 20\n") {
		t.Errorf("missing section must append at EOF:\n%s", res.Doc)
	}
}

// TestSetTopLevel: replaces the bare key in place; inserts before the first
// header when absent.
func TestSetTopLevel(t *testing.T) {
	res := mustSet(t, editFixture, "", "standalone", "true")
	if !strings.Contains(res.Doc, "standalone = true") || strings.Contains(res.Doc, "standalone = false") {
		t.Errorf("top-level replace failed:\n%s", res.Doc)
	}
	res = mustSet(t, editFixture, "", "default_repo", `"o/r"`)
	head := strings.SplitN(res.Doc, "[lanes]", 2)[0]
	if !strings.Contains(head, `default_repo = "o/r"`) {
		t.Errorf("a top-level insert must land above the first header:\n%s", res.Doc)
	}
}

// TestSetNoop: writing the value already there reports Unchanged with the doc
// intact.
func TestSetNoop(t *testing.T) {
	res := mustSet(t, editFixture, "lanes", "default", `"inbox"`)
	if !res.Unchanged || res.Doc != editFixture {
		t.Errorf("a no-op set must change nothing, got unchanged=%v", res.Unchanged)
	}
}

// TestSetHashInsideStringIsNotAComment: the '#' in a value string must not
// truncate the span.
func TestSetHashInsideStringIsNotAComment(t *testing.T) {
	doc := "[alias]\nnote = \"grep '#' body\"\n"
	res, err := SetInSection(doc, "alias", "note", `"x"`)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldText != `"grep '#' body"` {
		t.Errorf("OldText = %q — the # inside the string was treated as a comment", res.OldText)
	}
}

// TestSetCommentedExampleDoesNotMatch: the template's `# default_repo = …`
// documentation line is not the key.
func TestSetCommentedExampleDoesNotMatch(t *testing.T) {
	doc := "# standalone = false\n\n[lanes]\ndefault = \"inbox\"\n"
	res, err := SetInSection(doc, "", "standalone", "true")
	if err != nil {
		t.Fatal(err)
	}
	if res.Existed {
		t.Error("a commented-out example must read as absent")
	}
	if !strings.Contains(res.Doc, "# standalone = false\nstandalone = true\n") {
		t.Errorf("insert should land after the top-level block:\n%s", res.Doc)
	}
}

// TestSetInArrayTablePicksTheIndexedEntry: the second [[board]] gets the key,
// the first is untouched.
func TestSetInArrayTablePicksTheIndexedEntry(t *testing.T) {
	doc := `[[board]]
path = "~/a/.furrow"
scopes = ["~/a"]

[[board]]
path = "~/b/.furrow"
scopes = ["~/b"]
`
	res, err := SetInArrayTable(doc, "board", 1, "autocommit", "true")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Doc, "scopes = [\"~/b\"]\nautocommit = true\n") {
		t.Errorf("the key must join entry #2:\n%s", res.Doc)
	}
	if strings.Contains(strings.SplitN(res.Doc, "[[board]]", 3)[1], "autocommit") {
		t.Errorf("entry #1 must be untouched:\n%s", res.Doc)
	}
	if _, err := SetInArrayTable(doc, "board", 5, "autocommit", "true"); err == nil {
		t.Error("an out-of-range entry index must error")
	}
}

// TestTemplateSurvivesARealEdit: the shipped template, edited, still parses
// clean — and gains exactly the intended difference.
func TestTemplateSurvivesARealEdit(t *testing.T) {
	res := mustSet(t, Template, "lanes", "default", `"ready"`)
	if res.Unchanged || !res.Existed {
		t.Fatalf("the template declares lanes.default; got %+v", res)
	}
	cfg, warn, err := LoadBytes([]byte(res.Doc), "config.toml")
	if err != nil || len(warn) > 0 {
		t.Fatalf("edited template must parse warning-free: err=%v warn=%v", err, warn)
	}
	if cfg.DefaultLane != "ready" {
		t.Errorf("the loader must see the new value, got %q", cfg.DefaultLane)
	}
}

// TestKeyRegistryAndValues: the reflection registry knows the shipped keys with
// their kinds, resolves alias.<name> dynamically, and parses/renders values.
func TestKeyRegistryAndValues(t *testing.T) {
	keys := BoardKeys()
	find := func(dotted string) (Key, string) {
		t.Helper()
		k, name, ok := ResolveKey(keys, dotted)
		if !ok {
			t.Fatalf("ResolveKey(%q) missed; vocabulary %v", dotted, KeyVocabulary(keys))
		}
		return k, name
	}
	if k, _ := find("lanes.order"); k.Kind != KindStringSlice {
		t.Errorf("lanes.order kind = %v, want slice", k.Kind)
	}
	if k, _ := find("priority.step"); k.Kind != KindInt {
		t.Errorf("priority.step kind = %v, want int", k.Kind)
	}
	if k, _ := find("standalone"); k.Kind != KindBool || k.Section != "" {
		t.Errorf("standalone must be a top-level bool, got %+v", k)
	}
	if k, name := find("alias.triage"); !k.Dynamic || name != "triage" {
		t.Errorf("alias.<name> must resolve dynamically, got %+v/%q", k, name)
	}
	if _, _, ok := ResolveKey(keys, "lanes.nope"); ok {
		t.Error("an unknown key must miss")
	}
	if _, _, ok := ResolveKey(keys, "alias."); ok {
		t.Error("a dynamic section still needs a name")
	}

	v, err := ParseCLIValue(Key{Kind: KindStringSlice}, "ready, in-progress")
	if err != nil {
		t.Fatal(err)
	}
	if RenderTOMLValue(v) != `["ready", "in-progress"]` {
		t.Errorf("slice render = %s", RenderTOMLValue(v))
	}
	if _, err := ParseCLIValue(Key{Kind: KindBool}, "yes"); err == nil {
		t.Error("a bool must take exactly true|false")
	}
	if RenderTOMLValue(`say "hi"\`) != `"say \"hi\"\\"` {
		t.Errorf("string escaping = %s", RenderTOMLValue(`say "hi"\`))
	}
}
