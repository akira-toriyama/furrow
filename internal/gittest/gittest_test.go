package gittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate refuses a temp root inside a git work tree or a furrow board, and a
// relative one: tests take their temp dirs to be outside both, and with TMPDIR
// inside a work tree sync's "outside git" tests committed there and pushed to
// its upstream (furrow t-tayf). The root is caught as the marker's own
// directory or below it, from TMPDIR (also while GOTMPDIR names a safe
// directory) or GOTMPDIR (t.TempDir() roots there when it is set), through a
// symlink from outside, and as a symlink that resolves outside (furrow's board
// walk climbs the path as given, not the resolved one).
func TestIsolateRefusesAnUnsafeTempRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		marker  string // created in the enclosing directory
		file    bool   // the marker is a file, not a directory
		env     string
		at      string // the root, relative to the enclosing directory
		link    bool   // reach the root through a symlink from outside
		lexical bool   // the root is a symlink that resolves to a plain directory outside
		safeGo  bool   // GOTMPDIR names a plain directory outside
	}{
		{name: "inside a git work tree", marker: ".git", env: "TMPDIR", at: "e2e/tmp"},
		{name: "at a work tree's top", marker: ".git", env: "TMPDIR", at: "."},
		{name: "inside a linked worktree", marker: ".git", file: true, env: "TMPDIR", at: "e2e/tmp"},
		{name: "inside a board checkout", marker: ".furrow", env: "TMPDIR", at: "e2e/tmp"},
		{name: "beside a board pointer", marker: ".furrow-pointer.toml", file: true, env: "TMPDIR", at: "e2e/tmp"},
		{name: "GOTMPDIR inside a work tree", marker: ".git", env: "GOTMPDIR", at: "e2e/tmp"},
		{name: "through a symlink", marker: ".git", env: "TMPDIR", at: "e2e/tmp", link: true},
		{name: "a symlink under a board that resolves outside", marker: ".furrow", env: "TMPDIR", at: "l", lexical: true},
		{name: "TMPDIR while GOTMPDIR is safe", marker: ".git", env: "TMPDIR", at: "e2e/tmp", safeGo: true},
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
			if tc.lexical {
				if err := os.Symlink(t.TempDir(), root); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(root, 0o755); err != nil {
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
			if tc.safeGo {
				t.Setenv("GOTMPDIR", t.TempDir())
			}
			t.Setenv(tc.env, root)
			restore, err := isolate()
			if err == nil {
				restore()
				t.Fatalf("isolate accepted %s=%s, which sits inside %s", tc.env, root, outer)
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
		if restore, err := isolate(); err == nil {
			restore()
			t.Fatal("isolate accepted the relative TMPDIR=tmp")
		}
	})

	t.Run("a plain directory is accepted", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "e2e", "tmp")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOTMPDIR", "")
		t.Setenv("TMPDIR", root)
		restore, err := isolate()
		if err != nil {
			t.Fatalf("isolate refused a root outside every repo and board: %v", err)
		}
		restore()
	})
}

// isolate must drop the ambient variables that reach into repositories the
// test did not name, and its cleanup must put them back: FURROW_DIR sends
// app.Open to the developer's board before it looks at the directory a test
// named (sync then committed and pushed there, t-tayf review), FURROW_BOARD
// once the walk finds none; a hook's GIT_DIR / GIT_INDEX_FILE make the tests'
// own setup git calls rewrite the hook's repository; GIT_TEMPLATE_DIR copies
// the developer's hooks into every repo a test inits. The git keys are read
// from git itself, so the pin is on what the installed git honours, not on the
// frozen list alone.
func TestIsolateDropsTheAmbientRepositoryEnv(t *testing.T) {
	git := GitOrSkip(t)
	keys := append([]string{"FURROW_DIR", "FURROW_BOARD", "GIT_TEMPLATE_DIR"}, strings.Fields(RunGit(t, git, t.TempDir(), "rev-parse", "--local-env-vars"))...)
	for _, k := range keys {
		t.Setenv(k, "ambient-"+k)
	}
	restore, err := isolate()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s=%q survived isolate", k, v)
		}
	}
	restore()
	for _, k := range keys {
		if v := os.Getenv(k); v != "ambient-"+k {
			t.Errorf("%s=%q after isolate's cleanup, want the ambient value back", k, v)
		}
	}
}

// isolate must switch git's background maintenance off: with the default
// config every commit/push/fetch spawns a detached `git maintenance run
// --auto` that can still be writing under .git/ when t.TempDir's RemoveAll
// runs (ubuntu CI's "directory not empty" flake, t-6jep). Read the two keys
// back through git itself so the pin is on what git sees, not on the file.
func TestIsolateDisablesBackgroundMaintenance(t *testing.T) {
	restore, err := isolate()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	git := GitOrSkip(t)
	for key, want := range map[string]string{"gc.auto": "0", "maintenance.auto": "false"} {
		if got := strings.TrimSpace(RunGit(t, git, t.TempDir(), "config", "--global", "--get", key)); got != want {
			t.Errorf("%s = %q under isolate, want %q", key, got, want)
		}
	}
}
