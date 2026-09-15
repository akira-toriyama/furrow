// Package gittest isolates real-git tests from the developer's ambient git
// configuration. The load-bearing case is the git subprocess that App.Sync
// spawns: internal/gitrepo.runGit inherits os.Environ, so a developer's global
// commit.gpgsign / core.hooksPath / init.templateDir — or a non-"main"
// init.defaultBranch — would otherwise leak into the tests and flake them.
// Isolation therefore happens at the PROCESS-env level, from a package's
// TestMain (before any test runs), not per command.
//
// This is a TEST-ONLY helper: it is imported solely by _test.go files and never
// ends up in a production build.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/internal/claudecode"
)

// isolatedConfig is the throwaway global git config the tests run under. It
// gives them a deterministic default branch (so an empty bare clone lands on
// "main"), a committable identity (so `git commit` never fails for a missing
// user.name/email), gpgsign explicitly off (a developer's global
// commit.gpgsign=true would block unattended commits), and background
// maintenance OFF: by default `git commit`, `push` (the receiving side) and
// `fetch` each spawn `git maintenance run --auto --detach` (measured on git
// 2.54: three detached children per commit+push+fetch), a process that
// outlives the test and writes under .git/ while t.TempDir's RemoveAll runs —
// the shape of ubuntu CI's `TempDir RemoveAll cleanup: … directory not empty`
// flake (t-6jep). Nothing here reads the host machine, so every test sees the
// same git behavior everywhere.
const isolatedConfig = `[init]
	defaultBranch = main
[user]
	name = t
	email = t@e
[commit]
	gpgsign = false
[gc]
	auto = 0
[maintenance]
	auto = false
`

// Isolate points git at a throwaway global config and neutralizes the system
// config, so every git subprocess these tests spawn behaves identically
// regardless of the developer's ~/.gitconfig or /etc/gitconfig. Call it once
// from TestMain, before any test runs: it mutates process env, which is unsafe
// under t.Parallel. The returned cleanup restores the previous env and removes
// the temp config; ignore it only if the process is about to exit anyway.
func Isolate() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "furrow-gitconfig-*")
	if err != nil {
		return nil, fmt.Errorf("gittest: mkdir temp: %w", err)
	}
	path := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(path, []byte(isolatedConfig), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("gittest: write config: %w", err)
	}
	// GIT_CONFIG_GLOBAL swaps our file in for ~/.gitconfig; GIT_CONFIG_SYSTEM
	// pointed at /dev/null plus GIT_CONFIG_NOSYSTEM=1 silence /etc/gitconfig
	// (belt-and-suspenders across git versions).
	restore := setEnv(map[string]string{
		"GIT_CONFIG_GLOBAL":   path,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	})
	return func() {
		restore()
		_ = os.RemoveAll(dir)
	}, nil
}

// setEnv sets each key to its value and returns a func that restores the prior
// state — the previous value where one existed, an unset where it did not.
func setEnv(kv map[string]string) (restore func()) {
	type prev struct {
		val string
		set bool
	}
	saved := make(map[string]prev, len(kv))
	for k, v := range kv {
		old, ok := os.LookupEnv(k)
		saved[k] = prev{old, ok}
		_ = os.Setenv(k, v)
	}
	return func() {
		for k, p := range saved {
			if p.set {
				_ = os.Setenv(k, p.val)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

// GitOrSkip returns the git binary the real-git tests drive, skipping the test
// where git is absent. app and gitrepo carried the same helper (t-8ep8).
func GitOrSkip(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	return git
}

// RunGit runs one git command in dir and returns its combined output, failing
// the test on a non-zero exit with the output attached.
func RunGit(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, args...) //nolint:gosec // test helper: git is GitOrSkip's LookPath result, args are the test's own
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Main is the TestMain every real-git package shares: isolate, run, restore,
// exit. Two packages carried the same twenty lines (t-y6ya).
func Main(m *testing.M) {
	restore, err := Isolate()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}

// MainIsolated is Main for a package whose tests also read the machine's home
// state: it points XDG_CONFIG_HOME at an empty dir (so the developer's real
// ~/.config/furrow/config.toml is a clean no-op unless a test opts in with its
// own t.Setenv), and unsets the Claude Code session env (a developer's `go
// test` usually runs INSIDE a session, whose CLAUDECODE would arm the session
// write guard against the live registry). app and cli carried the same
// twenty-five lines each.
func MainIsolated(m *testing.M) {
	dir, err := os.MkdirTemp("", "furrow-xdg-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	for _, k := range []string{claudecode.EnvActive, claudecode.EnvPID, claudecode.EnvSessionID, claudecode.EnvConfigDir} {
		if err := os.Unsetenv(k); err != nil {
			panic(err)
		}
	}
	restore, err := Isolate()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	restore()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
