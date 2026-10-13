package cli

import (
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A --body-file that does not resolve is the operator's input: exit 2
// validation, as add and edit answer — not exit 3 internal (t-xypx).
func TestApplyMissingBodyFileIsValidation(t *testing.T) {
	initStore(t)
	fe, _, _ := execCLI(t, "", "apply", "--on", "merge", "--body-file", "/nonexistent/pr-body.md")
	if fe == nil || fe.Kind != core.KindValidation || fe.Code != core.CodeValidation {
		t.Fatalf("want validation exit 2, got %+v", fe)
	}
}
