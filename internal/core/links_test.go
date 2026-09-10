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
		{"inline code span is not a link", "document the notation with `[[t-x]]`", nil},
		{"double-backtick code span is not a link", "use ``[[t-abc]]`` as an example", nil},
		{"fenced block is not a link", "```\nexample: [[t-abc]]\n```", nil},
		{"real link in prose survives a nearby code span", "see [[t-9zz]] but not `[[t-x]]`", []string{"t-9zz"}},
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

// UnlinkIDs turns the named [[id]]s into bare ids outside code, leaves other
// links and every code-quoted example verbatim, and counts what it changed.
func TestUnlinkIDs(t *testing.T) {
	re := LinkPattern("t-")
	in := "see [[t-aa]] and [[t-bb]], `[[t-aa]]` stays\n```\n[[t-aa]]\n```\n[[t-aa]] again"
	got, n := UnlinkIDs(in, re, map[string]bool{"t-aa": true})
	want := "see t-aa and [[t-bb]], `[[t-aa]]` stays\n```\n[[t-aa]]\n```\nt-aa again"
	if got != want || n != 2 {
		t.Errorf("UnlinkIDs = %q (n=%d), want %q (n=2)", got, n, want)
	}
	if links := ExtractLinks(got, re); len(links) != 1 || links[0] != "t-bb" {
		t.Errorf("links after unlink = %v, want [t-bb]", links)
	}
}
