package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRevisitStaleDaysDefault(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.RevisitStaleDays != DefaultRevisitStaleDays {
		t.Errorf("default stale_days = %d, want %d", c.RevisitStaleDays, DefaultRevisitStaleDays)
	}
}

func TestRevisitStaleDaysOverrideAndClamp(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		want    int
		warnHas string // substring expected in a warning, "" = expect no warning
	}{
		{"explicit value", "[revisit]\nstale_days = 14\n", 14, ""},
		{"zero disables (accepted, no warn)", "[revisit]\nstale_days = 0\n", 0, ""},
		{"negative clamps to default + warn", "[revisit]\nstale_days = -5\n", DefaultRevisitStaleDays, "revisit.stale_days"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeTOML(t, c.toml)
			cfg, warn, err := Load(p)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.RevisitStaleDays != c.want {
				t.Errorf("stale_days = %d, want %d", cfg.RevisitStaleDays, c.want)
			}
			joined := strings.Join(warn, "\n")
			if c.warnHas == "" {
				if strings.Contains(joined, "revisit.stale_days") {
					t.Errorf("expected no stale_days warning, got: %v", warn)
				}
			} else if !strings.Contains(joined, c.warnHas) {
				t.Errorf("expected warning containing %q, got: %v", c.warnHas, warn)
			}
		})
	}
}
