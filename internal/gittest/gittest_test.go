package gittest

import (
	"strings"
	"testing"
)

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
