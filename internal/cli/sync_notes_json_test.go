package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/gittest"
)

// The pending-bodies remedy prints under --json too (t-mygx): the machine
// facts (pending_bodies, complete:false) are in the JSON, but the stderr line
// naming the fix (`furrow sync -b <id>`) used to live only in the human
// branch — the one sync note pair that broke the "--json stdout + stderr
// notes" pairing every other command keeps. stdout must stay pure JSON.
func TestSyncJSONStillPrintsPendingBodiesRemedy(t *testing.T) {
	git := gittest.GitOrSkip(t)
	t.Setenv(app.EnvBoard, "")

	origin := t.TempDir()
	gitAt := func(dir string, args ...string) string { return gittest.RunGit(t, git, dir, args...) }
	gitAt(origin, "init", "-q", "--bare", "-b", "main")
	boardRoot := filepath.Join(t.TempDir(), "central")
	gitAt(filepath.Dir(boardRoot), "clone", "-q", origin, boardRoot)
	for _, kv := range [][2]string{{"user.name", "t"}, {"user.email", "t@e"}} {
		gitAt(boardRoot, "config", kv[0], kv[1])
	}
	if _, err := app.Init(boardRoot); err != nil {
		t.Fatal(err)
	}
	gitAt(boardRoot, "add", "-A")
	gitAt(boardRoot, "commit", "-q", "-m", "board")
	gitAt(boardRoot, "push", "-q", "-u", "origin", "main")
	t.Setenv(app.EnvDir, filepath.Join(boardRoot, app.DirName))
	t.Chdir(boardRoot)

	id := addTask(t, "hand-edited later")
	if _, code := run(t, "sync"); code != 0 {
		t.Fatal("first sync failed")
	}

	// A HAND edit (no furrow write, no journal): the class a plain sync must
	// leave pending — and must SAY so on stderr, JSON mode or not.
	body := filepath.Join(boardRoot, app.DirName, "bodies", id+".md")
	if err := os.WriteFile(body, []byte("# hand-edited later\n\nlocal WIP prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sout, se, code := runSplit(t, "sync", "--json")
	if code != 0 {
		t.Fatalf("sync --json exit %d\n%s", code, sout)
	}
	var prog struct {
		PendingBodies []string `json:"pending_bodies"`
		Complete      bool     `json:"complete"`
	}
	if err := json.Unmarshal([]byte(sout), &prog); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, sout)
	}
	if len(prog.PendingBodies) != 1 || prog.PendingBodies[0] != id || prog.Complete {
		t.Fatalf("progress = %+v, want the hand edit pending and complete:false", prog)
	}
	if !strings.Contains(se, "left uncommitted") || !strings.Contains(se, id) {
		t.Errorf("stderr %q must carry the pending-bodies remedy naming %s", se, id)
	}
	if strings.Contains(sout, "left uncommitted") {
		t.Errorf("the remedy leaked into stdout: %s", sout)
	}
}
