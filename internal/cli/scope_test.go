package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// labelCmd builds a throwaway command carrying the shared --label flag, so a
// test can toggle whether the flag was "Changed".
func labelCmd() (*cobra.Command, *string) {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	var label string
	cmd.Flags().StringVarP(&label, "label", "l", "", "")
	return cmd, &label
}

func TestScopedLabel_DefaultAppliesAndAnnounces(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd() // --label NOT changed
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "")
	if got != "chord" {
		t.Errorf("label = %q, want chord", got)
	}
	if !strings.Contains(se.String(), "scope=label=chord") {
		t.Errorf("banner missing scope, stderr = %q", se.String())
	}
}

func TestScopedLabel_ExplicitEmptyEscapesNoBanner(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	_ = cmd.Flags().Set("label", "") // Changed=true, value ""
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty (whole board)", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_ExplicitOtherWins(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	_ = cmd.Flags().Set("label", "other")
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "other")
	if got != "other" {
		t.Errorf("label = %q, want other", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_NoPointerNoBanner(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	got := scopedLabel(cmd, &app.App{DefaultLabel: "", Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

// TestLs_BannerOnStderrNotStdout drives a real `ls` through cobra against a
// pointer layout (discovery uses cwd), asserting the output contract end to end:
// the scope banner lands on stderr while stdout stays pure data.
func TestLs_BannerOnStderrNotStdout(t *testing.T) {
	t.Setenv(app.EnvDir, "") // do not let FURROW_DIR override pointer discovery
	root := t.TempDir()
	central := filepath.Join(root, "central")
	if _, err := app.Init(central); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_label = \"demo\"\n"
	if err := os.WriteFile(filepath.Join(repo, app.PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed one task via the pointer (Open takes an explicit start dir, no chdir).
	a, err := app.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("hello from repo", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}

	// `ls` resolves the store from cwd, so run it from the repo dir.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	var so, se bytes.Buffer
	out, errOut = &so, &se
	defer func() { out, errOut = os.Stdout, os.Stderr }()
	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{"ls"})
	rootCmd.SetOut(&so)
	rootCmd.SetErr(&se)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls: %v", err)
	}

	if strings.Contains(so.String(), "furrow:") {
		t.Errorf("scope banner leaked into stdout:\n%s", so.String())
	}
	if !strings.Contains(se.String(), "scope=label=demo") {
		t.Errorf("scope banner missing from stderr:\n%s", se.String())
	}
	if !strings.Contains(so.String(), "hello from repo") {
		t.Errorf("scoped task missing from stdout:\n%s", so.String())
	}
}
