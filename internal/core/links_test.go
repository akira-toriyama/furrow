package core

import (
	"reflect"
	"testing"
)

func TestExtractLinks(t *testing.T) {
	re := LinkPattern("t-")
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"single ref", "see [[t-abc]] for context", []string{"t-abc"}},
		{"multiple refs in order", "[[t-9zz]] then [[t-abc]]", []string{"t-9zz", "t-abc"}},
		{"dedupes, keeps first-seen order", "[[t-abc]] and again [[t-abc]] and [[t-9zz]]", []string{"t-abc", "t-9zz"}},
		{"bare ids are not links", "t-abc is mentioned but not linked", nil},
		{"non-id bracket content ignored", "[[not-a-task]] and [[wiki page]]", nil},
		{"legacy numeric ids", "[[t-0042]]", []string{"t-0042"}},
		{"no links", "just prose", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractLinks(c.text, re)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ExtractLinks(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestLinkPatternHonorsPrefix(t *testing.T) {
	// A non-default id prefix must drive the [[...]] shape too, so the notation
	// tracks whatever [ids].prefix the board is configured with.
	re := LinkPattern("issue-")
	got := ExtractLinks("[[issue-7]] but not [[t-abc]]", re)
	want := []string{"issue-7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with prefix issue-: got %v, want %v", got, want)
	}
}
