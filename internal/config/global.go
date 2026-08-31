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
	Path   string   // path to the central .furrow (~, relative to the config file, or absolute)
	Scopes []string // activate only when cwd is under one of these dirs (at least one, post-clamp)
	// Repo is the board's scope repo: "auto" (derive owner/repo from the
	// enclosing checkout's git origin URL, worktree-aware, with a ghq-style path
	// fallback) | "" (none) | a literal owner/repo. The app layer derives it —
	// config only carries the mode.
	Repo string
	// Label is a LITERAL tag `add` unions into a task's labels ("" = none) —
	// like a GitHub Issues label auto-applied per board. It never filters
	// reads; scoping moved to Repo. "auto" is a reserved word (the retired
	// label="auto" mode), warned about and ignored below.
	Label      string
	AutoFilter bool // auto-filter reads by the board repo; defaults to true when omitted
	// AutoCommit, when true, makes every mutating furrow command git-commit the
	// board's .furrow/ afterwards (best-effort, no push) — the standalone-board
	// backup guarantee turned into a tool behavior. It lives here (per-machine
	// user config), NOT in the board's committed config.toml, so one operator's
	// choice never propagates to every clone or to CI. Default false.
	AutoCommit bool
}

type rawGlobal struct {
	Boards []rawBoard `toml:"board"`
}

type rawBoard struct {
	Path   string   `toml:"path"`
	Scopes []string `toml:"scopes"`
	Repo   string   `toml:"repo"`
	Label  string   `toml:"label"`
	// AutoFilter is a pointer so an omitted key is distinguishable from an
	// explicit false: nil clamps to the true default, set honors the value.
	AutoFilter *bool `toml:"auto_filter"`
	// AutoCommit defaults FALSE, so a plain bool suffices (the zero value is the
	// default) — unlike AutoFilter, no omitted-vs-explicit-false distinction is
	// needed.
	AutoCommit bool `toml:"autocommit"`
}

// LoadGlobalBoards parses the user-level furrow config at path and returns its
// [[board]] entries, or nil when there is no usable default board.
//
// Unlike LoadPointer (which fails loud on a missing board, because a pointer is
// an explicit per-repo redirect), the central board is CLAMP-DON'T-REJECT like
// Load: it is ambient and affects every repo, so a half-written file must never
// break furrow in an unrelated directory. Specifically:
//   - a missing/empty file (no [[board]]) yields (nil, nil, nil) — no board.
//   - TOML that does not PARSE (a syntax error) is an error — nothing can be
//     salvaged from a file with no readable structure.
//   - an unknown key is ignored with a warning naming the file, line, and key
//     (strict decode, same as Load).
//   - a TYPE error is NOT a file-wide error: the file is re-read leniently and
//     only the [[board]] entries whose keys hold the wrong type are dropped,
//     each with a warning naming the file, entry, and key. A wrong-typed value
//     in one board must not take furrow down for every repo on the machine —
//     that is the policy the paragraph above declares, applied to the failure
//     mode it used to miss.
//   - each surviving [[board]] is clamped on its own: an entry with no path, or
//     no scopes once blank strings are pruned, is dropped with a warning.
//   - if every entry is dropped the result is (nil, warn, nil) — "no board".
//
// A legacy single [board] table decodes (via go-toml/v2) into a one-element
// slice; its old `scope` key now WARNS as unknown, and with no `scopes` the
// entry clamps away here — the accepted rollout-window degradation (a v2 binary
// on a v1 config simply runs without a central board until the config is
// migrated).
//
// Resolving each board path, selecting the most specific scope, and checking
// that the chosen board exists are the caller's job (only the app layer knows
// cwd and the config file's directory).
func LoadGlobalBoards(path string) ([]GlobalBoard, []string, error) {
	// #nosec G304 -- path is furrow's own user-level config location
	// (~/.config/furrow/config.toml), resolved by the app layer, not attacker-supplied.
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return LoadGlobalBoardsBytes(data, path)
}

// LoadGlobalBoardsBytes is LoadGlobalBoards over in-memory content — the
// validation half of `config set --user`.
func LoadGlobalBoardsBytes(data []byte, path string) ([]GlobalBoard, []string, error) {
	var r rawGlobal
	unknown, err := decodeStrict(data, &r, path)
	if err != nil {
		// A type error somewhere in the file. Salvage what still reads: the
		// lenient pass below drops only the broken entries. A syntax error
		// falls through it unchanged (nothing decodes at all).
		entries, warn, salvageErr := salvageGlobalBoards(data, path)
		if salvageErr != nil {
			return nil, nil, fmt.Errorf("furrow config.toml: %w", err)
		}
		boards, clampWarn := clampGlobalBoards(entries, path)
		return boards, append(warn, clampWarn...), nil
	}
	boards, warn := clampGlobalBoards(r.Boards, path)
	return boards, append(unknown, warn...), nil
}

