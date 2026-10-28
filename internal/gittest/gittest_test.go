package gittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate refuses a temp root inside a git work tree or a furrow board, and a
// relative one: tests take their temp dirs to be outside both, and with TMPDIR
// inside a work tree sync's "outside git" tests committed there and pushed to
// its upstream (furrow t-tayf). The root is caught as the marker's own
// directory or below it, from TMPDIR or GOTMPDIR (t.TempDir() roots there when
// it is set), and through a symlink from outside.
func TestIsolateRefusesAnUnsafeTempRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string // created in the enclosing directory
		file   bool   // the marker is a file, not a directory
		env    string
		at     string // the root, relative to the enclosing directory
		link   bool   // reach the root through a symlink from outside
	}{
		{name: "inside a git work tree", marker: ".git", env: "TMPDIR", at: "e2e/tmp"},
		{name: "at a work tree's top", marker: ".git", env: "TMPDIR", at: "."},
		{name: "inside a linked worktree", marker: ".git", file: true, env: "TMPDIR", at: "e2e/tmp"},
		{name: "inside a board checkout", marker: ".furrow", env: "TMPDIR", at: "e2e/tmp"},
		{name: "beside a board pointer", marker: ".furrow-pointer.toml", file: true, env: "TMPDIR", at: "e2e/tmp"},
		{name: "GOTMPDIR inside a work tree", marker: ".git", env: "GOTMPDIR", at: "e2e/tmp"},
		{name: "through a symlink", marker: ".git", env: "TMPDIR", at: "e2e/tmp", link: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer := t.TempDir()
			mark := filepath.Join(outer, tc.marker)
			if tc.file {
				if err := os.WriteFile(mark, []byte("x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(mark, 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(outer, tc.at)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.link {
				link := filepath.Join(t.TempDir(), "tmp")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				root = link
			}
			t.Setenv("GOTMPDIR", "")
			t.Setenv(tc.env, root)
			restore, err := Isolate()
			if err == nil {
				restore()
				t.Fatalf("Isolate accepted %s=%s, which sits inside %s", tc.env, root, outer)
			}
			if !strings.Contains(err.Error(), tc.marker) {
				t.Errorf("the refusal %q does not name the %s it found", err, tc.marker)
			}
		})
	}

	t.Run("relative", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := os.Mkdir("tmp", 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOTMPDIR", "")
		t.Setenv("TMPDIR", "tmp")
		if restore, err := Isolate(); err == nil {
			restore()
			t.Fatal("Isolate accepted the relative TMPDIR=tmp")
		}
	})

	t.Run("a plain directory is accepted", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "e2e", "tmp")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOTMPDIR", "")
		t.Setenv("TMPDIR", root)
		restore, err := Isolate()
		if err != nil {
			t.Fatalf("Isolate refused a root outside every repo and board: %v", err)
		}
		restore()
	})
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
