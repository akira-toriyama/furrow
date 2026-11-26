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
	if c.RevisitStaleDays != defaultRevisitStaleDays {
		t.Errorf("default stale_days = %d, want %d", c.RevisitStaleDays, defaultRevisitStaleDays)
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
		{"negative clamps to default + warn", "[revisit]\nstale_days = -5\n", defaultRevisitStaleDays, "revisit.stale_days"},
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

// [revisit].parked_lanes is off by default — a board that never set it reads
// exactly as it did before the key existed — and is kept to lanes that ARE
// parked: an open lane is read already, and a closed task is never revisited.
func TestRevisitParkedLanes(t *testing.T) {
	c, _, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.RevisitParkedLanes) != 0 || c.IsRevisitParked("waiting") {
		t.Errorf("default parked_lanes must be empty, got %v", c.RevisitParkedLanes)
	}

	custom := "[lanes]\norder = [\"todo\", \"hold\", \"frozen\", \"shipped\"]\ndone = \"shipped\"\nterminal = [\"hold\", \"frozen\", \"shipped\"]\n"
	cases := []struct {
		name    string
		toml    string
		want    []string // lanes revisit keeps reading
		warnHas string   // substring expected in a warning, "" = expect none
	}{
		{"a parked lane", "[revisit]\nparked_lanes = [\"waiting\"]\n", []string{"waiting"}, ""},
		{"two parked lanes", "[revisit]\nparked_lanes = [\"waiting\", \"icebox\"]\n", []string{"waiting", "icebox"}, ""},
		{"explicit empty list is off", "[revisit]\nparked_lanes = []\n", nil, ""},
		{"an unknown lane is dropped", "[revisit]\nparked_lanes = [\"waiting\", \"nope\"]\n", []string{"waiting"}, `revisit.parked_lanes entry "nope" is not a lane`},
		{"the done lane is dropped", "[revisit]\nparked_lanes = [\"done\"]\n", nil, `revisit.parked_lanes entry "done" is the done lane`},
		{"an open lane is dropped", "[revisit]\nparked_lanes = [\"backlog\"]\n", nil, `revisit.parked_lanes entry "backlog" is not a terminal lane`},
		{"a custom scheme names its own lanes", custom + "[revisit]\nparked_lanes = [\"hold\"]\n", []string{"hold"}, ""},
		{"a custom done lane is dropped by role, not by name", custom + "[revisit]\nparked_lanes = [\"shipped\"]\n", nil, `revisit.parked_lanes entry "shipped" is the done lane`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, warn, err := Load(writeTOML(t, c.toml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.RevisitParkedLanes) != len(c.want) {
				t.Errorf("parked_lanes = %v, want %v", cfg.RevisitParkedLanes, c.want)
			}
			for _, l := range c.want {
				if !cfg.IsRevisitParked(l) {
					t.Errorf("lane %q must be read, got %v", l, cfg.RevisitParkedLanes)
				}
			}
			joined := strings.Join(warn, "\n")
			if c.warnHas == "" {
				if strings.Contains(joined, "revisit.parked_lanes") {
					t.Errorf("expected no parked_lanes warning, got: %v", warn)
				}
			} else if !strings.Contains(joined, c.warnHas) {
				t.Errorf("expected warning containing %q, got: %v", c.warnHas, warn)
			}
		})
	}
}
