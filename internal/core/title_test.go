package core

import (
	"strings"
	"testing"
)

func TestNormalizeTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain title", "plain title"},
		{"  trim me  ", "trim me"},
		{"line\nbreak", "line break"},
		{"crlf\r\nhere", "crlf here"},
		{"tab\tsep", "tab sep"},
		{"inject\n## Heading", "inject ## Heading"},
		{"nul\x00bell\x07x", "nul bell x"},
		{"multi   space", "multi space"},
		{"\n\n only newlines \n", "only newlines"},
		{"unicode ✨ kept", "unicode ✨ kept"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeTitle(c.in); got != c.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTitleHasControl(t *testing.T) {
	for _, s := range []string{"a\nb", "x\ty", "z\x00", "cr\rlf"} {
		if !TitleHasControl(s) {
			t.Errorf("TitleHasControl(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"clean title", "unicode ✨ ok", ""} {
		if TitleHasControl(s) {
			t.Errorf("TitleHasControl(%q) = true, want false", s)
		}
	}
}

// A format character (Cf) renders nothing and steers the terminal — U+202E
// flipped every row after it in `ls` — so NormalizeTitle drops it and
// TitleHasControl (lint's backstop for a hand-edited shard) sees it.
func TestNormalizeTitleDropsFormatCharacters(t *testing.T) {
	in := "fix \u202Elogin\u200B page"
	if got := NormalizeTitle(in); got != "fix login page" {
		t.Errorf("NormalizeTitle(%q) = %q, want the format characters dropped", in, got)
	}
	if !TitleHasControl(in) {
		t.Error("TitleHasControl must see a format character")
	}
	if why := TitleTooLong(strings.Repeat("x", MaxTitleLen)); why != "" {
		t.Errorf("a title at the cap fits: %s", why)
	}
	if why := TitleTooLong(strings.Repeat("字", MaxTitleLen+1)); why == "" {
		t.Error("a title past the cap, counted in characters, is refused")
	}
}
