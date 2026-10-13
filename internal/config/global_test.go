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

func TestLoadGlobal_MissingFileIsNoOp(t *testing.T) {
	global, warn, err := LoadGlobal(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if global.Boards != nil {
		t.Errorf("global.Boards = %+v, want nil for a missing file", global.Boards)
	}
	if warn != nil {
		t.Errorf("warn = %v, want nil", warn)
	}
}

func TestLoadGlobal_EmptyFileIsNoOp(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t, ""))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if global.Boards != nil || warn != nil {
		t.Errorf("global.Boards=%+v warn=%v, want both nil for a file with no [[board]]", global.Boards, warn)
	}
}

func TestLoadGlobal_SingleEntry(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/ws/org/projects/.furrow\"\nscopes = [\"/ws/org\"]\nrepo = \"auto\"\nlabel = \"tracked\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want exactly one", global.Boards)
	}
	b := global.Boards[0]
	if b.Path != "/ws/org/projects/.furrow" || b.Repo != "auto" || b.Label != "tracked" {
		t.Errorf("board = %+v, want path/repo/label set", b)
	}
	if len(b.Scopes) != 1 || b.Scopes[0] != "/ws/org" {
		t.Errorf("scopes = %v, want [/ws/org]", b.Scopes)
	}
}

// A literal repo passes through verbatim (validation/derivation is the app
// layer's job — config only carries the mode).
func TestLoadGlobal_LiteralRepoCarried(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nrepo = \"me/proj\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 || global.Boards[0].Repo != "me/proj" {
		t.Errorf("global.Boards = %+v, want one with repo me/proj", global.Boards)
	}
}

// label="auto" is a reserved word now (the retired scope mode): the board is
// kept, the label is ignored — NOT treated as a literal tag — and a tombstone
// warning points at repo="auto". This closes the window between this release
// and the user's config switch without a compat shim.
func TestLoadGlobal_LabelAutoTombstone(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want the board kept", global.Boards)
	}
	if global.Boards[0].Label != "" {
		t.Errorf("Label = %q, want empty (\"auto\" is reserved, never a literal)", global.Boards[0].Label)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], `repo="auto"`) {
		t.Errorf("warn = %v, want one tombstone warning pointing at repo=\"auto\"", warn)
	}
}

func TestLoadGlobal_MultipleEntriesPreserveOrder(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\n"+
			"[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\", \"/c\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
	if len(global.Boards) != 2 {
		t.Fatalf("global.Boards = %+v, want two in file order", global.Boards)
	}
	if global.Boards[0].Path != "/a/.furrow" || global.Boards[1].Path != "/b/.furrow" {
		t.Errorf("order = [%q,%q], want [/a/.furrow,/b/.furrow]", global.Boards[0].Path, global.Boards[1].Path)
	}
	if len(global.Boards[1].Scopes) != 2 || global.Boards[1].Scopes[0] != "/b" || global.Boards[1].Scopes[1] != "/c" {
		t.Errorf("board[1].Scopes = %v, want [/b /c]", global.Boards[1].Scopes)
	}
}

func TestLoadGlobal_EmptyPathDropsWithWarn(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\nscopes = [\"/a\"]\nlabel = \"auto\"\n"+ // no path -> dropped
			"[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 || global.Boards[0].Path != "/b/.furrow" {
		t.Fatalf("global.Boards = %+v, want only the valid /b board", global.Boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the path-less board, got none")
	}
}

func TestLoadGlobal_EmptyScopesDropsWithWarn(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\n")) // no scopes -> dropped
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if global.Boards != nil {
		t.Errorf("global.Boards = %+v, want nil (only entry has no scopes)", global.Boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the scope-less board, got none")
	}
}

func TestLoadGlobal_BlankScopeStringsRemoved(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"\", \"/a\", \"\"]\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want one (blank scopes pruned, /a kept)", global.Boards)
	}
	if len(global.Boards[0].Scopes) != 1 || global.Boards[0].Scopes[0] != "/a" {
		t.Errorf("scopes = %v, want [/a] with blanks removed", global.Boards[0].Scopes)
	}
}

func TestLoadGlobal_AllDroppedReturnsNilWithWarn(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\n"+ // no scopes
			"[[board]]\nscopes = [\"/b\"]\n")) // no path
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if global.Boards != nil {
		t.Errorf("global.Boards = %+v, want nil when every entry is dropped", global.Boards)
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
func TestLoadGlobal_OldSingleBoardDegradesGracefully(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t,
		"[board]\npath = \"/ws/org/projects/.furrow\"\nscope = \"/ws/org\"\nlabel = \"auto\"\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if global.Boards != nil {
		t.Errorf("global.Boards = %+v, want nil for a legacy single [board] config", global.Boards)
	}
	if len(warn) == 0 {
		t.Error("want a clamp warning for the legacy single-board config, got none")
	}
}

