package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// `furrow sync` outside a git repo: validation exit (2), and the progress
// object still lands on stdout — the "emitted on success AND failure" half of
// the contract that a plain error path would silently drop.
func TestSyncOutsideGitPrintsProgressAndExits2(t *testing.T) {
	initStore(t) // t.TempDir board — not a git repo

	out, code := run(t, "--json", "sync")
	if code != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", code, core.CodeValidation)
	}
	for _, key := range []string{`"committed": false`, `"pulled": false`, `"pushed": false`, `"conflict": false`} {
		if !strings.Contains(out, key) {
			t.Errorf("progress object missing %s on failure:\n%s", key, out)
		}
	}

	// Human mode prints the terse one-liner instead.
	hout, hcode := run(t, "sync")
	if hcode != int(core.CodeValidation) {
		t.Errorf("human exit = %d, want %d", hcode, core.CodeValidation)
	}
	if !strings.Contains(hout, "sync: committed=false") {
		t.Errorf("human summary missing:\n%s", hout)
	}
}

func TestRevisitLineRendering(t *testing.T) {
	// Non-empty: counts, scope tag, and the call-to-action.
	got := revisitLine(app.RevisitSummary{DepDone: []string{"t-1", "t-2"}, Stale: []string{"t-3"}}, "furrow")
	want := "revisit: 2 dep_done, 1 stale (furrow) — furrow revisit"
	if got != want {
		t.Errorf("revisitLine = %q, want %q", got, want)
	}
	// Empty summary renders nothing (clean board stays quiet).
	if got := revisitLine(app.RevisitSummary{}, "furrow"); got != "" {
		t.Errorf("empty revisitLine = %q, want \"\"", got)
	}
	// Board-wide fallback tag.
	if got := revisitLine(app.RevisitSummary{Stale: []string{"t-3"}}, "board"); !strings.Contains(got, "(board)") {
		t.Errorf("board tag missing: %q", got)
	}
}

func TestSyncOutputJSONShape(t *testing.T) {
	prog := &app.SyncProgress{Pulled: true, Pushed: true}
	// With a summary: revisit object carries the ids.
	withSum := mustJSON(syncOutput{prog, &app.RevisitSummary{DepDone: []string{"t-0046"}}})
	for _, want := range []string{`"pulled": true`, `"revisit"`, `"dep_done"`, `"t-0046"`} {
		if !strings.Contains(string(withSum), want) {
			t.Errorf("json missing %s:\n%s", want, withSum)
		}
	}
	// Without a summary: no revisit key at all (omitempty via nil pointer).
	noSum := mustJSON(syncOutput{prog, nil})
	if strings.Contains(string(noSum), "revisit") {
		t.Errorf("empty summary must omit revisit key:\n%s", noSum)
	}
}

// initGitBoard sets up a bare origin + one clone with an initialized board,
// points FURROW_DIR at the clone's .furrow, and returns the clone path. Skips
// when git is unavailable. sync can push/pull against origin for real.
func initGitBoard(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	gitT := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command(git, args...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	origin := t.TempDir()
	gitT(origin, "init", "-q", "--bare", "-b", "main")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(filepath.Dir(clone), "clone", "-q", origin, clone)
	gitT(clone, "config", "user.name", "t")
	gitT(clone, "config", "user.email", "t@e")
	if _, err := app.Init(clone); err != nil {
		t.Fatal(err)
	}
	gitT(clone, "add", "-A")
	gitT(clone, "commit", "-q", "-m", "board")
	gitT(clone, "push", "-q", "-u", "origin", "main")
	t.Setenv(app.EnvDir, filepath.Join(clone, app.DirName))
	return clone
}

func TestSyncSurfacesRevisitLine(t *testing.T) {
	initGitBoard(t)

	// A done dependency and a ready task depending on it -> one dep_done.
	dep := addTask(t, "dep")
	if _, code := run(t, "done", dep); code != 0 {
		t.Fatalf("done exit %d", code)
	}
	user := addTask(t, "needs dep", "--dep", dep)

	// Human sync prints the revisit line (board-wide: no auto repo in a test board).
	hout, hcode := run(t, "sync")
	if hcode != 0 {
		t.Fatalf("sync exit %d:\n%s", hcode, hout)
	}
	if !strings.Contains(hout, "revisit: 1 dep_done") {
		t.Errorf("human sync missing revisit line:\n%s", hout)
	}

	// JSON sync carries the dependent id under revisit.dep_done.
	jout, jcode := run(t, "--json", "sync")
	if jcode != 0 {
		t.Fatalf("json sync exit %d:\n%s", jcode, jout)
	}
	var got syncOutput
	if err := json.Unmarshal([]byte(jout), &got); err != nil {
		t.Fatalf("parse sync --json: %v\n%s", err, jout)
	}
	if got.Revisit == nil || len(got.Revisit.DepDone) != 1 || got.Revisit.DepDone[0] != user {
		t.Errorf("revisit.dep_done = %+v, want [%s]", got.Revisit, user)
	}
}
