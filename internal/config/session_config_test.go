package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionBusySecondsDefaultOverrideAndClamp(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SessionBusySeconds != DefaultSessionBusySeconds {
		t.Errorf("default busy_seconds = %d, want %d", c.SessionBusySeconds, DefaultSessionBusySeconds)
	}
	cases := []struct {
		name    string
		toml    string
		want    int
		warnHas string
	}{
		{"explicit value", "[session]\nbusy_seconds = 30\n", 30, ""},
		{"zero is warn-only (accepted, no warn)", "[session]\nbusy_seconds = 0\n", 0, ""},
		{"negative clamps to default + warn", "[session]\nbusy_seconds = -1\n", DefaultSessionBusySeconds, "session.busy_seconds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, warn, err := Load(writeTOML(t, c.toml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SessionBusySeconds != c.want {
				t.Errorf("busy_seconds = %d, want %d", cfg.SessionBusySeconds, c.want)
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

func TestSessionKeyIsSettable(t *testing.T) {
	if _, _, ok := ResolveKey(BoardKeys(), "session.busy_seconds"); !ok {
		t.Fatal("session.busy_seconds must be a `furrow config set` key (reflection over raw)")
	}
}
