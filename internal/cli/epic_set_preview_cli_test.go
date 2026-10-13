package cli

import (
	"strings"
	"testing"
)

// The anchor preview names the OTHER edits on the same line, which ride the
// same --yes: a preview that showed only the move hid a --title/--goal that
// was being held back.
func TestEpicSetAnchorPreviewNamesPendingEdits(t *testing.T) {
	epic, _, _, _, _ := anchorBoard(t)
	preview := mustRun(t, "epic", "set", epic, "--anchor", "2026-11-28", "--title", "Renamed", "--goal", "Goal Y")
	if !strings.Contains(preview, "also pending, applied with --yes: --title, --goal") {
		t.Fatalf("preview hides the held-back edits:\n%s", preview)
	}
	stdout, _, code := runSplit(t, "--json", "epic", "set", epic, "--anchor", "2026-11-28", "--title", "Renamed")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, `"pending": [`) || !strings.Contains(stdout, `"title"`) {
		t.Fatalf("JSON preview lacks pending: %s", stdout)
	}
}
