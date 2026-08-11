package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// t-1dre: the discovery give-up error used to say "run `furrow init`"
// unconditionally — on a machine WITH configured boards that advice grows a
// stray local board that shadows the central one (the incident this repo's
// .gitignore memorializes), and it contradicted doctor's own dir-unresolved
// wording for the same state. The remedy now depends on the machine.
func TestDiscoveryErrorRemedyMatchesMachineState(t *testing.T) {
	real := initGlobalConfig(t) // one real [[board]] with scopes elsewhere
	t.Setenv(app.EnvDir, "")
	t.Setenv(app.EnvBoard, "")
	t.Chdir(t.TempDir()) // outside every scope, no local .furrow anywhere up

	fe, _ := runErr(t, "ls")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("out-of-scope ls must be exit 2, got %+v", fe)
	}
	if strings.Contains(fe.Msg, "run `furrow init`") {
		t.Errorf("a machine WITH boards must not be steered to init: %q", fe.Msg)
	}
	for _, want := range []string{"scopes", "FURROW_BOARD"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("remedy must mention %q, got: %q", want, fe.Msg)
		}
	}
	found := false
	for _, c := range fe.Candidates {
		if c == real {
			found = true
		}
	}
	if !found {
		t.Errorf("candidates must name the configured board path %q, got %v", real, fe.Candidates)
	}

	// A machine with NO boards keeps the init steer.
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", empty)
	if err := os.MkdirAll(filepath.Join(empty, "furrow"), 0o755); err != nil {
		t.Fatal(err)
	}
	fe, _ = runErr(t, "ls")
	if fe == nil || !strings.Contains(fe.Msg, "furrow init") {
		t.Errorf("a board-less machine keeps the init remedy, got %+v", fe)
	}
}
