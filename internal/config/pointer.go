package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Pointer is a repo-local .furrow-pointer.toml: it redirects furrow at a central
// board and optionally scopes every command to one label (the repo's name on a
// shared tracker). It is the central-board counterpart to a repo-local .furrow.
type Pointer struct {
	Board        string // path to the central .furrow (relative to the pointer file, ~, or absolute)
	DefaultLabel string // label auto-applied on add and auto-filtered on reads ("" = redirect only)
}

type rawPointer struct {
	Board        string `toml:"board"`
	DefaultLabel string `toml:"default_label"`
}

// LoadPointer parses a .furrow-pointer.toml. Unlike config.Load it does NOT
// clamp: a pointer with no board is useless and a malformed pointer must fail
// loudly rather than silently send writes to the wrong store. Resolving the
// board path (relative/~/abs) and checking it exists is the caller's job — only
// the caller knows the pointer file's directory.
func LoadPointer(path string) (*Pointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r rawPointer
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("malformed pointer TOML: %w", err)
	}
	if r.Board == "" {
		return nil, fmt.Errorf("pointer is missing required key `board`")
	}
	return &Pointer{Board: r.Board, DefaultLabel: r.DefaultLabel}, nil
}
