package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// t-mzek, whole stack: on a board whose scope has exactly ONE active epic, a
// bare `furrow add` files the capture under it (disclosed on stderr, stdout
// stays the created task), `-e ”` stays unfiled on purpose, and --stdin
// follows the same rule with ONE note for the batch.
// runSplitStdin is runSplit with a stdin feed — needed because --stdin reads
// titles from the command's input stream.
func runSplitStdin(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var so, se bytes.Buffer
	out, errOut = &so, &se
	t.Cleanup(func() { out, errOut = os.Stdout, os.Stderr })
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&so)
	root.SetErr(&se)
	root.SetIn(strings.NewReader(stdin))
	err := root.Execute()
	if err == nil {
		return so.String(), se.String(), 0
	}
	fe := core.AsError(err)
	if fe == nil {
		return so.String(), se.String(), int(core.CodeValidation)
	}
	return so.String(), se.String(), int(fe.Code)
}

func TestCLIAddInheritsActiveEpic(t *testing.T) {
	initStore(t)
	// A box with a repo (activate requires one), then activate it.
	out, code := run(t, "--json", "epic", "add", "focus box", "--repo", "o/r")
	if code != 0 {
		t.Fatalf("epic add exit %d:\n%s", code, out)
	}
	var box struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &box); err != nil {
		t.Fatalf("parse epic add --json: %v\n%s", err, out)
	}
	if _, code := run(t, "epic", "activate", box.ID); code != 0 {
		t.Fatalf("epic activate exit %d", code)
	}

	stdout, stderr, code := runSplit(t, "--json", "add", "captured mid-focus")
	if code != 0 {
		t.Fatalf("add exit %d:\n%s", code, stdout)
	}
	var task struct {
		ID   string `json:"id"`
		Epic string `json:"epic"`
	}
	if err := json.Unmarshal([]byte(stdout), &task); err != nil {
		t.Fatalf("parse add --json: %v\n%s", err, stdout)
	}
	if task.Epic != box.ID {
		t.Errorf("bare add must inherit %s, got %q", box.ID, task.Epic)
	}
	if !strings.Contains(stderr, "filed under active epic "+box.ID) {
		t.Errorf("the inheritance must be disclosed on stderr, got: %q", stderr)
	}

	stdout, stderr, code = runSplit(t, "--json", "add", "scratch note", "-e", "")
	if code != 0 {
		t.Fatalf("add -e '' exit %d:\n%s", code, stdout)
	}
	task.Epic = "" // epic is omitempty, and Unmarshal leaves absent keys untouched
	if err := json.Unmarshal([]byte(stdout), &task); err != nil {
		t.Fatal(err)
	}
	if task.Epic != "" {
		t.Errorf("-e '' must stay unfiled, got %q", task.Epic)
	}
	if strings.Contains(stderr, "filed under") {
		t.Errorf("nothing inherited, nothing to disclose: %q", stderr)
	}

	so, se, code := runSplitStdin(t, "one\ntwo\n", "--json", "add", "--stdin")
	if code != 0 {
		t.Fatalf("add --stdin exit %d:\n%s", code, so)
	}
	var many []struct {
		Epic string `json:"epic"`
	}
	if err := json.Unmarshal([]byte(so), &many); err != nil {
		t.Fatalf("parse add --stdin --json: %v\n%s", err, so)
	}
	if len(many) != 2 || many[0].Epic != box.ID || many[1].Epic != box.ID {
		t.Errorf("both stdin tasks must inherit %s: %+v", box.ID, many)
	}
	if strings.Count(se, "filed under active epic") != 1 {
		t.Errorf("want exactly one batch note, got: %q", se)
	}
}