// salvageGlobalBoards is the lenient second pass behind LoadGlobalBoards: it
// re-reads the file as untyped TOML and coerces each [[board]] entry field by
// field, dropping ONLY the entries that hold a wrong-typed value (with a
// warning naming the file, entry, and key) so the healthy boards survive.
// Positions are gone on this path — go-toml reports them only while decoding
// into the typed struct, which is what just failed — so the warnings name the
// entry by index instead of by line.
func salvageGlobalBoards(data []byte, path string) ([]rawBoard, []string, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	var items []any
	switch v := doc["board"].(type) {
	case nil:
		return nil, nil, nil
	case []any: // [[board]] array of tables
		items = v
	case []map[string]any: // go-toml may type a homogeneous array this way
		for _, m := range v {
			items = append(items, m)
		}
	case map[string]any: // legacy single [board] table
		items = []any{v}
	default:
		return nil, []string{fmt.Sprintf("%s: `board` is not a [[board]] table; ignoring it", path)}, nil
	}
	var entries []rawBoard
	var warn []string
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			warn = append(warn, fmt.Sprintf("%s: [[board]] #%d is not a table; ignoring this board", path, i+1))
			continue
		}
		b, w := coerceBoard(m, path, i+1)
		warn = append(warn, w...)
		if b != nil {
			entries = append(entries, *b)
		}
	}
	return entries, warn, nil
}

// coerceBoard converts one untyped [[board]] table into a rawBoard. A key
// holding the wrong type poisons the ENTRY, not the file (nil + a warning
// naming file, entry number, and key); an unknown key only warns, exactly like
// the strict path — just without a line number.
func coerceBoard(m map[string]any, path string, n int) (*rawBoard, []string) {
	var b rawBoard
	var warn []string
	bad := func(key string) (*rawBoard, []string) {
		return nil, append(warn, fmt.Sprintf("%s: [[board]] #%d: key %q has the wrong type; ignoring this board", path, n, key))
	}
	for key, v := range m {
		switch key {
		case "path":
			s, ok := v.(string)
			if !ok {
				return bad(key)
			}
			b.Path = s
		case "scopes":
			items, ok := v.([]any)
			if !ok {
				return bad(key)
			}
			for _, it := range items {
				s, ok := it.(string)
				if !ok {
					return bad(key)
				}
				b.Scopes = append(b.Scopes, s)
			}
		case "repo":
			s, ok := v.(string)
			if !ok {
				return bad(key)
			}
			b.Repo = s
		case "label":
			s, ok := v.(string)
			if !ok {
				return bad(key)
			}
			b.Label = s
		case "auto_filter":
			f, ok := v.(bool)
			if !ok {
				return bad(key)
			}
			b.AutoFilter = &f
		case "autocommit":
			c, ok := v.(bool)
			if !ok {
				return bad(key)
			}
			b.AutoCommit = c
		default:
			warn = append(warn, fmt.Sprintf("%s: [[board]] #%d: unknown key %q; ignored", path, n, key))
		}
	}
	return &b, warn
}

// clampGlobalBoards applies the per-entry clamp rules shared by the strict and
// salvage decode paths: no path or no scopes drops the entry with a warning,
// the retired label="auto" tombstone warns and clears, and an omitted
// auto_filter defaults to true.
func clampGlobalBoards(entries []rawBoard, path string) ([]GlobalBoard, []string) {
	var boards []GlobalBoard
	var warn []string
	for i, b := range entries {
		if b.Path == "" {
			warn = append(warn, fmt.Sprintf("%s: [[board]] #%d has no path; ignoring it", path, i+1))
			continue
		}
		scopes := nonBlank(b.Scopes)
		if len(scopes) == 0 {
			warn = append(warn, fmt.Sprintf("%s: [[board]] %q has no scopes; ignoring it", path, b.Path))
			continue
		}
		label := b.Label
		if label == "auto" {
			// Tombstone, not a compat shim: the retired label="auto" mode is
			// ignored (never treated as a literal tag), loudly — so the window
			// between this release and the user's config switch cannot silently
			// change what a scoped board means.
			warn = append(warn, fmt.Sprintf("%s: [[board]] %q: label=\"auto\" moved to repo=\"auto\"; update your config (label ignored)", path, b.Path))
			label = ""
		}
		autoFilter := b.AutoFilter == nil || *b.AutoFilter
		boards = append(boards, GlobalBoard{Path: b.Path, Scopes: scopes, Repo: b.Repo, Label: label, AutoFilter: autoFilter, AutoCommit: b.AutoCommit})
	}
	return boards, warn
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

// UserBoardEntry is a [[board]] entry in FILE ORDER, unclamped — what `config
// set --user` resolves a --board ref against and what maps a ref to the entry
// index the surgical editor targets. Clamping would shift indices off the
// file's own order, so this projection deliberately skips it; a file that does
// not decode STRICTLY (type errors included) is an error here — the writer
// refuses to edit a document it cannot fully read, where the reader would
// salvage.
type UserBoardEntry struct {
	Path   string
	Scopes []string
}

// UserBoardEntries parses the user config's [[board]] entries for the writer:
// strict, file-ordered, unclamped. A missing file is (nil, nil) — nothing to
// edit is the caller's message to give.
func UserBoardEntries(data []byte, path string) ([]UserBoardEntry, error) {
	var r rawGlobal
	if _, err := decodeStrict(data, &r, path); err != nil {
		return nil, fmt.Errorf("furrow config.toml: %w", err)
	}
	out := make([]UserBoardEntry, 0, len(r.Boards))
	for _, b := range r.Boards {
		out = append(out, UserBoardEntry{Path: b.Path, Scopes: b.Scopes})
	}
	return out, nil
}
