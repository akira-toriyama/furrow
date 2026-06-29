package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// GlobalBoard is the optional user-level default board: a single central
// .furrow that backs many repos WITHOUT a per-repo .furrow-pointer.toml. It is
// read from ${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml (the path is the
// app layer's job to compute, like Load and LoadPointer take a path).
type GlobalBoard struct {
	Path  string // path to the central .furrow (~, relative to the config file, or absolute)
	Scope string // activate only when cwd is under this dir ("" = derive from the board's repo parent)
	Label string // "auto" (nearest git repo basename) | "" (none) | a literal label
}

type rawGlobal struct {
	Board struct {
		Path  string `toml:"path"`
		Scope string `toml:"scope"`
		Label string `toml:"label"`
	} `toml:"board"`
}

// LoadGlobalBoard parses the user-level furrow config at path and returns its
// [board] table, or nil when there is no usable default board.
//
// Unlike LoadPointer (which fails loud on a missing board, because a pointer is
// an explicit per-repo redirect), the global board is CLAMP-DON'T-REJECT like
// Load: it is ambient and affects every repo, so a half-written file must never
// break furrow in an unrelated directory. Specifically:
//   - missing file        -> (nil, nil, nil)   — no default board configured
//   - malformed TOML       -> error            — the user wrote broken TOML
//   - present, no [board].path -> (nil, warn, nil) — clamp to "no default board"
//
// Resolving the board path and checking that it exists is the caller's job
// (only the app layer knows cwd and the config file's directory).
func LoadGlobalBoard(path string) (*GlobalBoard, []string, error) {
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
	if r.Board.Path == "" {
		return nil, []string{fmt.Sprintf("%s: [board].path is empty; ignoring the default board", path)}, nil
	}
	return &GlobalBoard{Path: r.Board.Path, Scope: r.Board.Scope, Label: r.Board.Label}, nil, nil
}
