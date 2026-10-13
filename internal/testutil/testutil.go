// Package testutil holds the fixtures more than one package's _test.go files
// share and that are not git's: the user-level config under an isolated
// config home, and the [[board]] table it is built from. Like gittest it is
// imported only by _test.go files, and it imports nothing of furrow's own, so
// every package may use it without a cycle.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ConfigHome points the user-level config at a fresh temp dir for the test's
// lifetime and returns that dir: the "no user config at all" state, and the
// home a `config init` writes into. Every test that reads the user config
// goes through here, so none can read the developer's real one.
func ConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	return home
}

// UserConfig writes body as the user-level furrow config under a fresh
// ConfigHome and returns the file's path.
func UserConfig(t *testing.T, body string) string {
	t.Helper()
	return UserConfigIn(t, ConfigHome(t), body)
}

// UserConfigIn writes body as the user-level furrow config under home, which
// it also makes the config home — for the one test whose scopes are relative
// to the config's own directory.
func UserConfigIn(t *testing.T, home, body string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "furrow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// BoardEntry renders one [[board]] table: path, scopes and repo, every key
// present. A caller wanting more appends its own `key = value` lines, which
// stay inside the table.
func BoardEntry(path, repo string, scopes ...string) string {
	q := make([]string, len(scopes))
	for i, s := range scopes {
		q[i] = "\"" + s + "\""
	}
	return "[[board]]\npath = \"" + path + "\"\nscopes = [" + strings.Join(q, ", ") + "]\nrepo = \"" + repo + "\"\n"
}
