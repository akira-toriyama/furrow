package core

import (
	"strings"
	"testing"
)

func TestContainsFold(t *testing.T) {
	cases := []struct {
		text, term string
		want       bool
	}{
		{"TeaTest boots the program", "teatest", true},
		{"lower", "LOWER", true},
		{"exact", "exact", true},
		{"nothing here", "missing", false},
		{"anything", "", false}, // an empty term never matches (needle required)
		{"", "x", false},
		{"日本語のタスク", "本語", true},
	}
	for _, c := range cases {
		if got := ContainsFold(c.text, c.term); got != c.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", c.text, c.term, got, c.want)
		}
	}
}

func TestSnippet(t *testing.T) {
	cases := []struct {
		name       string
		text, term string
		radius     int
		want       string
	}{
		{
			name:   "match with context both sides truncated",
			text:   "the quick brown fox jumps over the lazy dog",
			term:   "fox",
			radius: 4,
			want:   "…own fox jum…",
		},
		{
			name:   "match at start has no leading ellipsis",
			text:   "fox at the front",
			term:   "fox",
			radius: 3,
			want:   "fox at…",
		},
		{
			name:   "match at end has no trailing ellipsis",
			text:   "at the end is fox",
			term:   "fox",
			radius: 3,
			want:   "…is fox",
		},
		{
			name:   "newlines collapse to single spaces (one-line excerpt)",
			text:   "line one\n\nteatest here\n  indented",
			term:   "teatest",
			radius: 5,
			want:   "…one teatest here…",
		},
		{
			name:   "case-insensitive match preserves original case in excerpt",
			text:   "Boots the Program in a terminal",
			term:   "program",
			radius: 3,
			want:   "…he Program in…",
		},
		{
			name:   "whole text fits within radius",
			text:   "short fox",
			term:   "fox",
			radius: 50,
			want:   "short fox",
		},
		{
			name:   "term absent yields empty string",
			text:   "no needle here",
			term:   "zzz",
			radius: 3,
			want:   "",
		},
		{
			name:   "unicode context counted in runes not bytes",
			text:   "あいうえお teatest かきくけこ",
			term:   "teatest",
			radius: 2,
			want:   "…お teatest か…",
		},
	}
	for _, c := range cases {
		if got := Snippet(c.text, c.term, c.radius); got != c.want {
			t.Errorf("%s: Snippet(%q, %q, %d) = %q, want %q", c.name, c.text, c.term, c.radius, got, c.want)
		}
	}
}

func TestRegexNeedle(t *testing.T) {
	n, err := RegexNeedle(`\bB\b`)
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]bool{
		"候補 B":     true,
		"手伝いB":     true, // kana is \W to RE2, so it is a boundary
		"B社":       true,
		"BGM":      false,
		"plan b":   true, // case folds by default
		"subtotal": false,
	} {
		if got := n.In(text); got != want {
			t.Errorf("In(%q) = %v, want %v", text, got, want)
		}
	}
	if cs, _ := RegexNeedle(`(?-i)\bB\b`); cs.In("plan b") {
		t.Error("(?-i) must clear the default case folding")
	}
	ml, _ := RegexNeedle(`(?m)^- 10/10`)
	if !ml.In("intro\n- 10/10 booked") || ml.In("intro - 10/10 booked") {
		t.Error("(?m)^ must anchor at a line start of the raw text")
	}
}

func TestRegexNeedleRefusals(t *testing.T) {
	for _, p := range []string{``, `(unclosed`, `a*`, `^`, `x?`} {
		if _, err := RegexNeedle(p); AsError(err) == nil || AsError(err).Code != CodeValidation {
			t.Errorf("RegexNeedle(%q) = %v, want a validation error", p, err)
		}
	}
	// The fault is named on the caller's spelling, never the flag-prefixed one.
	_, err := RegexNeedle(`(unclosed`)
	if e := AsError(err); e == nil || strings.Contains(e.Msg, "(?i)") || !strings.Contains(e.Msg, "missing closing )") {
		t.Errorf("a syntax error must quote the caller's pattern without the (?i) prefix, got %v", err)
	}
}

// A regex snippet is located on the RAW text — so a line anchor still finds its
// line — and the window is collapsed to one line after.
func TestNeedleSnippetRegex(t *testing.T) {
	n, _ := RegexNeedle(`(?m)^- 10/10`)
	got := n.Snippet("## 前提\n\nintro line\n- 10/10 booked\n  and more", 7)
	if want := "…o line - 10/10 booked…"; got != want {
		t.Errorf("Snippet = %q, want %q", got, want)
	}
	if got := n.Snippet("no match here", 6); got != "" {
		t.Errorf("a miss must yield \"\", got %q", got)
	}
	// Context is counted in collapsed text, as the substring Snippet counts it,
	// so a whitespace run neither eats the radius nor earns an ellipsis.
	body := "alpha\n\n\n\n\n\nfoo\n\n\n\n\n\nomega"
	foo, _ := RegexNeedle(`fo+`)
	if got, want := foo.Snippet(body, 4), Snippet(body, "foo", 4); got != want {
		t.Errorf("regex Snippet = %q, want the substring shape %q", got, want)
	}
	if got := foo.Snippet("\n\n\n\nfoo bar", 2); got != "foo b…" {
		t.Errorf("leading whitespace must not earn an ellipsis, got %q", got)
	}
	// A long match is cut at two context windows, so `.*` cannot return the body.
	greedy, _ := RegexNeedle(`(?s)start.*`)
	if got := []rune(greedy.Snippet("start "+strings.Repeat("x", 500), 10)); len(got) > 2*10+10+1 {
		t.Errorf("a long match must stay bounded, got %d runes", len(got))
	}
	// The substring needle keeps the package Snippet's output exactly.
	s := SubstringNeedle("fox")
	if got, want := s.Snippet("the quick brown fox jumps over", 4), Snippet("the quick brown fox jumps over", "fox", 4); got != want {
		t.Errorf("substring Needle.Snippet = %q, want Snippet's %q", got, want)
	}
}

func TestFindTextBodyError(t *testing.T) {
	boom := Internalf("", "boom")
	task := &Task{Title: "x"}
	if _, _, err := FindText(task, SubstringNeedle("zz"), func() (string, error) { return "", boom }); err != boom {
		t.Errorf("a body load failure must surface, got %v", err)
	}
	// A shard-field hit never calls body, so a broken body cannot fail it.
	task.Refs = []string{"zz.md"}
	if f, _, err := FindText(task, SubstringNeedle("zz"), func() (string, error) { return "", boom }); err != nil || f != FieldRefs {
		t.Errorf("a ref hit must not read the body: field=%q err=%v", f, err)
	}
}
