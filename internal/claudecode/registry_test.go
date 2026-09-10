package claudecode

import (
	"os"
	"path/filepath"
	"strconv"
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
	if len(unreadable) != 2 {
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
}

func TestDefaultDirHonorsOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/cc-alt/")
	if d, err := DefaultDir(); err != nil || d != "/tmp/cc-alt" {
		t.Fatalf("override: %q %v", d, err)
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
	if processAlive(1<<30 - 1) {
		t.Fatal("an absurd pid must be dead")
	}
}
