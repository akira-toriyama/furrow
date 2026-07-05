// Package version carries the build identity, overridden at release time via
// -ldflags "-X github.com/akira-toriyama/furrow/internal/version.<Field>=...".
// Defaults describe a from-source build: Version "dev", with Commit/Date left
// for the runtime/debug VCS stamp to fill in (see build.sh / .goreleaser.yaml).
package version

import (
	"runtime/debug"
	"strings"
)

// Build identity, injected by the linker at release/install time. Do not set
// these here: "dev" with empty Commit/Date is the only shape a bare `go build`
// starts from — Resolve then backfills Commit/Date from the Go VCS stamp.
var (
	// Version is the semver tag (or `git describe`) of the build; "dev" from source.
	Version = "dev"
	// Commit is the full VCS revision the binary was built from.
	Commit = ""
	// Date is the commit/build timestamp (RFC 3339).
	Date = ""
)

// Info is the resolved build identity: the ldflags values, with Commit/Date
// backfilled from the Go build's VCS stamp when they were not injected (so even
// a plain `go build` binary can report which commit it came from).
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Modified bool   `json:"modified"`
}

// Resolve reads the injected build vars and, when Commit or Date is missing,
// falls back to runtime/debug.ReadBuildInfo (vcs.revision/vcs.time/vcs.modified).
// It never panics and is safe to call repeatedly.
func Resolve() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date}
	if info.Commit != "" && info.Date != "" {
		return info
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				info.Modified = true
			}
		}
	}
	return info
}

// String renders the human one-liner shared by `furrow version` and
// `furrow --version`, e.g. "furrow v1.2.3 (abc1234, 2026-07-03T00:00:00Z)".
// The commit is shortened for readability; the full sha stays in the JSON form.
func (i Info) String() string {
	var b strings.Builder
	b.WriteString("furrow ")
	b.WriteString(i.Version)
	if i.Commit == "" {
		return b.String()
	}
	b.WriteString(" (")
	b.WriteString(shortCommit(i.Commit))
	if i.Modified {
		b.WriteString("-dirty")
	}
	if i.Date != "" {
		b.WriteString(", ")
		b.WriteString(i.Date)
	}
	b.WriteString(")")
	return b.String()
}

// shortCommit trims a full sha to 7 chars for human display, leaving shorter
// values (and the empty string) untouched.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