func TestLoadGlobal_MalformedErrors(t *testing.T) {
	if _, _, err := LoadGlobal(writeGlobal(t, "[[board]]\npath = broken = toml\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

// auto_filter is a per-board switch for whether reads are scoped by the board's
// REPO — never by its label, which is only a literal add-time tag. It defaults
// to true when the key is omitted, so an existing [[board]] keeps scoping reads
// as before.
func TestLoadGlobal_AutoFilterDefaultsTrue(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\n")) // no auto_filter key
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want one", global.Boards)
	}
	if !global.Boards[0].AutoFilter {
		t.Errorf("AutoFilter = false, want true when auto_filter is omitted")
	}
}

func TestLoadGlobal_AutoFilterExplicitFalse(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nauto_filter = false\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want one", global.Boards)
	}
	if global.Boards[0].AutoFilter {
		t.Errorf("AutoFilter = true, want false when auto_filter = false")
	}
}

func TestLoadGlobal_AutoFilterExplicitTrue(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nauto_filter = true\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 || !global.Boards[0].AutoFilter {
		t.Errorf("global.Boards = %+v, want one with AutoFilter true", global.Boards)
	}
}

// autocommit is a per-machine opt-in (default false). Unlike auto_filter it is a
// plain bool: an omitted key is simply false, no *bool omitted-vs-explicit dance.
func TestLoadGlobal_AutoCommitDefaultsFalse(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\n")) // no autocommit key
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 || global.Boards[0].AutoCommit {
		t.Errorf("global.Boards = %+v, want one with AutoCommit false when the key is omitted", global.Boards)
	}
}

func TestLoadGlobal_AutoCommitExplicitTrue(t *testing.T) {
	global, _, err := LoadGlobal(writeGlobal(t,
		"[[board]]\npath = \"/a/.furrow\"\nscopes = [\"/a\"]\nautocommit = true\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 || !global.Boards[0].AutoCommit {
		t.Errorf("global.Boards = %+v, want one with AutoCommit true", global.Boards)
	}
}

// A wrong-typed value in ONE [[board]] must not take furrow down for every
// repo on the machine: the file is ambient. The broken entry is dropped with a
// warning naming the file, the entry, and the key; the healthy entries load.
func TestLoadGlobal_TypeErrorDropsOnlyThatEntry(t *testing.T) {
	path := writeGlobal(t, `[[board]]
path = "/a/.furrow"
scopes = ["/a"]

[[board]]
path = "/b/.furrow"
scopes = "not-an-array"
`)
	global, warn, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("a type error in one entry must not error the file: %v", err)
	}
	if len(global.Boards) != 1 || global.Boards[0].Path != "/a/.furrow" {
		t.Fatalf("global.Boards = %+v, want only the healthy /a entry", global.Boards)
	}
	joined := strings.Join(warn, "\n")
	if !strings.Contains(joined, "#2") || !strings.Contains(joined, `"scopes"`) {
		t.Errorf("warning should name the entry and the key, got %v", warn)
	}
	if !strings.Contains(joined, path) {
		t.Errorf("warning should name the file, got %v", warn)
	}
}

// The same survival when the broken entry comes FIRST — order must not decide
// which boards live.
func TestLoadGlobal_TypeErrorInFirstEntry(t *testing.T) {
	global, warn, err := LoadGlobal(writeGlobal(t, `[[board]]
path = 42
scopes = ["/a"]

[[board]]
path = "/b/.furrow"
scopes = ["/b"]
`))
	if err != nil {
		t.Fatalf("a type error in one entry must not error the file: %v", err)
	}
	if len(global.Boards) != 1 || global.Boards[0].Path != "/b/.furrow" {
		t.Fatalf("global.Boards = %+v, want only the healthy /b entry", global.Boards)
	}
	if len(warn) == 0 {
		t.Error("want a warning for the dropped entry")
	}
}

// An unknown key in the user config warns with its line (strict decode, same
// contract as the board config) and never drops the entry.
func TestLoadGlobal_UnknownKeyWarnsAndEntrySurvives(t *testing.T) {
	path := writeGlobal(t, `[[board]]
path = "/a/.furrow"
scopes = ["/a"]
scope = "/a"
`)
	global, warn, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(global.Boards) != 1 {
		t.Fatalf("global.Boards = %+v, want the entry to survive its unknown key", global.Boards)
	}
	joined := strings.Join(warn, "\n")
	if !strings.Contains(joined, `"board.scope"`) || !strings.Contains(joined, path+":4:") {
		t.Errorf("want an unknown-key warning naming board.scope with file:line, got %v", warn)
	}
}

// A pure syntax error still fails: nothing can be salvaged from a file with no
// readable structure (and pretending otherwise would hide real breakage).
func TestLoadGlobal_SyntaxErrorStillErrors(t *testing.T) {
	if _, _, err := LoadGlobal(writeGlobal(t, "[[board]\npath=\n")); err == nil {
		t.Fatal("expected error for a syntax error, got nil")
	}
}
