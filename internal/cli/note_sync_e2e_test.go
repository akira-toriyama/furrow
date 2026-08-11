package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// The whole-stack proof of the touched-bodies journal (t-dw9v): `furrow note`
// in one command process (autocommit OFF), a PLAIN `furrow sync` in another —
// the prose must reach the remote, because the CLI post-run journaled the id
// and sync consumed it. Before the journal, that body sat in pending_bodies
// until someone remembered `-b`, so the progress record agents are told to
// keep in the body never left the machine.
func TestNoteThenPlainSyncPublishesBody_EndToEndViaCLI(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv(app.EnvBoard, "")

	origin := t.TempDir()
	gitAt := func(dir string, args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
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

	id := addTask(t, "long-running task")
	if _, code := run(t, "sync"); code != 0 {
		t.Fatalf("first sync exit %d", code)
	}

	// Process boundary: note in its own command (runCLI builds a fresh tree and
	// runs the real PersistentPostRunE, which journals the body id).
	if _, code := run(t, "note", id, "progress: resume at the parser"); code != 0 {
		t.Fatalf("note exit %d", code)
	}
	// Plain sync — no -b, no --all-bodies.
	sout, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("plain sync exit %d\n%s", code, sout)
	}
	if strings.Contains(sout, "pending_bodies") {
		t.Errorf("furrow's own note must not pend: %s", sout)
	}

	// The remote carries the prose.
	check := filepath.Join(t.TempDir(), "check")
	gitAt(filepath.Dir(check), "clone", "-q", origin, check)
	body, err := os.ReadFile(filepath.Join(check, app.DirName, "bodies", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "resume at the parser") {
		t.Errorf("remote body must carry the note, got:\n%s", body)
	}
}
