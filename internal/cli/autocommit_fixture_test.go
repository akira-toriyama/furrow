package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/gittest"
	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

// autoCommitFixture is a user-config [[board]] with autocommit on: the board
// at scope/<name>/.furrow, FURROW_DIR and FURROW_BOARD cleared so discovery
// goes through the user config, a work dir under the scope, and — when the
// layout asked for it — the board's own git repo with one commit.
type autoCommitFixture struct {
	scope, boardRoot, board, work string
	gitAt                         func(args ...string) string
	commitCount                   func() int
}

func autoCommitLayout(t *testing.T, name string, gitInit bool) autoCommitFixture {
	t.Helper()
	git := gittest.GitOrSkip(t)
	t.Setenv(app.EnvDir, "")
	t.Setenv(app.EnvBoard, "")
	fx := autoCommitFixture{scope: t.TempDir()}
	fx.boardRoot = filepath.Join(fx.scope, name)
	if _, err := app.Init(fx.boardRoot); err != nil {
		t.Fatal(err)
	}
	fx.board = filepath.Join(fx.boardRoot, app.DirName)
	fx.gitAt = func(args ...string) string { return gittest.RunGit(t, git, fx.boardRoot, args...) }
	if gitInit {
		fx.gitAt("init", "-q", "-b", "main")
		fx.gitAt("add", "-A")
		fx.gitAt("commit", "-q", "-m", "board")
	}
	testutil.UserConfig(t, testutil.BoardEntry(fx.board, "", fx.scope)+"autocommit = true\n")
	fx.work = filepath.Join(fx.scope, "work")
	if err := os.MkdirAll(fx.work, 0o755); err != nil {
		t.Fatal(err)
	}
	fx.commitCount = func() int {
		n, err := strconv.Atoi(strings.TrimSpace(fx.gitAt("rev-list", "--count", "HEAD")))
		if err != nil {
			t.Fatalf("rev-list --count HEAD: %v", err)
		}
		return n
	}
	return fx
}

// autoCommitBoard is the layout the per-command proofs share: the board in
// its own git repo and cwd under the scope (never inside the board's tree),
// returning the board's git runner and a HEAD commit counter.
func autoCommitBoard(t *testing.T) (gitAt func(args ...string) string, commitCount func() int) {
	t.Helper()
	fx := autoCommitLayout(t, "central", true)
	t.Chdir(fx.work)
	return fx.gitAt, fx.commitCount
}
