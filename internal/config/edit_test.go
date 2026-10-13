package config

import (
	"strings"
	"testing"
)

const editFixture = `# header comment
mode = "shared"

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
// note` — and the gap before it, byte for byte (the editor used to re-join
// with two spaces, so `width = 5 # t-k3m9p` came back one byte wider).
func TestSetKeepsTrailingComment(t *testing.T) {
	res := mustSet(t, editFixture, "lanes", "order", `["a", "b"]`)
	if !strings.Contains(res.Doc, `order = ["a", "b"] # inline note`) {
		t.Errorf("trailing comment must survive a value replace with its gap:\n%s", res.Doc)
	}
	doc := "[ids]\nwidth = 5   # t-k3m9p\nprefix\t=\t\"t-\"  # tabbed\n"
	res = mustSet(t, doc, "ids", "width", "2")
	if res.Doc != "[ids]\nwidth = 2   # t-k3m9p\nprefix\t=\t\"t-\"  # tabbed\n" {
		t.Errorf("the gap before the comment must survive:\n%s", res.Doc)
	}
	res = mustSet(t, res.Doc, "ids", "prefix", `"x-"`)
	if res.Doc != "[ids]\nwidth = 2   # t-k3m9p\nprefix\t=\t\"x-\"  # tabbed\n" {
		t.Errorf("tabs around '=' must survive:\n%s", res.Doc)
	}
	// Set back: the document is byte-identical to the original.
	back := mustSet(t, mustSet(t, res.Doc, "ids", "width", "5").Doc, "ids", "prefix", `"t-"`)
	if back.Doc != doc {
		t.Errorf("a round trip must restore every byte:\n--- got ---\n%s\n--- want ---\n%s", back.Doc, doc)
	}
}

// A key TOML cannot spell bare (an alias named 日本, or one with a space) is
// written quoted and found again on the next set — never duplicated, never a
// document that fails to parse.
func TestSetQuotesANonBareKeyAndFindsItAgain(t *testing.T) {
	res := mustSet(t, editFixture, "alias", "日本", `"ls"`)
	if !strings.Contains(res.Doc, "\n\"日本\" = \"ls\"\n") {
		t.Errorf("a non-bare key must be written quoted:\n%s", res.Doc)
	}
	res = mustSet(t, res.Doc, "alias", "日本", `"next"`)
	if !res.Existed || strings.Count(res.Doc, "日本") != 1 || !strings.Contains(res.Doc, "\"日本\" = \"next\"") {
		t.Errorf("the quoted key must be found and replaced in place:\n%s", res.Doc)
	}
	res = mustSet(t, "[alias]\n'has space' = \"ls\"\n", "alias", "has space", `"next"`)
	if !res.Existed || !strings.Contains(res.Doc, "'has space' = \"next\"") {
		t.Errorf("an operator's literal-quoted key must match:\n%s", res.Doc)
	}
}

// A header the operator padded or quoted (`[ lanes ]`, `["lanes"]`,
// `[[ board ]]`) names the same table as `[lanes]`; the editor used to miss it
// and append a second [lanes] — a table defined twice.
func TestSetMatchesPaddedAndQuotedHeaders(t *testing.T) {
	doc := "[ lanes ]\ndefault = \"inbox\"\n\n[\"next\"]\nlanes = [\"ready\"]\n\n[[ board ]]\npath = \"a\"\n"
	res := mustSet(t, doc, "lanes", "default", `"ready"`)
	if !res.Existed || strings.Count(res.Doc, "[ lanes ]") != 1 || strings.Contains(res.Doc, "\n[lanes]") || !strings.Contains(res.Doc, "[ lanes ]\ndefault = \"ready\"") {
		t.Errorf("[ lanes ] must be the lanes table:\n%s", res.Doc)
	}
	res = mustSet(t, doc, "next", "lanes", `["backlog"]`)
	if !res.Existed || !strings.Contains(res.Doc, "[\"next\"]\nlanes = [\"backlog\"]") {
		t.Errorf("[\"next\"] must be the next table:\n%s", res.Doc)
	}
	ares, err := SetInArrayTable(doc, "board", 0, "path", `"b"`)
	if err != nil || !ares.Existed || !strings.Contains(ares.Doc, "[[ board ]]\npath = \"b\"") {
		t.Errorf("[[ board ]] must be the board entry: err=%v\n%s", err, ares.Doc)
	}
}

// A control character in a value is escaped as TOML's \uXXXX, so the written
// document parses and reads the same rune back.
func TestRenderTOMLStringEscapesControlCharacters(t *testing.T) {
	got := RenderTOMLValue("a\x01b\x7fc")
	if got != `"a\u0001b\u007Fc"` {
		t.Fatalf("rendered %s", got)
	}
	if v := ParseTOMLValueText(got); v != "a\x01b\x7fc" {
		t.Errorf("round trip = %q", v)
	}
}

// An empty list member is refused, not dropped: "ready,,in-progress" used
// to write ["ready", "in-progress"] and report "unchanged".
func TestParseCLIValueRefusesAnEmptyListMember(t *testing.T) {
	k := Key{Section: "next", Name: "lanes", Kind: KindStringSlice}
	for _, bad := range []string{"ready,,in-progress", "ready,", ",ready", " , "} {
		if _, err := ParseCLIValue(k, bad); err == nil {
			t.Errorf("ParseCLIValue(%q) must refuse the empty member", bad)
		}
	}
	if v, err := ParseCLIValue(k, ""); err != nil || len(v.([]string)) != 0 {
		t.Errorf("an empty list is still a list: %v %v", v, err)
	}
	if v, err := ParseCLIValue(k, "ready, in-progress"); err != nil || strings.Join(v.([]string), ",") != "ready,in-progress" {
		t.Errorf("a padded list still splits: %v %v", v, err)
	}
}

// UnsetInSection removes the key's whole span (the regression guard's
// baseline); a key that is not there leaves the document alone.
func TestUnsetRemovesTheWholeSpan(t *testing.T) {
	got := UnsetInSection(editFixture, "next", "lanes")
	if strings.Contains(got, "in-progress") || !strings.Contains(got, "[next]\n\n[alias]") {
		t.Errorf("the multi-line span must go:\n%s", got)
	}
	if UnsetInSection(editFixture, "next", "nope") != editFixture || UnsetInSection(editFixture, "nope", "x") != editFixture {
		t.Error("an absent key or section must change nothing")
	}
	doc := "[[board]]\npath = \"a\"\nrepo = \"auto\"\n\n[[board]]\npath = \"b\"\n"
	if got := UnsetInArrayTable(doc, "board", 1)("path"); got != "[[board]]\npath = \"a\"\nrepo = \"auto\"\n\n[[board]]\n" {
		t.Errorf("the second entry's path must go:\n%s", got)
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
	res := mustSet(t, editFixture, "", "mode", `"standalone"`)
	if !strings.Contains(res.Doc, `mode = "standalone"`) || strings.Contains(res.Doc, `mode = "shared"`) {
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
	doc := "# mode = \"shared\"\n\n[lanes]\ndefault = \"inbox\"\n"
	res, err := SetInSection(doc, "", "mode", `"standalone"`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Existed {
		t.Error("a commented-out example must read as absent")
	}
	if !strings.Contains(res.Doc, "# mode = \"shared\"\nmode = \"standalone\"\n") {
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
	if k, _ := find("mode"); k.Kind != KindString || k.Section != "" {
		t.Errorf("mode must be a top-level string, got %+v", k)
	}
	// `mode` retired the only top-level bool, so keep a bool subject in the
	// registry check: kindOf's bool arm is still live for section keys.
	if k, _ := find("labels.required"); k.Kind != KindBool || k.Section != "labels" {
		t.Errorf("labels.required must be a bool in [labels], got %+v", k)
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
