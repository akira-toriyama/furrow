package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
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

// A PR description with no text names no directive: apply answers a file
// holding one with the empty report it gives the same text on stdin, not the
// refusal add and edit give an empty body. sync-task-status.yml hands apply
// the PR body as a file, where that refusal is a red job for a PR opened
// without a description (t-264j). The file leg puts a directive on stdin, so
// an apply that read stdin instead of the file reports an outcome.
func TestApplyEmptyBodyFileIsAnEmptyReport(t *testing.T) {
	initStore(t)
	for name, text := range map[string]string{"empty": "", "blank": " \n\n"} {
		path := filepath.Join(t.TempDir(), name+".md")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, in := range []struct {
			via   string
			stdin string
			arg   string
		}{{"file", "SetStatus-task: t-nosuchtask done\n", path}, {"stdin", text, "-"}} {
			fe, stdout, _ := execCLI(t, in.stdin, "--json", "apply", "--on", "open", "--ref", "x#1", "--body-file", in.arg)
			if fe != nil {
				t.Fatalf("%s via %s: want exit 0, got %+v", name, in.via, fe)
			}
			var res struct {
				Outcomes []json.RawMessage `json:"outcomes"`
			}
			if err := json.Unmarshal([]byte(stdout), &res); err != nil {
				t.Fatalf("%s via %s: report is not JSON: %v\n%s", name, in.via, err, stdout)
			}
			if res.Outcomes == nil || len(res.Outcomes) != 0 {
				t.Errorf("%s via %s: want `outcomes: []`, got %s", name, in.via, stdout)
			}
		}
	}
}
