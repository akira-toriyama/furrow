package core

import (
	"reflect"
	"testing"
)

func TestRepoMatches(t *testing.T) {
	universe := []string{
		"akira-toriyama/furrow",
		"akira-toriyama/chord",
		"other-org/furrow",
		"other-org/facet.js",
	}
	cases := []struct {
		name string
		q    string
		uni  []string
		want []string
	}{
		{
			name: "short name unique",
			q:    "chord",
			uni:  universe,
			want: []string{"akira-toriyama/chord"},
		},
		{
			name: "short name is case-insensitive",
			q:    "CHORD",
			uni:  universe,
			want: []string{"akira-toriyama/chord"},
		},
		{
			name: "short name ambiguous returns all candidates sorted",
			q:    "furrow",
			uni:  universe,
			want: []string{"akira-toriyama/furrow", "other-org/furrow"},
		},
		{
			name: "full owner/repo matches exactly",
			q:    "akira-toriyama/furrow",
			uni:  universe,
			want: []string{"akira-toriyama/furrow"},
		},
		{
			name: "full owner/repo match is case-insensitive, returns canonical casing",
			q:    "Akira-Toriyama/Furrow",
			uni:  universe,
			want: []string{"akira-toriyama/furrow"},
		},
		{
			// The suffix must sit at a "/" boundary: "furrow" must NOT match
			// "org/my-furrow" (the char before the suffix is "-", not "/").
			name: "suffix only matches at a / boundary",
			q:    "furrow",
			uni:  []string{"org/my-furrow"},
			want: nil,
		},
		{
			name: "no match",
			q:    "ghost",
			uni:  universe,
			want: nil,
		},
		{
			name: "empty query matches nothing",
			q:    "",
			uni:  universe,
			want: nil,
		},
		{
			name: "duplicate universe entries are deduped",
			q:    "chord",
			uni:  []string{"akira-toriyama/chord", "akira-toriyama/chord"},
			want: []string{"akira-toriyama/chord"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RepoMatches(c.q, c.uni)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("RepoMatches(%q) = %v, want %v", c.q, got, c.want)
			}
		})
	}
}

func TestIsRepoShaped(t *testing.T) {
	for _, ok := range []string{"akira-toriyama/furrow", "o/r", "org/facet.js", "a-b/c_d-e"} {
		if !IsRepoShaped(ok) {
			t.Errorf("IsRepoShaped(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"furrow", "org/", "/repo", "-org/repo", "org-/repo", "a/b/c", "http://x/y", "o r/x"} {
		if IsRepoShaped(bad) {
			t.Errorf("IsRepoShaped(%q) = true, want false", bad)
		}
	}
}
