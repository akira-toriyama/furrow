package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/testutil"
)

// shadowedBoardLayout builds a central board configured with a [[board]] scope
// and chdirs INTO the board's own tree — the scope-shadowed layout, where
// discovery runs source=local and the [[board]]'s repo scope is lost.
func shadowedBoardLayout(t *testing.T) {
	t.Helper()
	t.Setenv(app.EnvBoard, "")
	t.Setenv(app.EnvDir, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	central := filepath.Join(scope, "projects")
	if _, err := app.Init(central); err != nil {
		t.Fatal(err)
	}
	testutil.UserConfig(t, testutil.BoardEntry(filepath.Join(central, app.DirName), "me/projects", scope))
	t.Chdir(central) // inside the board's own tree
}

// A bare add inside the board's own tree drafts — and now says so on stderr at
// the moment it happens (doctor's scope-shadowed finding, raised at the bite).
func TestAdd_ShadowedScopeWarnsOnDraft(t *testing.T) {
	shadowedBoardLayout(t)

	so, se := mustSplit(t, "add", "bare add here")
	if !strings.Contains(se, "drafted") || !strings.Contains(se, "scope-shadowed") {
		t.Errorf("stderr should carry the shadowed-draft warning, got:\n%s", se)
	}
	if strings.Contains(so, "drafted —") {
		t.Errorf("the warning must not leak into stdout:\n%s", so)
	}

	// An explicit --draft is a DELIBERATE draft: no warning.
	if _, se := mustSplit(t, "add", "--draft", "meant to be a draft"); strings.Contains(se, "drafted —") {
		t.Errorf("--draft must not warn, stderr:\n%s", se)
	}

	// An explicit -r attaches a repo — nothing drafted, nothing to warn about.
	if _, se := mustSplit(t, "add", "-r", "me/projects", "scoped by hand"); strings.Contains(se, "drafted —") {
		t.Errorf("-r must not warn, stderr:\n%s", se)
	}
}

// Outside any configured scope, a plain local board drafting is NORMAL
// repo-local behavior — no warning (the guard must not nag classic boards).
func TestAdd_PlainLocalBoardDoesNotWarn(t *testing.T) {
	t.Setenv(app.EnvBoard, "")
	t.Setenv(app.EnvDir, "")
	testutil.ConfigHome(t) // no [[board]] configured
	dir := t.TempDir()
	if _, err := app.Init(dir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	_, se := mustSplit(t, "add", "classic repo-local task")
	if strings.Contains(se, "drafted —") {
		t.Errorf("classic local board must not warn, stderr:\n%s", se)
	}
}
