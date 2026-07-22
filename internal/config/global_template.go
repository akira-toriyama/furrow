package config

import (
	"strconv"
	"strings"
)

// Placeholder values in GlobalTemplate that RenderGlobalConfig swaps for the
// context-derived board path and scopes. They are deliberately unique tokens so
// a single literal replace is unambiguous (and keeps the surrounding alignment).
const (
	placeholderBoardPath = `"/path/to/central/.furrow"`
	placeholderScopes    = `["/path/to/the/tree/it/backs"]`
)

// GlobalTemplate is the canonical ~/.config/furrow/config.toml written by
// `furrow config init` (with placeholder values when it can't derive a board
// from context) and mirrored at the repo root as config.global.toml. furrow only
// ever READS this file; a half-written entry is clamped away with a warning
// (`furrow lint` and `furrow config path` report it), never an error.
//
// Keep this in sync with the repo-root config.global.toml (a from-source
// reference); `sh scripts/check.sh` guards the two against drift.
const GlobalTemplate = `# furrow — user-level configuration (~/.config/furrow/config.toml).
# https://github.com/akira-toriyama/furrow
#
# This file is PER-MACHINE — unlike a board's own .furrow/config.toml (which is
# committed and shared, holding lane/id rules), it says which CENTRAL board backs
# the repos under your tree. A central board is one .furrow that many repos use
# WITHOUT each carrying its own .furrow or a .furrow-pointer.toml.
#
# furrow only READS this file; a half-written [[board]] is clamped away with a
# warning (` + "`furrow lint`" + ` and ` + "`furrow config path`" + ` report it), never an error, so a
# typo here can't break furrow in an unrelated directory.
#
# Write it with ` + "`furrow config init`" + ` (it fills path/scopes in from the nearest
# .furrow when run inside a board); find it with ` + "`furrow config path`" + `.

[[board]]
# Path to the central .furrow (~, relative to this file, or absolute).
path = "/path/to/central/.furrow"
# Activate this board only when the working directory is under one of these dirs.
# Across all [[board]] entries the most specific (longest) match wins; list more
# than one to back several trees with the same board.
scopes = ["/path/to/the/tree/it/backs"]
# Repo the board scopes to: "auto" = derive owner/repo from the enclosing
# checkout (the git origin URL, worktree-aware; falls back to a ghq-style
# .../github.com/<owner>/<repo> path; otherwise new tasks are drafts), "" = no
# scope, or a literal "owner/repo". ` + "`furrow add`" + ` attaches it (` + "`--draft`" + ` opts out).
repo = "auto"
# Optional LITERAL label ` + "`furrow add`" + ` tags new tasks with — a pure tag, like a
# GitHub Issues label auto-applied per board. It never filters reads.
label = ""
# Auto-filter reads (ls/next/revisit) by the board repo; false shows the whole
# board while ` + "`add`" + ` still attaches the repo.
auto_filter = true
# Commit the board's .furrow/ after every mutating command (best-effort, no
# push) — the "touch furrow, always commit" backup guarantee turned into a tool
# behavior, for a standalone single-machine board. PER-MACHINE by design (that
# is why it lives in THIS file, not the board's shared config.toml): one
# operator's choice never propagates to other clones or CI. The board must be
# its own git repo (` + "`git init`" + ` in the board's directory). Default false.
autocommit = false

# Repeat [[board]] to declare more central boards; the innermost scope wins.
`

// RenderGlobalConfig returns the home config to write. With an empty boardPath
// (and/or no scopes) it returns GlobalTemplate unchanged — the placeholder form
// mirrored at repo-root config.global.toml. Given a derived board path and/or
// scopes it substitutes them into that template, leaving every comment intact so
// the written file stays self-documenting.
func RenderGlobalConfig(boardPath string, scopes []string) string {
	out := GlobalTemplate
	if boardPath != "" {
		out = strings.Replace(out, placeholderBoardPath, strconv.Quote(boardPath), 1)
	}
	if len(scopes) > 0 {
		out = strings.Replace(out, placeholderScopes, renderScopeArray(scopes), 1)
	}
	return out
}

// renderScopeArray formats scopes as a TOML inline array of basic strings, e.g.
// ["/a", "/b"]. strconv.Quote matches TOML basic-string escaping for the paths
// furrow handles (and stays safe for the pathological ones).
func renderScopeArray(scopes []string) string {
	quoted := make([]string, len(scopes))
	for i, s := range scopes {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
