package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/claudecode"
)

func countDoctorCode(r *DoctorReport, code string) int {
	n := 0
	for _, p := range r.Problems {
		if p.Code == code {
			n++
		}
	}
	return n
}

func TestDoctorSessionRegistryUnreadable(t *testing.T) {
	cc := t.TempDir()
	t.Setenv(claudecode.EnvConfigDir, cc)
	// No registry at all: a machine without Claude Code raises nothing.
	if n := countDoctorCode(mustDoctor(t, t.TempDir()), "session-registry-unreadable"); n != 0 {
		t.Fatalf("no registry, no finding; got %d", n)
	}
	sdir := filepath.Join(cc, "sessions")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "1.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := mustDoctor(t, t.TempDir())
	// The entry AND the whole registry (nothing readable) are each a finding.
	if n := countDoctorCode(r, "session-registry-unreadable"); n != 2 {
		t.Fatalf("want 2 session-registry-unreadable findings, got %d: %+v", n, r.Problems)
	}
	if r.Healthy {
		t.Error("a warn finding makes the machine unhealthy")
	}
}
