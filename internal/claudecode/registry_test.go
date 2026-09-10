package claudecode

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeEntry plants one sessions/<pid>.json in dir, plus (when active is
// non-zero) a transcript whose mtime is active.
func writeEntry(t *testing.T, dir string, pid int, id, cwd string, started time.Time, active time.Time) {
	t.Helper()
	sdir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":` + itoa(pid) + `,"sessionId":"` + id + `","cwd":"` + cwd + `","startedAt":` + itoa64(started.UnixMilli()) + `,"kind":"interactive","name":"n-` + id + `","extra":true}`
	if err := os.WriteFile(filepath.Join(sdir, itoa(pid)+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !active.IsZero() {
		tdir := filepath.Join(dir, "projects", mangleCWD(cwd))
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
}

func itoa(i int) string     { return strconv.Itoa(i) }
func itoa64(i int64) string { return strconv.FormatInt(i, 10) }

func TestMangleCWD(t *testing.T) {
	if got := mangleCWD("/Volumes/workspace/github.com/o/r"); got != "-Volumes-workspace-github-com-o-r" {
		t.Fatalf("mangle = %q", got)
	}
	// One dash per CHARACTER, not per byte (see transcriptPath).
	if got := mangleCWD("/w/仕事/r"); got != "-w----r" {
		t.Fatalf("rune-wise mangle = %q", got)
	}
}

func TestScanReadsLiveEntriesAndTranscriptMtime(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	active := time.Date(2026, 9, 10, 1, 30, 0, 0, time.UTC)
	writeEntry(t, dir, 11, "aa", "/w/one", started, active)
	writeEntry(t, dir, 22, "bb", "/w/two", started, time.Time{}) // no transcript
	writeEntry(t, dir, 33, "cc", "/w/dead", started, active)
	r := Registry{Dir: dir, alive: func(pid int) bool { return pid != 33 }}
	got, unreadable, err := r.Scan()
	if err != nil || len(unreadable) != 0 {
		t.Fatalf("scan: %v %v", err, unreadable)
	}
	if len(got) != 2 || got[0].PID != 11 || got[1].PID != 22 {
		t.Fatalf("sessions = %+v", got)
	}
	if got[0].ID != "aa" || got[0].CWD != "/w/one" || !got[0].StartedAt.Equal(started) || !got[0].LastActive.Equal(active) || got[0].Name != "n-aa" {
		t.Errorf("entry 11 = %+v", got[0])
	}
	if !got[1].LastActive.IsZero() {
		t.Errorf("entry 22 should have unknown activity: %+v", got[1])
	}
}

// setTranscript overwrites the transcript writeEntry planted for (cwd, id)
// with content, keeping its mtime at active.
func setTranscript(t *testing.T, dir, cwd, id, content string, active time.Time) {
	t.Helper()
	p := filepath.Join(dir, "projects", mangleCWD(cwd), id+".jsonl")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, active, active); err != nil {
		t.Fatal(err)
	}
}

const (
	recEndTurn   = `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]},"isSidechain":false}` + "\n"
	recToolUse   = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash"}]},"isSidechain":false}` + "\n"
	recUser      = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result"}]},"isSidechain":false}` + "\n"
	recBridge    = `{"type":"bridge-session","bridgeSessionId":"x","lastSequenceNum":3}` + "\n"
	recAttach    = `{"type":"attachment","attachment":{"type":"hook_success"}}` + "\n"
	recSidechain = `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"sub"}]},"isSidechain":true}` + "\n"
)

func TestScanReadsTurnEndedOffTheTranscriptTail(t *testing.T) {
	started := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	active := time.Date(2026, 9, 10, 1, 30, 0, 0, time.UTC)
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"end_turn last: the turn ended", recUser + recEndTurn, true},
		{"tool_use last: mid-turn", recUser + recToolUse, false},
		{"a tool result last: mid-turn", recToolUse + recUser, false},
		{"bookkeeping after the end_turn is skipped", recUser + recEndTurn + recBridge + recAttach, true},
		{"bookkeeping after a tool_use is skipped too", recToolUse + recBridge, false},
		{"a subagent's end_turn is not the session's", recToolUse + recSidechain, false},
		{"a stop hook's feedback prompt reopens the turn", recEndTurn + `{"type":"user","message":{"role":"user","content":"Stop hook feedback: fix the report"}}` + "\n", false},
		{"an assistant record with another stop reason is not ended", recUser + `{"type":"assistant","message":{"stop_reason":"max_tokens"}}` + "\n", false},
		{"no message record at all: unknown, not ended", recBridge + recAttach, false},
		{"unparsable tail: unknown, not ended", "{not json\n", false},
		{"empty transcript: unknown, not ended", "", false},
		{"a garbage line after the end_turn does not hide it", recEndTurn + "{cut\n", true},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			id := "s" + itoa(i)
			writeEntry(t, dir, 11, id, "/w/one", started, active)
			setTranscript(t, dir, "/w/one", id, c.content, active)
			r := Registry{Dir: dir, alive: func(int) bool { return true }}
			got, _, err := r.Scan()
			if err != nil || len(got) != 1 {
				t.Fatalf("scan: %+v %v", got, err)
			}
			if got[0].TurnEnded != c.want {
				t.Fatalf("TurnEnded = %v, want %v", got[0].TurnEnded, c.want)
			}
			if !got[0].LastActive.Equal(active) {
				t.Fatalf("LastActive must still come from the mtime: %v", got[0].LastActive)
			}
		})
	}
}

