package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDueTimezoneDefaultLoadAndClamp(t *testing.T) {
	c, warn, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DueTimezone != nil || c.DueTimezoneName != "" {
		t.Errorf("unset timezone = (%v, %q), want (nil, \"\") so the process zone still applies",
			c.DueTimezone, c.DueTimezoneName)
	}
	if len(warn) != 0 {
		t.Errorf("a board with no [due].timezone must warn about nothing, got %v", warn)
	}

	cases := []struct {
		name     string
		toml     string
		wantName string // "" = unset, i.e. clamped back to the process zone
		warnHas  string
	}{
		{"an IANA name loads", "[due]\ntimezone = \"Asia/Tokyo\"\n", "Asia/Tokyo", ""},
		{"UTC loads", "[due]\ntimezone = \"UTC\"\n", "UTC", ""},
		{"an unloadable name clamps to the process zone + warns",
			"[due]\ntimezone = \"Mars/Olympus\"\n", "", "due.timezone"},
		// An empty string must not reach time.LoadLocation: it loads UTC there,
		// which would silently move a board with `timezone = ""` off its own
		// process zone.
		{"an empty value is simply unset", "[due]\ntimezone = \"\"\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, warn, err := Load(writeTOML(t, tc.toml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DueTimezoneName != tc.wantName {
				t.Errorf("timezone name = %q, want %q", cfg.DueTimezoneName, tc.wantName)
			}
			if (cfg.DueTimezone == nil) != (tc.wantName == "") {
				t.Errorf("timezone location = %v, but name is %q — the two must agree",
					cfg.DueTimezone, cfg.DueTimezoneName)
			}
			joined := strings.Join(warn, "\n")
			if tc.warnHas == "" && joined != "" {
				t.Errorf("unexpected warning: %s", joined)
			}
			if tc.warnHas != "" && !strings.Contains(joined, tc.warnHas) {
				t.Errorf("warnings %q do not mention %q", joined, tc.warnHas)
			}
		})
	}
}

// The whole point of declaring the zone on the board is that the same spelling
// means the same instant on the operator's machine and on the UTC CI runner
// that closes tasks through the identical write path.
func TestDueTimezoneIsTheSameOffsetRegardlessOfProcessZone(t *testing.T) {
	cfg, _, err := Load(writeTOML(t, "[due]\ntimezone = \"Asia/Tokyo\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DueTimezone == nil {
		t.Fatal("Asia/Tokyo did not load — the binary must embed tzdata (see cmd/furrow/main.go)")
	}
	// Midsummer and midwinter: Asia/Tokyo has no DST, so both are +09:00. A zone
	// that failed to load would fall back to UTC and report 0 here.
	for _, when := range []time.Time{
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	} {
		if _, off := when.In(cfg.DueTimezone).Zone(); off != 9*60*60 {
			t.Errorf("%s: offset = %ds, want 32400s", when.Format(time.RFC3339), off)
		}
	}
}
