package version

import (
	"os"
	"path/filepath"
	"regexp"
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

// repoFile reads a file at the repository root. This package sits two
// directories below it; a move breaks the test loudly rather than silently.
func repoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// TestModulePathCarriesTheMajorAndReadmeSpellsIt holds the two halves of Go's
// major-version rule together: from v2 on, the module path must end in /vN,
// and every documented `go install` must spell that same path.
//
// Missed through v5→v9: go.mod never grew the suffix, so the proxy stayed
// frozen at v1.0.0 — `go install …@latest` handed out a v1 binary that then
// failed every shared board with schema-too-new, and `@v6.0.0` 404ed with
// "module path must match major version". Nothing here reads tags: a v10 tag
// cut over a /v9 go.mod still passes; scripts/check-version-lockstep.sh is
// where tag-vs-tree lives.
func TestModulePathCarriesTheMajorAndReadmeSpellsIt(t *testing.T) {
	m := regexp.MustCompile(`(?m)^module (\S+)$`).FindStringSubmatch(repoFile(t, "go.mod"))
	if m == nil {
		t.Fatalf("go.mod declares no module path")
	}
	path := m[1]

	if !regexp.MustCompile(`/v([2-9]|[1-9][0-9]+)$`).MatchString(path) {
		t.Fatalf("go.mod's module path is %q, which carries no /vN major suffix. Go treats a "+
			"path without one as v0/v1 forever: the proxy serves v1.0.0 to `go install @latest` "+
			"and rejects the real tags. Cutting a v2+ tag does not change that — go.mod does", path)
	}

	readme := repoFile(t, "README.md")
	installs := regexp.MustCompile(`(?m)^go install (\S+)@`).FindAllStringSubmatch(readme, -1)
	if len(installs) == 0 {
		t.Fatalf("README.md documents no `go install` line; if the install route was dropped on " +
			"purpose, drop this half of the guard with it")
	}
	for _, in := range installs {
		if !strings.HasPrefix(in[1], path+"/") {
			t.Errorf("README.md documents `go install %s@…`, which is not under the module path "+
				"%q. That command installs whatever the unsuffixed path last resolved to — v1.0.0 — "+
				"not this tree", in[1], path)
		}
	}
}
