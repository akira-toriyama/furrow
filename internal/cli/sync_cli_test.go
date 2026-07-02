package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `furrow sync` outside a git repo: validation exit (2), and the progress
// object still lands on stdout — the "emitted on success AND failure" half of
// the contract that a plain error path would silently drop.
func TestSyncOutsideGitPrintsProgressAndExits2(t *testing.T) {
	initStore(t) // t.TempDir board — not a git repo

	out, code := run(t, "--json", "sync")
	if code != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", code, core.CodeValidation)
	}
	for _, key := range []string{`"committed": false`, `"pulled": false`, `"pushed": false`, `"conflict": false`} {
		if !strings.Contains(out, key) {
			t.Errorf("progress object missing %s on failure:\n%s", key, out)
		}
	}

	// Human mode prints the terse one-liner instead.
	hout, hcode := run(t, "sync")
	if hcode != int(core.CodeValidation) {
		t.Errorf("human exit = %d, want %d", hcode, core.CodeValidation)
	}
	if !strings.Contains(hout, "sync: committed=false") {
		t.Errorf("human summary missing:\n%s", hout)
	}
}
