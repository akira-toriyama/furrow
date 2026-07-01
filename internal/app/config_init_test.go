package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
)

func TestGlobalConfigPath_HonorsXDG(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	p, err := GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfgHome, "furrow", "config.toml"); p != want {
		t.Errorf("GlobalConfigPath() = %q, want %q", p, want)
	}
}

// With no nearby .furrow and no flags, init writes the placeholder template
// verbatim (the bytes mirrored at repo-root config.global.toml).
func TestInitGlobalConfig_PlaceholderWhenNoContext(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	start := t.TempDir() // a bare dir with no .furrow above it

	path, derived, err := InitGlobalConfig(start, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if derived {
		t.Errorf("derived should be false with no context")
	}
	if want := filepath.Join(cfgHome, "furrow", "config.toml"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != config.GlobalTemplate {
		t.Errorf("placeholder init must write GlobalTemplate verbatim; got:\n%s", got)
	}
}

// Run inside a board, init fills the board path (nearest .furrow) and scope (that
// board repo's parent) into the written config.
func TestInitGlobalConfig_DerivesFromNearestFurrow(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "org", "proj")
	if _, err := Init(repo); err != nil {
		t.Fatal(err)
	}
	board := filepath.Join(repo, DirName)

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)

	path, derived, err := InitGlobalConfig(repo, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !derived {
		t.Errorf("derived should be true when a .furrow encloses the start dir")
	}
	boards, warn, err := config.LoadGlobalBoards(path)
	if err != nil {
		t.Fatalf("written config did not parse: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("derived config should not warn; got %v", warn)
	}
	if len(boards) != 1 {
		t.Fatalf("want 1 board, got %d (%v)", len(boards), boards)
	}
	if boards[0].Path != board {
		t.Errorf("board path = %q, want %q", boards[0].Path, board)
	}
	if wantScope := filepath.Join(root, "org"); len(boards[0].Scopes) != 1 || boards[0].Scopes[0] != wantScope {
		t.Errorf("scopes = %v, want [%s]", boards[0].Scopes, wantScope)
	}
}

// --path / --scope win over context derivation.
func TestInitGlobalConfig_FlagsOverrideDerivation(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "org", "proj")
	if _, err := Init(repo); err != nil {
		t.Fatal(err)
	}
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)

	path, _, err := InitGlobalConfig(repo, "/explicit/.furrow", []string{"/explicit/scope"})
	if err != nil {
		t.Fatal(err)
	}
	boards, _, err := config.LoadGlobalBoards(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) != 1 || boards[0].Path != "/explicit/.furrow" {
		t.Errorf("flag --path ignored; got %v", boards)
	}
	if len(boards[0].Scopes) != 1 || boards[0].Scopes[0] != "/explicit/scope" {
		t.Errorf("flag --scope ignored; got %v", boards[0].Scopes)
	}
}

// init never clobbers an existing home config.
func TestInitGlobalConfig_RefusesWhenExists(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InitGlobalConfig(t.TempDir(), "", nil); err == nil {
		t.Fatal("InitGlobalConfig should refuse to overwrite an existing config")
	}
}

// A broken symlink at the config path must still count as "exists": os.Stat
// would miss it (and os.WriteFile would silently write THROUGH it to the
// symlink's target), so init must refuse via a stat that sees the link itself.
func TestInitGlobalConfig_RefusesWhenBrokenSymlinkExists(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(fdir, "config.toml")
	if err := os.Symlink(filepath.Join(cfgHome, "no-such-target"), cfgPath); err != nil {
		t.Fatal(err)
	}
	_, _, err := InitGlobalConfig(t.TempDir(), "", nil)
	if err == nil {
		t.Fatal("init should refuse a broken symlink at the config path, not write through it")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("should report 'already exists'; got: %v", err)
	}
}

// A relative (or ~) --path can't yield a meaningful scope via dir-of-dir, so the
// derivation must be skipped and the placeholder scope kept for the user to fill,
// rather than writing a garbage scope like "." into the config.
func TestInitGlobalConfig_RelativePathKeepsPlaceholderScope(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	path, _, err := InitGlobalConfig(t.TempDir(), "rel/.furrow", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"rel/.furrow"`) {
		t.Errorf("the relative board path should still be substituted; got:\n%s", s)
	}
	if !strings.Contains(s, `["/path/to/the/tree/it/backs"]`) {
		t.Errorf("a relative --path with no --scope must keep the placeholder scope; got:\n%s", s)
	}
}

// A half-written home config (a scope-less board) surfaces a clamp warning —
// the warning discovery drops on its inert path.
func TestGlobalConfigWarnings_FlagsClampedHomeConfig(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte("[[board]]\npath = \"/x/.furrow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warns := GlobalConfigWarnings()
	if len(warns) == 0 {
		t.Fatal("expected a clamp warning for a scope-less board")
	}
	if !strings.Contains(strings.Join(warns, "\n"), "no scopes") {
		t.Errorf("warning should mention missing scopes; got %v", warns)
	}
}

func TestGlobalConfigWarnings_QuietWhenNoConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if w := GlobalConfigWarnings(); len(w) != 0 {
		t.Errorf("expected no warnings when there is no home config; got %v", w)
	}
}

// lint is the one place that reports everything off, so it also surfaces a
// half-written home config — the clamp warning discovery drops on its inert path.
func TestLint_SurfacesGlobalConfigWarnings(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte("[[board]]\npath = \"/x/.furrow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newApp()
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Severity == core.SevWarn && strings.Contains(p.Msg, "no scopes") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should surface the home-config clamp warning; got %+v", ps)
	}
}
