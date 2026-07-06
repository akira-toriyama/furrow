package gitrepo

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEmptyCloneLandsOnMain is the anchor for the process-env git isolation
// wired into this package's TestMain (see internal/gittest). It proves two
// things at once: an empty bare repo made with -b main, when cloned, checks out
// "main" deterministically, and the isolated global config is the one in force
// for the git subprocess (init.defaultBranch=main and gpgsign forced false,
// never the developer's ambient values). Without the isolation these config
// reads would surface the host machine's settings (or exit 1 when unset).
func TestEmptyCloneLandsOnMain(t *testing.T) {
	git := gitOrSkip(t)

	origin := t.TempDir()
	runGitT(t, git, origin, "init", "-q", "--bare", "-b", "main")

	clone := filepath.Join(t.TempDir(), "clone")
	runGitT(t, git, filepath.Dir(clone), "clone", "-q", origin, clone)

	// The empty clone has an unborn HEAD (no commits), but symbolic-ref still
	// names the branch it will land its first commit on — it must be "main",
	// not the ambient default. (rev-parse --abbrev-ref needs a resolvable HEAD;
	// symbolic-ref reads the ref target directly, so it works pre-first-commit.)
	if got := strings.TrimSpace(runGitT(t, git, clone, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("empty clone HEAD = %q, want main", got)
	}

	// init.defaultBranch resolves to main from the isolated global (a developer
	// whose global says "master" — or leaves it unset — would surface here
	// without the isolation).
	if got := strings.TrimSpace(runGitT(t, git, clone, "config", "--get", "init.defaultBranch")); got != "main" {
		t.Errorf("init.defaultBranch = %q, want main (isolated global not in force)", got)
	}

	// commit.gpgsign is forced false by the isolated global, never the
	// developer's gpgsign=true (the exact flake this isolation kills).
	if got := strings.TrimSpace(runGitT(t, git, clone, "config", "--get", "commit.gpgsign")); got != "false" {
		t.Errorf("commit.gpgsign = %q, want false (isolated global not in force)", got)
	}
}
