package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate must stop repository discovery at the temp root. Tests take
// t.TempDir() to be outside every repository; with TMPDIR inside a work tree
// sync's "outside git" tests committed to that tree and pushed to its
// upstream (furrow t-tayf). A repo a test makes under the root must still be
// found from its subdirectories.
func TestIsolateStopsRepoDiscoveryAtTheTempRoot(t *testing.T) {
	git := GitOrSkip(t)
	outer := t.TempDir()
	root := filepath.Join(outer, "e2e", "tmp")
	own := filepath.Join(root, "own")
	sub := filepath.Join(own, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	restore, err := Isolate()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	RunGit(t, git, outer, "init", "-q")

	cmd := exec.Command(git, "rev-parse", "--show-toplevel") //nolint:gosec // test: git is GitOrSkip's LookPath result
	cmd.Dir = own
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git found %s from under the temp root %s; discovery must stop there", strings.TrimSpace(string(out)), root)
	}

	RunGit(t, git, own, "init", "-q")
	want, err := filepath.EvalSymlinks(own)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(RunGit(t, git, sub, "rev-parse", "--show-toplevel")); got != want {
		t.Errorf("a repo under the temp root: toplevel = %q from its subdirectory, want %q", got, want)
	}
}

// Isolate must switch git's background maintenance off: with the default
// config every commit/push/fetch spawns a detached `git maintenance run
// --auto` that can still be writing under .git/ when t.TempDir's RemoveAll
// runs (ubuntu CI's "directory not empty" flake, t-6jep). Read the two keys
// back through git itself so the pin is on what git sees, not on the file.
func TestIsolateDisablesBackgroundMaintenance(t *testing.T) {
	restore, err := Isolate()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	git := GitOrSkip(t)
	for key, want := range map[string]string{"gc.auto": "0", "maintenance.auto": "false"} {
		if got := strings.TrimSpace(RunGit(t, git, t.TempDir(), "config", "--global", "--get", key)); got != want {
			t.Errorf("%s = %q under Isolate, want %q", key, got, want)
		}
	}
}
