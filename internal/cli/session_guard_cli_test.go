package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/claudecode"
	"github.com/akira-toriyama/furrow/internal/core"
)

// plantSession writes a registry entry (and, when active is non-zero, a
// transcript whose mtime is active) under the Claude Code config dir cc.
func plantSession(t *testing.T, cc string, pid int, id, cwd string, started, active time.Time) {
	t.Helper()
	sdir := filepath.Join(cc, "sessions")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":` + strconv.Itoa(pid) + `,"sessionId":"` + id + `","cwd":"` + cwd + `","startedAt":` + strconv.FormatInt(started.UnixMilli(), 10) + `,"kind":"interactive","name":"n-` + id + `"}`
	if err := os.WriteFile(filepath.Join(sdir, strconv.Itoa(pid)+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if active.IsZero() {
		return
	}
	mangled := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, cwd)
	tdir := filepath.Join(cc, "projects", mangled)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(tdir, id+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, active, active); err != nil {
		t.Fatal(err)
	}
}

func plantCheckout(t *testing.T, repo string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "co")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/" + repo + ".git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// armGuard puts this test process under a fake Claude Code session (self =
// our own pid, started 10 minutes ago) with one earlier occupant in a o/glyph
// checkout — the parent process, so its pid is alive — last active `idle` ago.
func armGuard(t *testing.T, idle time.Duration) (cc string, occupantTranscript string) {
	t.Helper()
	cc = t.TempDir()
	t.Setenv(claudecode.EnvConfigDir, cc)
	t.Setenv(claudecode.EnvActive, "1")
	t.Setenv(claudecode.EnvPID, strconv.Itoa(os.Getpid()))
	t.Setenv(claudecode.EnvSessionID, "self-sid")
	now := time.Now()
	plantSession(t, cc, os.Getpid(), "self-sid", t.TempDir(), now.Add(-10*time.Minute), now)
	glyph := plantCheckout(t, "o/glyph")
	plantSession(t, cc, os.Getppid(), "occ-sid", glyph, now.Add(-time.Hour), now.Add(-idle))
	return cc, filepath.Join(cc, "projects", strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, glyph), "occ-sid.jsonl")
}

func TestCLISessionGuardRefusesThenEscapesWithDraft(t *testing.T) {
	initStore(t)
	armGuard(t, 10*time.Second)
	fe, _ := runErr(t, "add", "into glyph", "-r", "o/glyph")
	if fe == nil || fe.Kind != core.KindSessionBusy || fe.Code != core.CodeValidation {
		t.Fatalf("want session-busy exit 2, got %+v", fe)
	}
	d, _ := fe.Details.(map[string]any)
	if d["hint"] != "--draft" {
		t.Errorf("details.hint = %v", d["hint"])
	}
	if _, code := run(t, "add", "as draft", "--draft"); code != 0 {
		t.Fatalf("--draft must pass, exit %d", code)
	}
	if out, _ := run(t, "ls", "--json", "-r", ""); strings.Contains(out, "into glyph") || !strings.Contains(out, "as draft") {
		t.Fatalf("only the draft exists:\n%s", out)
	}
	// Every mutator on a task in the occupied repo is refused too.
	var id string
	t.Setenv(claudecode.EnvActive, "") // a human shell creates the fixture
	id = addTask(t, "in glyph", "-r", "o/glyph", "-s", "ready")
	t.Setenv(claudecode.EnvActive, "1")
	for _, args := range [][]string{{"done", id}, {"note", id, "hi"}, {"set", id, "-s", "backlog"}, {"label", id, "--add", "x"}} {
		if fe, _ := runErr(t, args...); fe == nil || fe.Kind != core.KindSessionBusy {
			t.Errorf("%v: want session-busy, got %+v", args, fe)
		}
	}
}

