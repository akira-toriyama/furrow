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

// With auto_filter on (a pointer, or a board with auto_filter=true) and no
// explicit --label, scopedLabel scopes reads to the default label — silently.
// PR2 turns the old scope banner OFF (auto_filter is now an explicit,
// discoverable config field), so nothing is written to stderr.
func TestScopedLabel_AutoFilterAppliesSilently(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd() // --label NOT changed
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "")
	if got != "chord" {
		t.Errorf("label = %q, want chord", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner (PR2 turns it off), stderr = %q", se.String())
	}
}

// auto_filter = false: reads are NOT scoped by the board label (the whole board
// shows), even though DefaultLabel is set — the label still tags `add`, but read
// filtering is off.
func TestScopedLabel_AutoFilterFalseDoesNotScope(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd() // --label NOT changed
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", AutoFilter: false, Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty (auto_filter=false shows the whole board)", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_ExplicitEmptyEscapes(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	_ = cmd.Flags().Set("label", "") // Changed=true, value ""
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "")
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
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "other")
	if got != "other" {
		t.Errorf("label = %q, want other", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_NoDefaultLabel(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	got := scopedLabel(cmd, &app.App{DefaultLabel: "", AutoFilter: true, Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

// TestLs_PointerScopesSilently drives a real `ls` through cobra against a pointer
// layout. It asserts the PR2 contract end to end: the pointer still scopes reads
// to its label (a differently-labeled task on the same board is filtered out),
// stdout stays pure data, and NO scope banner appears on stdout or stderr.
func TestLs_PointerScopesSilently(t *testing.T) {
	t.Setenv(app.EnvDir, "") // do not let FURROW_DIR override pointer discovery
	root := t.TempDir()
	central := filepath.Join(root, "central")
	cboard, err := app.Init(central)
	if err != nil {
		t.Fatal(err)
	}
	// A task on the board NOT carrying the pointer label must be scoped out.
	if _, err := cboard.Add("only on board", app.AddOpts{Labels: []string{"other"}}); err != nil {
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
	// Seed one task via the pointer (Open takes an explicit start dir, no chdir);
	// withDefaultLabel tags it "demo".
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
	if strings.Contains(se.String(), "scope=label=") {
		t.Errorf("scope banner must be OFF in PR2, stderr:\n%s", se.String())
	}
	if !strings.Contains(so.String(), "hello from repo") {
		t.Errorf("scoped task missing from stdout:\n%s", so.String())
	}
	if strings.Contains(so.String(), "only on board") {
		t.Errorf("pointer scope must filter out the differently-labeled task:\n%s", so.String())
	}
}
