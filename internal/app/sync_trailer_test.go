package app

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSyncTrailerShape(t *testing.T) {
	tr := syncTrailer("/tmp/some board")
	if !strings.HasPrefix(tr, "Furrow-sync: ") {
		t.Fatalf("trailer = %q, want the Furrow-sync: prefix", tr)
	}
	if strings.Contains(tr, "\n") {
		t.Errorf("a git trailer is one line, got %q", tr)
	}
	if host, err := os.Hostname(); err == nil && host != "" && !strings.Contains(tr, "host="+host) {
		t.Errorf("trailer %q missing host=%s", tr, host)
	}
	if want := "pid=" + strconv.Itoa(os.Getpid()); !strings.Contains(tr, want) {
		t.Errorf("trailer %q missing %s", tr, want)
	}
	// dir stays LAST so a space inside it cannot split an earlier field.
	if !strings.HasSuffix(tr, " dir=/tmp/some board") {
		t.Errorf("trailer %q must end with the dir field", tr)
	}
	if len(parentChain(4)) > 0 && !strings.Contains(tr, "via=") {
		t.Errorf("trailer %q missing via= although the ancestry walk works here", tr)
	}

	// A dirless call (defensive) drops the field instead of writing dir=.
	if tr := syncTrailer(""); strings.Contains(tr, "dir=") {
		t.Errorf("empty dir must be omitted, got %q", tr)
	}
}

func TestNormalizeComm(t *testing.T) {
	for in, want := range map[string]string{
		"zsh":              "zsh",
		"Code Helper (Plu": "Code_Helper_(Plu",
		"  a \t b  ":       "a_b",
	} {
		if got := normalizeComm(in); got != want {
			t.Errorf("normalizeComm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParentChainWalks(t *testing.T) {
	chain := parentChain(4)
	if len(chain) > 4 {
		t.Fatalf("chain %v longer than the cap", chain)
	}
	// The test process was launched by SOMETHING, so an empty chain on a
	// platform that exposes a parent pid is a defect in the walk, not in the
	// environment.
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if len(chain) == 0 {
			t.Fatal("empty ancestry on a platform where /proc or ps works")
		}
	}
	for _, c := range chain {
		if c == "" || strings.ContainsAny(c, "\n< \t") {
			t.Errorf("comm %q would corrupt the via= field (spaces must be normalized to _)", c)
		}
	}
}