func TestCLISessionGuardIdleOccupantWarnsAndAnnotates(t *testing.T) {
	initStore(t)
	armGuard(t, 20*time.Minute)
	so, se, code := runSplit(t, "--json", "add", "into glyph", "-r", "o/glyph")
	if code != 0 {
		t.Fatalf("idle occupant must not refuse: exit %d\n%s%s", code, so, se)
	}
	if !strings.Contains(se, "warning:") || !strings.Contains(se, "o/glyph") || !strings.Contains(se, "n-occ-sid") {
		t.Fatalf("stderr should carry the warning line: %q", se)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(so), &created); err != nil {
		t.Fatalf("add --json stays the created task: %v\n%s", err, so)
	}
	so, se, code = runSplit(t, "--json", "done", created.ID)
	if code != 0 {
		t.Fatalf("done: exit %d\n%s%s", code, so, se)
	}
	var envs []map[string]any
	if err := json.Unmarshal([]byte(so), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("done --json envelope array: %v\n%s", err, so)
	}
	warn, _ := envs[0]["session_warn"].(map[string]any)
	clashes, _ := warn["clashes"].([]any)
	if len(clashes) != 1 {
		t.Fatalf("session_warn.clashes missing: %v", envs[0])
	}
	c := clashes[0].(map[string]any)
	if c["repo"] != "o/glyph" || c["busy"] != false || c["idle_seconds"] == nil || c["session_id"] != "occ-sid" {
		t.Errorf("clash = %v", c)
	}
	if strings.Count(se, "warning:") != 1 {
		t.Errorf("the warning prints once: %q", se)
	}
	// A clean write carries no key.
	t.Setenv(claudecode.EnvActive, "")
	so, _, _ = runSplit(t, "--json", "note", created.ID, "human")
	if strings.Contains(so, "session_warn") {
		t.Errorf("no session_warn outside Claude Code:\n%s", so)
	}
}

func TestCLISessionGuardHumanAndStandDown(t *testing.T) {
	initStore(t)
	cc, _ := armGuard(t, time.Second)
	t.Setenv(claudecode.EnvActive, "")
	if _, code := run(t, "add", "human", "-r", "o/glyph"); code != 0 {
		t.Fatalf("a shell outside Claude Code passes: exit %d", code)
	}
	t.Setenv(claudecode.EnvActive, "1")
	// Break every registry entry: the guard stands down and says so.
	entries, _ := filepath.Glob(filepath.Join(cc, "sessions", "*.json"))
	for _, e := range entries {
		if err := os.WriteFile(e, []byte("{nope"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	so, se, code := runSplit(t, "add", "unordered", "-r", "o/glyph")
	if code != 0 {
		t.Fatalf("stand down, never refuse on a guess: exit %d\n%s%s", code, so, se)
	}
	if !strings.Contains(se, "note: session guard:") || !strings.Contains(se, "standing down") {
		t.Fatalf("stderr should carry the stand-down note: %q", se)
	}
	out, _ := run(t, "doctor", "--json")
	if !strings.Contains(out, `"session-registry-unreadable"`) {
		t.Fatalf("doctor should name the unreadable registry:\n%s", out)
	}
	// The note survives a run that FAILS after the guard ran (cobra skips the
	// post-run hook there): `dep` guards, then rejects the unknown dep id.
	id := addTask(t, "y", "-r", "o/glyph")
	so, se, code = runSplit(t, "dep", id, "t-nope")
	if code != 2 || !strings.Contains(se, "note: session guard:") {
		t.Fatalf("stand-down note lost on the error path: exit %d\nSTDOUT=%q\nSTDERR=%q", code, so, se)
	}
}

func TestCLISessionGuardRefusalNeverClaimsTheWriteWentThrough(t *testing.T) {
	initStore(t)
	cc, _ := armGuard(t, 20*time.Minute) // o/glyph occupant idle
	// A second, BUSY occupant in o/other (pid 1 — init/launchd, alive on every
	// unix host, and a foreign uid reads alive too): the batch clashes idle on
	// the first id and busy on the second, so the run fails after an idle
	// clash was queued.
	other := plantCheckout(t, "o/other")
	plantSession(t, cc, 1, "busy-sid", other, time.Now().Add(-2*time.Hour), time.Now())
	t.Setenv(claudecode.EnvActive, "")
	a := addTask(t, "a", "-r", "o/glyph", "-s", "ready")
	b := addTask(t, "b", "-r", "o/other", "-s", "ready")
	t.Setenv(claudecode.EnvActive, "1")
	so, se, code := runSplit(t, "--json", "done", a, b)
	if code != 2 {
		t.Fatalf("the busy occupant must refuse the batch: exit %d\n%s%s", code, so, se)
	}
	if strings.Contains(se, "went through") || strings.Contains(so, "session_warn") {
		t.Fatalf("a refused run must not print the idle warning:\n%s%s", so, se)
	}
}