func TestTurnEndedReadsOnlyTheTailOfALongTranscript(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	active := time.Date(2026, 9, 10, 1, 30, 0, 0, time.UTC)
	writeEntry(t, dir, 11, "long", "/w/one", started, active)
	// A transcript far larger than the window, whose window starts mid-record:
	// the cut first line must be skipped, not mistaken for a format change.
	var b strings.Builder
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 1000) + `"}}` + "\n"
	for b.Len() < 3*transcriptTailBytes {
		b.WriteString(filler)
	}
	b.WriteString(recToolUse)
	b.WriteString(recUser)
	b.WriteString(recEndTurn)
	b.WriteString(recBridge)
	setTranscript(t, dir, "/w/one", "long", b.String(), active)
	r := Registry{Dir: dir, alive: func(int) bool { return true }}
	got, _, err := r.Scan()
	if err != nil || len(got) != 1 || !got[0].TurnEnded {
		t.Fatalf("long transcript: %+v %v", got, err)
	}
	// A transcript whose window holds only cut/bookkeeping lines: unknown.
	setTranscript(t, dir, "/w/one", "long", strings.Repeat(recBridge, transcriptTailBytes/len(recBridge)+2), active)
	if got, _, _ := r.Scan(); len(got) != 1 || got[0].TurnEnded {
		t.Fatalf("no message record in the window must read as not ended: %+v", got)
	}
	// No transcript at all: neither activity nor a turn.
	if err := os.Remove(filepath.Join(dir, "projects", mangleCWD("/w/one"), "long.jsonl")); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := r.Scan(); len(got) != 1 || got[0].TurnEnded || !got[0].LastActive.IsZero() {
		t.Fatalf("missing transcript: %+v", got)
	}
}

func TestScanSkipsBrokenEntryButFailsWhenNothingParses(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	writeEntry(t, dir, 11, "aa", "/w/one", started, time.Time{})
	sdir := filepath.Join(dir, "sessions")
	if err := os.WriteFile(filepath.Join(sdir, "12.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "13.json"), []byte(`{"process":13,"dir":"/x"}`), 0o644); err != nil {
		t.Fatal(err) // a renamed-field entry: parses, but lacks what the guard needs
	}
	if err := os.WriteFile(filepath.Join(sdir, "14.json"), []byte(`{"pid":14,"sid":"x","cwd":"/x","startedAt":1}`), 0o644); err != nil {
		t.Fatal(err) // only sessionId renamed: no transcript could ever be found — unreadable, not "busy forever"
	}
	if err := os.WriteFile(filepath.Join(sdir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Registry{Dir: dir, alive: func(int) bool { return true }}
	got, unreadable, err := r.Scan()
	if err != nil {
		t.Fatalf("one good entry must keep the scan usable: %v", err)
	}
	if len(got) != 1 || got[0].PID != 11 {
		t.Fatalf("sessions = %+v", got)
	}
	if len(unreadable) != 3 {
		t.Fatalf("unreadable = %v", unreadable)
	}
	// Remove the good one: every entry unreadable is the format-change shape.
	if err := os.Remove(filepath.Join(sdir, "11.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Scan(); err == nil {
		t.Fatal("a registry with no readable entry must be an error (stand down, say so)")
	}
}

func TestScanNoRegistryIsNoSessions(t *testing.T) {
	r := Registry{Dir: t.TempDir()}
	got, unreadable, err := r.Scan()
	if err != nil || got != nil || unreadable != nil {
		t.Fatalf("empty dir: %v %v %v", got, unreadable, err)
	}
	if r.Exists() {
		t.Fatal("Exists must be false without sessions/")
	}
}

func TestSelfReadsEnv(t *testing.T) {
	t.Setenv(EnvActive, "")
	if _, _, ok := Self(); ok {
		t.Fatal("no CLAUDECODE means no session")
	}
	t.Setenv(EnvActive, "1")
	t.Setenv(EnvPID, "4242")
	t.Setenv(EnvSessionID, "sid")
	pid, id, ok := Self()
	if !ok || pid != 4242 || id != "sid" {
		t.Fatalf("self = %d %q %v", pid, id, ok)
	}
	t.Setenv(EnvPID, "garbage")
	if pid, _, ok := Self(); !ok || pid != 0 {
		t.Fatalf("unparsable pid must be 0 under Claude Code: %d %v", pid, ok)
	}
	t.Setenv(EnvPID, "-3")
	if pid, _, ok := Self(); !ok || pid != 0 {
		t.Fatalf("a negative pid must be 0: %d %v", pid, ok)
	}
}

func TestDefaultDirHonorsOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/cc-alt/")
	if d, err := DefaultDir(); err != nil || d != "/tmp/cc-alt" {
		t.Fatalf("override: %q %v", d, err)
	}
	t.Setenv(EnvConfigDir, "rel/dir")
	if d, err := DefaultDir(); err != nil || !filepath.IsAbs(d) {
		t.Fatalf("a relative override resolves to an absolute path: %q %v", d, err)
	}
	t.Setenv(EnvConfigDir, "")
	d, err := DefaultDir()
	if err != nil || filepath.Base(d) != ".claude" {
		t.Fatalf("default: %q %v", d, err)
	}
}

func TestProcessAliveSelfAndBogus(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("own pid must be alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Fatal("pid 0 / a negative pid name a process GROUP to kill(2); never a session")
	}
	if processAlive(1<<30 - 1) {
		t.Fatal("an absurd pid must be dead")
	}
}
