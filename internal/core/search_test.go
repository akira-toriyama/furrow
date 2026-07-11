package core

import "testing"

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
