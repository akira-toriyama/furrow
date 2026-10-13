package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// t-hyr0: the [session] section lives in the USER config (per-machine), with
// an enum guard and a positive busy window — clamp-don't-reject like the
// boards beside it.
func TestLoadGlobal_SessionDefaultsOverridesAndClamps(t *testing.T) {
	g, warn, err := LoadGlobal(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(warn) != 0 || g.Session != DefaultGlobalSession() {
		t.Errorf("a missing file is the default session (%+v) with no warning; got %+v / %v", DefaultGlobalSession(), g.Session, warn)
	}
	cases := []struct {
		name    string
		toml    string
		want    GlobalSession
		warnHas string
	}{
		{"absent section", "[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\"]\n", DefaultGlobalSession(), ""},
		{"refuse", "[session]\nguard = \"refuse\"\n", GlobalSession{Guard: SessionGuardRefuse, BusySeconds: 300}, ""},
		{"warn + window", "[session]\nguard = \"warn\"\nbusy_seconds = 30\n", GlobalSession{Guard: SessionGuardWarn, BusySeconds: 30}, ""},
		{"off", "[session]\nguard = \"off\"\n", GlobalSession{Guard: SessionGuardOff, BusySeconds: 300}, ""},
		{"unknown guard clamps to refuse", "[session]\nguard = \"maybe\"\n", DefaultGlobalSession(), "session.guard \"maybe\" is not one of refuse|warn|off"},
		{"zero window is no longer warn-only", "[session]\nbusy_seconds = 0\n", DefaultGlobalSession(), "session.guard = \"warn\""},
		{"negative window clamps", "[session]\nbusy_seconds = -5\n", DefaultGlobalSession(), "session.busy_seconds -5"},
		{"unknown key warns, section survives", "[session]\nguard = \"warn\"\nbusy = 1\n", GlobalSession{Guard: SessionGuardWarn, BusySeconds: 300}, "unknown key \"session.busy\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, warn, err := LoadGlobal(writeGlobal(t, c.toml))
			if err != nil {
				t.Fatalf("LoadGlobal: %v", err)
			}
			if g.Session != c.want {
				t.Errorf("session = %+v, want %+v", g.Session, c.want)
			}
			joined := strings.Join(warn, "\n")
			if c.warnHas == "" && joined != "" {
				t.Errorf("unexpected warning: %s", joined)
			}
			if c.warnHas != "" && !strings.Contains(joined, c.warnHas) {
				t.Errorf("warning %q missing %q", joined, c.warnHas)
			}
		})
	}
}

// A wrong-typed [session] key takes the salvage path: the boards survive, the
// bad key is dropped with a warning, the good key beside it still reads.
func TestLoadGlobal_SessionTypeErrorSalvages(t *testing.T) {
	g, warn, err := LoadGlobal(writeGlobal(t, "[[board]]\npath = \"/b/.furrow\"\nscopes = [\"/b\"]\n\n[session]\nguard = 1\nbusy_seconds = 42\n"))
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(g.Boards) != 1 {
		t.Errorf("the board must survive a [session] type error: %+v", g.Boards)
	}
	if g.Session != (GlobalSession{Guard: SessionGuardRefuse, BusySeconds: 42}) {
		t.Errorf("session = %+v, want the default guard with the window that still read", g.Session)
	}
	if joined := strings.Join(warn, "\n"); !strings.Contains(joined, "[session]: key \"guard\" has the wrong type") {
		t.Errorf("warn = %v, want the wrong-typed key named", warn)
	}
}

// The user config's own keys are `config set --user`'s vocabulary; the board
// config no longer knows [session] at all.
func TestSessionKeysAreUserKeysNotBoardKeys(t *testing.T) {
	for _, k := range []string{"session.guard", "session.busy_seconds"} {
		if _, _, ok := ResolveKey(UserKeys(), k); !ok {
			t.Errorf("%s must be a `config set --user` key (reflection over rawGlobal)", k)
		}
		if _, _, ok := ResolveKey(BoardKeys(), k); ok {
			t.Errorf("%s must not be a board config key any more", k)
		}
	}
	for _, k := range UserKeys() {
		if k.Section == "" || k.Section == "board" {
			t.Errorf("UserKeys must not expose the [[board]] array as a key: %+v", k)
		}
	}
	if got := KeyVocabulary(UserKeys()); strings.Join(got, ",") != "session.guard,session.busy_seconds" {
		t.Errorf("UserKeys = %v", got)
	}
}
