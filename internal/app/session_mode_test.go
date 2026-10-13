package app

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/claudecode"
	"github.com/akira-toriyama/furrow/internal/config"
)

// t-hyr0: `guard = "warn"` never refuses — a working occupant is recorded as
// a clash that still says Busy, so the CLI's line says working, and the
// envelope's session_warn carries it.
func TestSessionGuardWarnModeNeverRefuses(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	a.Session.Guard = config.SessionGuardWarn
	if _, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("guard = warn never refuses a working occupant: %v", err)
	}
	w := a.TakeSessionWarn()
	if len(w) != 1 || !w[0].Busy {
		t.Fatalf("the clash is recorded as a WORKING one: %+v", w)
	}
	// Unknown activity too: the one escape a non-add write has when an
	// occupant's transcript cannot be found.
	b, _ := guardedApp(t, 0, true)
	b.Session.Guard = config.SessionGuardWarn
	if _, err := b.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("guard = warn never refuses: %v", err)
	}
	if w := b.TakeSessionWarn(); len(w) != 1 || !w[0].Busy {
		t.Fatalf("warned as busy: %+v", w)
	}
}

// The busy window is the user config's, not the board's.
func TestSessionGuardWindowComesFromTheUserConfig(t *testing.T) {
	a, _ := guardedApp(t, 10*time.Second, false)
	a.Session.BusySeconds = 5
	if _, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("an occupant silent past the window is idle: %v", err)
	}
	if w := a.TakeSessionWarn(); len(w) != 1 || w[0].Busy {
		t.Fatalf("idle clash expected: %+v", w)
	}
}

// Open reads the mode: under the agent env the guard is armed unless the user
// config says off, and the window rides along. A human shell (no env) never
// reads the section.
func TestOpenArmsTheGuardByTheUserConfig(t *testing.T) {
	store := filepath.Join(t.TempDir(), ".furrow")
	if _, err := Init(filepath.Dir(store)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, store)
	t.Setenv(claudecode.EnvConfigDir, t.TempDir())
	t.Setenv(claudecode.EnvActive, "1")
	t.Setenv(claudecode.EnvPID, strconv.Itoa(os.Getpid()))
	t.Setenv(claudecode.EnvSessionID, "self")

	cases := []struct {
		name  string
		toml  string
		armed bool
		want  config.GlobalSession
	}{
		{"no user config", "", true, config.DefaultGlobalSession()},
		{"refuse", "[session]\nguard = \"refuse\"\nbusy_seconds = 7\n", true, config.GlobalSession{Guard: config.SessionGuardRefuse, BusySeconds: 7}},
		{"warn", "[session]\nguard = \"warn\"\n", true, config.GlobalSession{Guard: config.SessionGuardWarn, BusySeconds: 300}},
		{"off", "[session]\nguard = \"off\"\n", false, config.GlobalSession{Guard: config.SessionGuardOff, BusySeconds: 300}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.toml == "" {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			} else {
				writeGlobalConfig(t, c.toml)
			}
			a, err := Open(filepath.Dir(store))
			if err != nil {
				t.Fatal(err)
			}
			if (a.Sessions != nil) != c.armed {
				t.Errorf("armed = %v, want %v", a.Sessions != nil, c.armed)
			}
			if a.Session != c.want {
				t.Errorf("session = %+v, want %+v", a.Session, c.want)
			}
			if n := a.TakeSessionNotes(); len(n) != 0 {
				t.Errorf("a readable user config leaves no note: %v", n)
			}
		})
	}
	t.Run("human shell", func(t *testing.T) {
		writeGlobalConfig(t, "[session]\nguard = \"warn\"\n")
		t.Setenv(claudecode.EnvActive, "")
		a, err := Open(filepath.Dir(store))
		if err != nil {
			t.Fatal(err)
		}
		if a.Sessions != nil || a.Session != config.DefaultGlobalSession() {
			t.Errorf("outside an agent the guard is off and the section unread: %+v", a.Session)
		}
	})
	t.Run("unreadable user config", func(t *testing.T) {
		writeGlobalConfig(t, "[[board]\npath=\n")
		a, err := Open(filepath.Dir(store))
		if err != nil {
			t.Fatal(err)
		}
		if a.Sessions == nil || a.Session != config.DefaultGlobalSession() {
			t.Errorf("a broken user config runs the guard on its defaults: %+v", a.Session)
		}
		if n := a.TakeSessionNotes(); len(n) != 1 {
			t.Errorf("one stand-down note names the broken file: %v", n)
		}
	})
}

// Doctor skips the registry probe when the guard is off: nothing reads the
// registry then, so a stale entry is nobody's problem.
func TestDoctorSkipsTheRegistryProbeWhenTheGuardIsOff(t *testing.T) {
	cc := t.TempDir()
	t.Setenv(claudecode.EnvConfigDir, cc)
	if err := os.MkdirAll(filepath.Join(cc, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cc, "sessions", "1.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	has := func(r *DoctorReport) bool {
		for _, p := range r.Problems {
			if p.Code == "session-registry-unreadable" {
				return true
			}
		}
		return false
	}
	writeGlobalConfig(t, "[session]\nguard = \"refuse\"\n")
	r, err := Doctor(t.Context(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !has(r) {
		t.Fatalf("with the guard on, the unreadable entry is a finding: %+v", r.Problems)
	}
	writeGlobalConfig(t, "[session]\nguard = \"off\"\n")
	if r, err = Doctor(t.Context(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if has(r) {
		t.Fatalf("with the guard off, the registry is not probed: %+v", r.Problems)
	}
}
