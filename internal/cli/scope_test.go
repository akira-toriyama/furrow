package cli

import (
	"bytes"
	"os"
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
