package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Pointer is a repo-local .furrow-pointer.toml: it redirects furrow at a central
// board and optionally scopes every command to one repo. It is the central-board
// counterpart to a repo-local .furrow.
type Pointer struct {
	Board string // path to the central .furrow (relative to the pointer file, ~, or absolute)
	// DefaultRepo scopes the repo to one owner/repo: unioned into a task's
	// repos on add and auto-filtered on reads. "auto" derives it from the
	// pointer's checkout (git origin URL); "" = redirect only.
	DefaultRepo string
}

type rawPointer struct {
	Board       string `toml:"board"`
	DefaultRepo string `toml:"default_repo"`
	// DefaultLabel is the RETIRED pre-repos key, decoded only to warn about it
	// (a tombstone, not a compat shim): silently ignoring it would silently
	// un-scope the repo.
	DefaultLabel string `toml:"default_label"`
}

// LoadPointer parses a .furrow-pointer.toml. Unlike config.Load it does NOT
// clamp: a pointer with no board is useless and a malformed pointer must fail
// loudly rather than silently send writes to the wrong store. The warnings are
// stderr-bound notes (currently only the retired default_label tombstone).
// Resolving the board path (relative/~/abs) and checking it exists is the
// caller's job — only the caller knows the pointer file's directory.
func LoadPointer(path string) (*Pointer, []string, error) {
	// #nosec G304 -- path is a furrow .furrow-pointer.toml discovered by the
	// app layer walking up from cwd, not attacker-supplied.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var r rawPointer
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, nil, fmt.Errorf("malformed pointer TOML: %w", err)
	}
	if r.Board == "" {
		return nil, nil, fmt.Errorf("pointer is missing required key `board`")
	}
	var warn []string
	if r.DefaultLabel != "" {
		warn = append(warn, fmt.Sprintf("%s: default_label was replaced by default_repo; update the pointer (key ignored)", path))
	}
	return &Pointer{Board: r.Board, DefaultRepo: r.DefaultRepo}, warn, nil
}
