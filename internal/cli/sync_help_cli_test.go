package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// `sync --help` is where an agent reads the progress object's keys and the
// failure kinds it must branch on; both are closed vocabularies, and the help
// had lost two keys (foreign_files, switches) and never named three kinds
// (sync-op-in-progress, no-git-repo, git-missing).
func TestSyncHelpNamesEveryProgressKeyAndSyncKind(t *testing.T) {
	initStore(t)
	help := mustRun(t, "sync", "--help")
	for _, key := range app.Vocabularies()["sync-progress-keys"] {
		if !strings.Contains(help, key) {
			t.Errorf("sync --help does not name progress key %q", key)
		}
	}
	for _, kind := range core.ErrorKindList() {
		named := strings.HasPrefix(kind, "sync-") || kind == core.KindGitMissing || kind == core.KindNoGitRepo || kind == core.KindBodyConflictMarker
		if named && !strings.Contains(help, kind) {
			t.Errorf("sync --help does not name failure kind %q", kind)
		}
	}
}
