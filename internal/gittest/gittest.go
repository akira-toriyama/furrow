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
	"path/filepath"
)

// isolatedConfig is the throwaway global git config the tests run under. It
// gives them a deterministic default branch (so an empty bare clone lands on
// "main"), a committable identity (so `git commit` never fails for a missing
// user.name/email), and gpgsign explicitly off (a developer's global
// commit.gpgsign=true would block unattended commits). Nothing here reads the
// host machine, so every test sees the same git behavior everywhere.
const isolatedConfig = `[init]
	defaultBranch = main
[user]
	name = t
	email = t@e
[commit]
	gpgsign = false
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
