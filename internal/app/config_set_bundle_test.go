package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

func userConfigPath(t *testing.T) string {
	t.Helper()
	p, err := GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A value the reader clamps is refused even when the file ALREADY holds it:
// the guard used to compare against the document as it was, so a clamped
// current value carried its warning before the edit too and "bogus" over
// "bogus" was exit 0, "unchanged".
func TestConfigSetUserRefusesAValueTheFileAlreadyHoldsClamped(t *testing.T) {
	testutil.UserConfig(t, "[session]\nguard = \"bogus\"\n")
	before, _ := os.ReadFile(userConfigPath(t))
	_, err := ConfigSetUser("", "session.guard", "bogus")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "session.guard") {
		t.Fatalf("set of an already-clamped value = %v; want exit 2 naming the clamp", err)
	}
	after, _ := os.ReadFile(userConfigPath(t))
	if string(after) != string(before) {
		t.Error("a refused set wrote the file")
	}
	ed, err := ConfigSetUser("", "session.guard", "warn")
	if err != nil || len(ed.Changed) != 1 {
		t.Fatalf("a valid value over a clamped one must be written: %v %+v", err, ed)
	}
}

// A [session] key set on the BOARD target names where the key lives now,
// with the user keys as candidates — not a bare "unknown key".
func TestConfigSetBoardPointsASessionKeyAtTheUserConfig(t *testing.T) {
	a := newFSApp(t)
	_, err := a.ConfigSetBoard("session.guard", "warn")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--user session.guard") {
		t.Fatalf("board set of session.guard = %v; want exit 2 pointing at --user", err)
	}
	if joined := strings.Join(fe.Candidates, ","); strings.Contains(joined, "session.") || !strings.Contains(joined, "lanes.default") {
		t.Errorf("candidates = %v; want the board's own vocabulary, which has no session keys", fe.Candidates)
	}
}

// With --board the candidates for an unknown key are the ENTRY keys only:
// the [session] keys never combine with --board, so offering them misleads.
func TestConfigSetUserBoardCandidatesExcludeSessionKeys(t *testing.T) {
	testutil.UserConfig(t, testutil.BoardEntry("/b/.furrow", "auto", "/b"))
	_, err := ConfigSetUser("/b/.furrow", "nosuch", "1")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("want exit 2, got %v", err)
	}
	for _, c := range fe.Candidates {
		if strings.HasPrefix(c, "session.") {
			t.Errorf("candidates under --board must not offer %s: %v", c, fe.Candidates)
		}
	}
	_, err = ConfigSetUser("", "nosuch", "1")
	if fe := core.AsError(err); fe == nil || !strings.Contains(strings.Join(fe.Candidates, ","), "session.guard") {
		t.Errorf("without --board the session keys are candidates: %+v", fe)
	}
}

// Two [[board]] entries declaring one path are both exact for --board <path>;
// the first used to be edited silently. It is ambiguous, naming both.
func TestConfigSetUserRefusesAPathTwoEntriesDeclare(t *testing.T) {
	testutil.UserConfig(t, testutil.BoardEntry("/b/.furrow", "auto", "/one")+testutil.BoardEntry("/b/.furrow", "auto", "/two"))
	before, _ := os.ReadFile(userConfigPath(t))
	_, err := ConfigSetUser("/b/.furrow", "autocommit", "true")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) != 2 || !strings.Contains(fe.Msg, "merge") {
		t.Fatalf("set with a duplicated path = %+v; want an ambiguity naming both entries", err)
	}
	after, _ := os.ReadFile(userConfigPath(t))
	if string(after) != string(before) {
		t.Error("a refused set wrote the file")
	}
	// A scope unique to one of them still picks it.
	if ed, err := ConfigSetUser("/two", "autocommit", "true"); err != nil || len(ed.Changed) != 1 {
		t.Errorf("a unique scope must still resolve: %v %+v", err, ed)
	}
}

// scopes = ["/"] means every directory, not "/" alone: the boundary check
// appended a separator to a scope that already ends in one.
func TestRootScopeActivatesEverywhere(t *testing.T) {
	store := mustInitBoard(t, t.TempDir())
	testutil.UserConfig(t, testutil.BoardEntry(store, "", "/"))
	cwd := t.TempDir()
	a, err := Open(cwd)
	if err != nil {
		t.Fatalf("Open under a root scope: %v", err)
	}
	if canonicalPath(a.Dir) != canonicalPath(store) {
		t.Errorf("resolved %s, want the root-scoped board %s", a.Dir, store)
	}
}

// doctor lists one board once, however its path is spelled: a FURROW_DIR
// through a symlink and the [[board]] entry naming the real store compared
// as strings and came out as two rows.
func TestDoctorListsASymlinkedStoreOnce(t *testing.T) {
	parent := t.TempDir()
	store := mustInitBoard(t, parent)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(parent, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	testutil.UserConfig(t, testutil.BoardEntry(store, "", parent))
	t.Setenv(EnvDir, filepath.Join(link, DirName))
	r := mustDoctor(t, parent)
	n := 0
	for _, b := range r.Boards {
		if canonicalPath(b.Store) == canonicalPath(store) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the store is listed %d times: %+v", n, r.Boards)
	}
}

// Two entries declaring one store are reported once by doctor, naming both.
func TestDoctorWarnsAboutEntriesDeclaringOneStore(t *testing.T) {
	store := mustInitBoard(t, t.TempDir())
	testutil.UserConfig(t, testutil.BoardEntry(store, "", "/one")+testutil.BoardEntry(store, "", "/two"))
	r := mustDoctor(t, "")
	n := findProblems(r, "board-duplicate-path")
	if len(n) != 1 || !strings.Contains(r.Problems[n[0]].Msg, "#1, #2") {
		t.Errorf("want one board-duplicate-path naming entries #1 and #2, got %+v", r.Problems)
	}
}
