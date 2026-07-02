package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGlobal(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadGlobalBoards_MissingFileIsNoOp(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if boards != nil {
		t.Errorf("boards = %+v, want nil for a missing file", boards)
	}
	if warn != nil {
		t.Errorf("warn = %v, want nil", warn)
	}
}

func TestLoadGlobalBoards_EmptyFileIsNoOp(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t, ""))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if boards != nil || warn != nil {
		t.Errorf("boards=%+v warn=%v, want both nil for a file with no [[board]]", boards, warn)
	}
}

func TestLoadGlobalBoards_SingleEntry(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/ws/org/projects/.furrow\"\nscopes = [\"/ws/org\"]\nrepo = \"auto\"\nlabel = \"tracked\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if len(boards) != 1 {
		t.Fatalf("boards = %+v, want exactly one", boards)
	}
	b := boards[0]
	if b.Path != "/ws/org/projects/.furrow" || b.Repo != "auto" || b.Label != "tracked" {
		t.Errorf("board = %+v, want path/repo/label set", b)
	}
	if len(b.Scopes) != 1 || b.Scopes[0] != "/ws/org" {
		t.Errorf("scopes = %v, want [/ws/org]", b.Scopes)
	}
}

// A literal repo passes through verbatim (validation/derivation is the app
// layer's job — config only carries the mode).
func TestLoadGlobalBoards_LiteralRepoCarried(t *testing.T) {
	boards, _, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nrepo = \"me/proj\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 || boards[0].Repo != "me/proj" {
		t.Errorf("boards = %+v, want one with repo me/proj", boards)
	}
}

// label="auto" is a reserved word now (the retired scope mode): the board is
// kept, the label is ignored — NOT treated as a literal tag — and a tombstone
// warning points at repo="auto". This closes the window between this release
// and the user's config switch without a compat shim.
func TestLoadGlobalBoards_LabelAutoTombstone(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("boards = %+v, want the board kept", boards)
	}
	if boards[0].Label != "" {
		t.Errorf("Label = %q, want empty (\"auto\" is reserved, never a literal)", boards[0].Label)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], `repo="auto"`) {
		t.Errorf("warn = %v, want one tombstone warning pointing at repo=\"auto\"", warn)
	}
}

func TestLoadGlobalBoards_MultipleEntriesPreserveOrder(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\n"+
			"[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\", \"/c\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if len(boards) != 2 {
		t.Fatalf("boards = %+v, want two in file order", boards)
	}
	if boards[0].Path != "/a/.furrow" || boards[1].Path != "/b/.furrow" {
		t.Errorf("order = [%q,%q], want [/a/.furrow,/b/.furrow]", boards[0].Path, boards[1].Path)
	}
	if len(boards[1].Scopes) != 2 || boards[1].Scopes[0] != "/b" || boards[1].Scopes[1] != "/c" {
		t.Errorf("board[1].Scopes = %v, want [/b /c]", boards[1].Scopes)
	}
}

func TestLoadGlobalBoards_EmptyPathDropsWithWarn(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\nscopes = [\"/a\"]\nlabel = \"auto\"\n"+ // no path -> dropped
			"[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 || boards[0].Path != "/b/.furrow" {
		t.Fatalf("boards = %+v, want only the valid /b board", boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the path-less board, got none")
	}
}

func TestLoadGlobalBoards_EmptyScopesDropsWithWarn(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\n")) // no scopes -> dropped
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if boards != nil {
		t.Errorf("boards = %+v, want nil (only entry has no scopes)", boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the scope-less board, got none")
	}
}

func TestLoadGlobalBoards_BlankScopeStringsRemoved(t *testing.T) {
	boards, _, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"\", \"/a\", \"\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("boards = %+v, want one (blank scopes pruned, /a kept)", boards)
	}
	if len(boards[0].Scopes) != 1 || boards[0].Scopes[0] != "/a" {
		t.Errorf("scopes = %v, want [/a] with blanks removed", boards[0].Scopes)
	}
}

func TestLoadGlobalBoards_AllDroppedReturnsNilWithWarn(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\n"+ // no scopes
			"[[board]]\nscopes = [\"/b\"]\n")) // no path
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if boards != nil {
		t.Errorf("boards = %+v, want nil when every entry is dropped", boards)
	}
	if len(warn) != 2 {
		t.Fatalf("warn = %v, want exactly two (one per dropped entry)", warn)
	}
	joined := strings.Join(warn, "\n")
	if !strings.Contains(joined, "/a/.furrow") || !strings.Contains(joined, "no scopes") {
		t.Errorf("warn = %v, want a no-scopes warning naming /a/.furrow", warn)
	}
	if !strings.Contains(joined, "#2") || !strings.Contains(joined, "no path") {
		t.Errorf("warn = %v, want a no-path warning for entry #2", warn)
	}
}

// An old single [board] table (pre-v2 config) decodes into a one-element slice
// with no scopes (the old `scope` key is silently dropped), so it clamps away to
// "no default board" with a warning rather than erroring. This is the accepted
// rollout-window behaviour: a v2 binary reading a v1 config simply runs without a
// global board until the config is migrated.
func TestLoadGlobalBoards_OldSingleBoardDegradesGracefully(t *testing.T) {
	boards, warn, err := LoadGlobalBoards(writeGlobal(t,
		"[board]\npath = \"/ws/org/projects/.furrow\"\nscope = \"/ws/org\"\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if boards != nil {
		t.Errorf("boards = %+v, want nil for a legacy single [board] config", boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the legacy single-board config, got none")
	}
}

func TestLoadGlobalBoards_MalformedErrors(t *testing.T) {
	if _, _, err := LoadGlobalBoards(writeGlobal(t, "[[board]]\npath = broken = toml\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

// auto_filter is an explicit, discoverable per-board switch for whether the read
// commands (ls/next/revisit) auto-filter by label. It defaults to true when the
// key is omitted, so an existing [[board]] keeps scoping reads as before.
func TestLoadGlobalBoards_AutoFilterDefaultsTrue(t *testing.T) {
	boards, _, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\n")) // no auto_filter key
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("boards = %+v, want one", boards)
	}
	if !boards[0].AutoFilter {
		t.Errorf("AutoFilter = false, want true when auto_filter is omitted")
	}
}

func TestLoadGlobalBoards_AutoFilterExplicitFalse(t *testing.T) {
	boards, _, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nauto_filter = false\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("boards = %+v, want one", boards)
	}
	if boards[0].AutoFilter {
		t.Errorf("AutoFilter = true, want false when auto_filter = false")
	}
}

func TestLoadGlobalBoards_AutoFilterExplicitTrue(t *testing.T) {
	boards, _, err := LoadGlobalBoards(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nauto_filter = true\n"))
	if err != nil {
		t.Fatalf("LoadGlobalBoards: %v", err)
	}
	if len(boards) != 1 || !boards[0].AutoFilter {
		t.Errorf("boards = %+v, want one with AutoFilter true", boards)
	}
}
