package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/config"
)

// runConfigCLI drives the real root command and captures stdout/stderr through
// the package output funnels (out/errOut), restoring them afterwards.
func runConfigCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&so)
	root.SetErr(&se)
	err := root.Execute()
	return so.String(), se.String(), err
}

func TestConfigPath_PrintsResolvedPath(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	so, _, err := runConfigCLI(t, "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfgHome, "furrow", "config.toml"); strings.TrimSpace(so) != want {
		t.Errorf("stdout = %q, want %q", strings.TrimSpace(so), want)
	}
}

func TestConfigPath_JSON(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	so, _, err := runConfigCLI(t, "--json", "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfgHome, "furrow", "config.toml")
	if !strings.Contains(so, `"path"`) || !strings.Contains(so, want) {
		t.Errorf("json path output missing; got %q", so)
	}
}

// A half-written home config surfaces its clamp warning on stderr while stdout
// stays the clean path (so `furrow config path` still pipes cleanly).
func TestConfigPath_SurfacesClampWarningOnStderr(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	fdir := filepath.Join(cfgHome, "furrow")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte("[[board]]\npath = \"/x/.furrow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	so, se, err := runConfigCLI(t, "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(se, "no scopes") {
		t.Errorf("clamp warning should be on stderr; got stderr=%q", se)
	}
	if strings.Contains(so, "no scopes") {
		t.Errorf("warning leaked into stdout: %q", so)
	}
	if want := filepath.Join(cfgHome, "furrow", "config.toml"); strings.TrimSpace(so) != want {
		t.Errorf("stdout should be the path only; got %q", so)
	}
}

func TestConfigInit_WritesPlaceholderTemplate(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)

	start := t.TempDir() // no enclosing .furrow -> placeholder
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(start); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	so, _, err := runConfigCLI(t, "config", "init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(so, "wrote") {
		t.Errorf("init should confirm it wrote the file; got %q", so)
	}
	got, err := os.ReadFile(filepath.Join(cfgHome, "furrow", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != config.GlobalTemplate {
		t.Errorf("placeholder init must write GlobalTemplate verbatim; got:\n%s", got)
	}
}

func TestConfig_BareListsSubcommands(t *testing.T) {
	so, se, _ := runConfigCLI(t, "config")
	combined := so + se
	if !strings.Contains(combined, "init") || !strings.Contains(combined, "path") {
		t.Errorf("`furrow config` should list its subcommands; got stdout=%q stderr=%q", so, se)
	}
}
