package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// GlobalBoard is one entry of the user-level central-board config: a single
// central .furrow that backs many repos WITHOUT a per-repo .furrow-pointer.toml.
// Several boards may be configured ([[board]] is an array of tables); the app
// layer activates whichever board's scope most specifically encloses the cwd.
// The config is read from ${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml (the
// path is the app layer's job to compute, like Load and LoadPointer take a path).
type GlobalBoard struct {
	Path       string   // path to the central .furrow (~, relative to the config file, or absolute)
	Scopes     []string // activate only when cwd is under one of these dirs (at least one, post-clamp)
	Label      string   // "auto" (nearest git repo basename) | "" (none) | a literal label
	AutoFilter bool     // auto-filter reads (ls/next/revisit) by Label; defaults to true when omitted
}

type rawGlobal struct {
	Boards []rawBoard `toml:"board"`
}

type rawBoard struct {
	Path   string   `toml:"path"`
	Scopes []string `toml:"scopes"`
	Label  string   `toml:"label"`
	// AutoFilter is a pointer so an omitted key is distinguishable from an
	// explicit false: nil clamps to the true default, set honors the value.
	AutoFilter *bool `toml:"auto_filter"`
}

// LoadGlobalBoards parses the user-level furrow config at path and returns its
// [[board]] entries, or nil when there is no usable default board.
//
// Unlike LoadPointer (which fails loud on a missing board, because a pointer is
// an explicit per-repo redirect), the central board is CLAMP-DON'T-REJECT like
// Load: it is ambient and affects every repo, so a half-written file must never
// break furrow in an unrelated directory. Specifically:
//   - a missing/empty file (no [[board]]) yields (nil, nil, nil) — no board.
//   - malformed TOML is an error.
//   - each [[board]] is clamped on its own: an entry with no path, or no scopes
//     once blank strings are pruned, is dropped with a warning.
//   - if every entry is dropped the result is (nil, warn, nil) — "no board".
//
// A legacy single [board] table decodes (via go-toml/v2) into a one-element
// slice whose old `scope` key is silently ignored; with no `scopes` it clamps
// away here — the accepted rollout-window degradation (a v2 binary on a v1
// config simply runs without a central board until the config is migrated).
//
// Resolving each board path, selecting the most specific scope, and checking
// that the chosen board exists are the caller's job (only the app layer knows
// cwd and the config file's directory).
func LoadGlobalBoards(path string) ([]GlobalBoard, []string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var r rawGlobal
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, nil, fmt.Errorf("furrow config.toml: %w", err)
	}
	var boards []GlobalBoard
	var warn []string
	for i, b := range r.Boards {
		if b.Path == "" {
			warn = append(warn, fmt.Sprintf("%s: [[board]] #%d has no path; ignoring it", path, i+1))
			continue
		}
		scopes := nonBlank(b.Scopes)
		if len(scopes) == 0 {
			warn = append(warn, fmt.Sprintf("%s: [[board]] %q has no scopes; ignoring it", path, b.Path))
			continue
		}
		autoFilter := b.AutoFilter == nil || *b.AutoFilter // omitted -> true
		boards = append(boards, GlobalBoard{Path: b.Path, Scopes: scopes, Label: b.Label, AutoFilter: autoFilter})
	}
	if len(boards) == 0 {
		return nil, warn, nil
	}
	return boards, warn, nil
}

// nonBlank returns the non-empty elements of ss, or nil when none remain. The
// result is a fresh slice, so callers never alias the decoded TOML.
func nonBlank(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
