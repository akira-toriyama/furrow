package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
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
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	fdir := filepath.Join(cfgDir, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[[board]]\npath = \"" + filepath.Join(central, app.DirName) + "\"\nscopes = [\"" + scope + "\"]\nrepo = \"me/projects\"\n"
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(central); err != nil { // inside the board's own tree
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// A bare add inside the board's own tree drafts — and now says so on stderr at
// the moment it happens (doctor's scope-shadowed finding, raised at the bite).
func TestAdd_ShadowedScopeWarnsOnDraft(t *testing.T) {
	shadowedBoardLayout(t)

	var so, se bytes.Buffer
	out, errOut = &so, &se
	defer func() { out, errOut = os.Stdout, os.Stderr }()
	root := newRootCmd()
	root.SetArgs([]string{"add", "bare add here"})
	root.SetOut(&so)
	root.SetErr(&se)
	if err := root.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(se.String(), "drafted") || !strings.Contains(se.String(), "scope-shadowed") {
		t.Errorf("stderr should carry the shadowed-draft warning, got:\n%s", se.String())
	}
	if strings.Contains(so.String(), "drafted —") {
		t.Errorf("the warning must not leak into stdout:\n%s", so.String())
	}

	// An explicit --draft is a DELIBERATE draft: no warning.
	se.Reset()
	root = newRootCmd()
	root.SetArgs([]string{"add", "--draft", "meant to be a draft"})
	root.SetOut(&so)
	root.SetErr(&se)
	if err := root.Execute(); err != nil {
		t.Fatalf("add --draft: %v", err)
	}
	if strings.Contains(se.String(), "drafted —") {
		t.Errorf("--draft must not warn, stderr:\n%s", se.String())
	}

	// An explicit -r attaches a repo — nothing drafted, nothing to warn about.
	se.Reset()
	root = newRootCmd()
	root.SetArgs([]string{"add", "-r", "me/projects", "scoped by hand"})
	root.SetOut(&so)
	root.SetErr(&se)
	if err := root.Execute(); err != nil {
		t.Fatalf("add -r: %v", err)
	}
	if strings.Contains(se.String(), "drafted —") {
		t.Errorf("-r must not warn, stderr:\n%s", se.String())
	}
}

// Outside any configured scope, a plain local board drafting is NORMAL
// repo-local behavior — no warning (the guard must not nag classic boards).
func TestAdd_PlainLocalBoardDoesNotWarn(t *testing.T) {
	t.Setenv(app.EnvBoard, "")
	t.Setenv(app.EnvDir, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no [[board]] configured
	dir := t.TempDir()
	if _, err := app.Init(dir); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	var so, se bytes.Buffer
	out, errOut = &so, &se
	defer func() { out, errOut = os.Stdout, os.Stderr }()
	root := newRootCmd()
	root.SetArgs([]string{"add", "classic repo-local task"})
	root.SetOut(&so)
	root.SetErr(&se)
	if err := root.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.Contains(se.String(), "drafted —") {
		t.Errorf("classic local board must not warn, stderr:\n%s", se.String())
	}
}
