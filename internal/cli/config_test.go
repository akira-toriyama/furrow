package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/config"
)

func TestConfigPath_PrintsResolvedPath(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	so := mustRun(t, "config", "path")
	if want := filepath.Join(cfgHome, "furrow", "config.toml"); strings.TrimSpace(so) != want {
		t.Errorf("stdout = %q, want %q", strings.TrimSpace(so), want)
	}
}

func TestConfigPath_JSON(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	so := mustRun(t, "--json", "config", "path")
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
	so, se := mustSplit(t, "config", "path")
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

	so := mustRun(t, "config", "init")
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
	so, se, _ := runSplit(t, "config")
	combined := so + se
	if !strings.Contains(combined, "init") || !strings.Contains(combined, "path") {
		t.Errorf("`furrow config` should list its subcommands; got stdout=%q stderr=%q", so, se)
	}
}
