package core

import "testing"

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
