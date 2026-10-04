package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardsListsConfiguredBoards(t *testing.T) {
	boardParent := t.TempDir()
	real := mustInitBoard(t, boardParent)
	missing := filepath.Join(t.TempDir(), "nowhere", ".furrow")
	writeGlobalConfig(t,
		boardEntry(real, "auto", boardParent)+
			"\n"+boardEntry(missing, "owner/repo", "/somewhere")+"label = \"tag\"\n"+
			"\n[[board]]\npath = \"/dropped/no/scopes\"\n")
	cfgPath, err := globalConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	list, warns, err := Boards()
	if err != nil {
		t.Fatal(err)
	}
	if list.Config != cfgPath {
		t.Errorf("config = %q, want %q", list.Config, cfgPath)
	}
	if len(list.Boards) != 2 {
		t.Fatalf("boards = %d entries, want 2 (the scope-less one clamps away): %+v", len(list.Boards), list.Boards)
	}

	live := list.Boards[0]
	if live.Store != real || !live.Exists {
		t.Errorf("live entry: store=%q exists=%t, want %q/true", live.Store, live.Exists, real)
	}
	if live.Repo != "auto" {
		t.Errorf("repo must stay DECLARED (%q), never resolved", live.Repo)
	}
	if len(live.Scopes) != 1 || live.Scopes[0] != boardParent {
		t.Errorf("scopes = %v, want [%s]", live.Scopes, boardParent)
	}
	if len(live.Lanes) == 0 || live.SchemaState != SchemaCurrent || !live.Writable {
		t.Errorf("live entry must carry the board's vocabulary and a current schema triple: %+v", live)
	}

	dead := list.Boards[1]
	if dead.Exists {
		t.Errorf("missing board must report exists=false")
	}
	if dead.Label != "tag" || dead.Repo != "owner/repo" {
		t.Errorf("declared repo/label must survive on a missing board: %+v", dead)
	}
	if dead.Lanes == nil || len(dead.Lanes) != 0 {
		t.Errorf("a board that was never opened reports an EMPTY (not nil, not default) vocabulary: %#v", dead.Lanes)
	}
	if dead.SchemaState != SchemaUnreadable || dead.Writable || dead.SchemaVersion != 0 {
		t.Errorf("missing board triple = %+v, want {0, unreadable, false}", dead.SchemaTriple)
	}

	found := false
	for _, w := range warns {
		if strings.Contains(w, "/dropped/no/scopes") {
			found = true
		}
	}
	if !found {
		t.Errorf("the clamped entry must surface as a warning, got %v", warns)
	}
}

func TestBoardsWithNoConfigIsEmptyNotError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file at all

	list, _, err := Boards()
	if err != nil {
		t.Fatalf("a machine with no config must list zero boards, not fail: %v", err)
	}
	if list.Boards == nil || len(list.Boards) != 0 {
		t.Errorf("boards = %#v, want empty non-nil slice", list.Boards)
	}
	if list.Config == "" {
		t.Errorf("config must name the file furrow LOOKED for even when absent")
	}
}

func TestBoardsIgnoresEnvBoardOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	board := mustInitBoard(t, t.TempDir())
	t.Setenv(EnvBoard, board)

	list, _, err := Boards()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Boards) != 0 {
		t.Errorf("FURROW_BOARD is a per-invocation redirect, not machine config — it must not be listed: %+v", list.Boards)
	}
}

// Unlisted is not unsaid: while FURROW_BOARD is set the configured entries are
// never read by discovery, so `boards` showed their scopes as if live, and the
// one scope that did decide was printed nowhere (t-xr78).
func TestBoardsNotesTheEnvBoardShadowingTheEntries(t *testing.T) {
	t.Setenv(EnvDir, "")
	writeGlobalConfig(t, boardEntry(mustInitBoard(t, t.TempDir()), "auto", t.TempDir()))
	board := mustInitBoard(t, filepath.Join(t.TempDir(), "org", "projects"))
	t.Setenv(EnvBoard, board)
	scope := `"` + filepath.Dir(filepath.Dir(board)) + `"`

	note := func() string {
		t.Helper()
		_, warns, err := Boards()
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range warns {
			if strings.Contains(w, "FURROW_BOARD=") {
				return w
			}
		}
		t.Fatalf("want a FURROW_BOARD note, got %q", warns)
		return ""
	}

	list, _, err := Boards()
	if err != nil || len(list.Boards) != 1 {
		t.Fatalf("the configured entry is still listed, got %+v / %v", list, err)
	}
	if w := note(); !strings.Contains(w, scope) || !strings.Contains(w, "not the configured entries") {
		t.Errorf("the note must name the override's scope %s over the entries: %s", scope, w)
	}

	// Each state says what discovery really does — never a scope that does not
	// decide, never "the configured entries" when there are none.
	t.Setenv(EnvDir, board)
	if w := note(); !strings.Contains(w, "FURROW_DIR outranks it") {
		t.Errorf("with FURROW_DIR set the note must say FURROW_BOARD is outranked: %s", w)
	}
	t.Setenv(EnvDir, "")
	gone := filepath.Join(t.TempDir(), "gone", DirName)
	t.Setenv(EnvBoard, gone)
	if w := note(); !strings.Contains(w, "not an existing directory") || !strings.Contains(w, "not the configured entries") {
		t.Errorf("a missing override store must be named as such: %s", w)
	}

	// With no entries configured, no arm may talk about them.
	writeGlobalConfig(t, "")
	for _, s := range []struct{ dir, board string }{{"", board}, {"", gone}, {board, board}} {
		t.Setenv(EnvDir, s.dir)
		t.Setenv(EnvBoard, s.board)
		if w := note(); strings.Contains(w, "entries") {
			t.Errorf("with no entries configured the note must not talk about them (FURROW_DIR=%q): %s", s.dir, w)
		}
	}
}
