package version

import (
	"strings"
	"testing"
)

// A from-source build (no ldflags) reports "dev"; Resolve never panics and its
// human string always leads with the binary name.
func TestResolveDefault(t *testing.T) {
	info := Resolve()
	if info.Version != "dev" {
		t.Errorf("Version = %q, want %q (this test binary is un-stamped)", info.Version, "dev")
	}
	if !strings.HasPrefix(info.String(), "furrow ") {
		t.Errorf("String() = %q, want it to start with %q", info.String(), "furrow ")
	}
}

// String() renders a stamped build as "furrow <version> (<short-commit>, <date>)"
// — the commit is shortened for humans while JSON keeps the full sha.
func TestInfoStringStamped(t *testing.T) {
	info := Info{Version: "v1.2.3", Commit: "abc1234def5678", Date: "2026-07-03T00:00:00Z"}
	got := info.String()
	want := "furrow v1.2.3 (abc1234, 2026-07-03T00:00:00Z)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// A commit with no date still renders (no trailing ", ").
func TestInfoStringCommitOnly(t *testing.T) {
	info := Info{Version: "dev", Commit: "abc1234def5678"}
	got := info.String()
	want := "furrow dev (abc1234)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// A dirty working tree marks the short commit with -dirty.
func TestInfoStringModified(t *testing.T) {
	info := Info{Version: "dev", Commit: "abc1234def5678", Date: "2026-07-03T00:00:00Z", Modified: true}
	got := info.String()
	want := "furrow dev (abc1234-dirty, 2026-07-03T00:00:00Z)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// With neither commit nor date, String() is just the name + version.
func TestInfoStringBare(t *testing.T) {
	info := Info{Version: "dev"}
	if got := info.String(); got != "furrow dev" {
		t.Errorf("String() = %q, want %q", got, "furrow dev")
	}
}

// shortCommit truncates to 7 chars but leaves shorter values (and "") intact.
func TestShortCommit(t *testing.T) {
	cases := map[string]string{
		"abc1234def5678": "abc1234",
		"abc12":          "abc12",
		"":               "",
	}
	for in, want := range cases {
		if got := shortCommit(in); got != want {
			t.Errorf("shortCommit(%q) = %q, want %q", in, got, want)
		}
	}
}
