package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

// TestRevisitScopeLabelAndSyncScope is a direct unit test for the two scope
// helpers (revisitScopeLabel, syncScope) sharing boardScopeRepo — a bare
// *app.App reading only DefaultRepo/AutoFilter is enough, no store needed.
func TestRevisitScopeLabelAndSyncScope(t *testing.T) {
	a := &app.App{DefaultRepo: "akira-toriyama/furrow", AutoFilter: true}
	if got := revisitScopeLabel(a); got != "furrow" {
		t.Errorf("revisitScopeLabel = %q, want %q", got, "furrow")
	}
	if got := syncScope(a).ScopeRepo; got != "akira-toriyama/furrow" {
		t.Errorf("syncScope(a).ScopeRepo = %q, want %q", got, "akira-toriyama/furrow")
	}

	off := &app.App{DefaultRepo: "akira-toriyama/furrow", AutoFilter: false}
	if got := revisitScopeLabel(off); got != "board" {
		t.Errorf("revisitScopeLabel (auto_filter=false) = %q, want %q", got, "board")
	}
	if got := syncScope(off).ScopeRepo; got != "" {
		t.Errorf("syncScope(off).ScopeRepo = %q, want empty", got)
	}

	noRepo := &app.App{DefaultRepo: "", AutoFilter: true}
	if got := revisitScopeLabel(noRepo); got != "board" {
		t.Errorf("revisitScopeLabel (no default repo) = %q, want %q", got, "board")
	}
	if got := syncScope(noRepo).ScopeRepo; got != "" {
		t.Errorf("syncScope(noRepo).ScopeRepo = %q, want empty", got)
	}
}

func TestSyncOutputJSONShape(t *testing.T) {
	prog := &app.SyncProgress{Pulled: true, Pushed: true}
	// With a summary: revisit object carries the ids.
	withSum := mustJSON(syncOutput{prog, &app.RevisitSummary{DepDone: []string{"t-0046"}}, nil})
	for _, want := range []string{`"pulled": true`, `"revisit"`, `"dep_done"`, `"t-0046"`} {
		if !strings.Contains(string(withSum), want) {
			t.Errorf("json missing %s:\n%s", want, withSum)
		}
	}
	// Without a summary: no revisit key at all (omitempty via nil pointer).
	noSum := mustJSON(syncOutput{prog, nil, nil})
	if strings.Contains(string(noSum), "revisit") {
		t.Errorf("empty summary must omit revisit key:\n%s", noSum)
	}
	// A clean board omits the lint key the same way; errors surface it.
	if strings.Contains(string(noSum), "lint") {
		t.Errorf("clean board must omit lint key:\n%s", noSum)
	}
	withLint := mustJSON(syncOutput{prog, nil, &app.LintErrorSummary{Errors: 2, Codes: map[string]int{"epic-required": 2}}})
	for _, want := range []string{`"lint"`, `"errors": 2`, `"epic-required": 2`} {
		if !strings.Contains(string(withLint), want) {
			t.Errorf("json missing %s:\n%s", want, withLint)
		}
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

// t-7kvj end to end: a board whose only gate is a client-side pre-push hook.
// When the hook blocks, sync used to say pushed=false and the envelope said
// "failed to push some refs" — the hook's own lines (which lint code fired,
// the --no-verify escape) died inside the git wrapper's buffer. They must now
// reach the operator verbatim on stderr, and machine callers via the
// envelope's details.stderr.
func TestSyncRelaysPushHookStderr(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv(app.EnvBoard, "")

	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })

	origin := t.TempDir()
	gitAt := func(dir string, args ...string) {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
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

	hook := filepath.Join(boardRoot, ".git", "hooks", "pre-push")
	script := "#!/bin/sh\n" +
		"echo 'error  epic-required: the new task is filed under no box' >&2\n" +
		"echo 'escape hatch: git push --no-verify' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	addTask(t, "blocked by the gate") // something to commit and push
	so.Reset()
	se.Reset()

	fe, _ := runErr(t, "sync")
	if fe == nil || fe.Kind != core.KindGitFailed {
		t.Fatalf("sync = %+v, want kind git-failed", fe)
	}
	m, ok := fe.Details.(map[string]any)
	if !ok {
		t.Fatalf("details = %#v, want a map carrying stderr", fe.Details)
	}
	if s, _ := m["stderr"].(string); !strings.Contains(s, "epic-required") {
		t.Errorf("details.stderr %q should carry the hook's block reason", s)
	}

	relay := se.String()
	if !strings.Contains(relay, "git push stderr:") {
		t.Errorf("stderr should introduce the relay, got:\n%s", relay)
	}
	for _, want := range []string{"epic-required", "--no-verify"} {
		if !strings.Contains(relay, want) {
			t.Errorf("stderr relay should carry %q, got:\n%s", want, relay)
		}
	}
}
