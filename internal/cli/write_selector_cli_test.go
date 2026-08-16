package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// addTitled creates a task and returns its id.
func addTitled(t *testing.T, args ...string) string {
	t.Helper()
	out, code := run(t, append([]string{"add", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("add %v: exit %d\n%s", args, code, out)
	}
	var task core.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("add --json: %v\n%s", err, out)
	}
	return task.ID
}

// A -q selection on done PREVIEWS until --yes (nothing written, dry_run in
// JSON), then applies as one batch — the write-side typed query (t-zce2).
func TestDoneSelectorPreviewsThenApplies(t *testing.T) {
	initStore(t)
	bug1 := addTitled(t, "first bug", "-l", "bug")
	bug2 := addTitled(t, "second bug", "-l", "bug")
	other := addTitled(t, "unrelated")

	// Preview: names both matches, closes nothing.
	out, code := run(t, "done", "-q", "label:bug")
	if code != 0 || !strings.Contains(out, "would close 2 task(s)") ||
		!strings.Contains(out, bug1) || !strings.Contains(out, bug2) ||
		!strings.Contains(out, "re-run with --yes") {
		t.Fatalf("preview = %q (exit %d), want both ids + the --yes hint", out, code)
	}
	if out, _ := run(t, "ls", "-s", "done", "--json"); !strings.Contains(out, "[]") {
		t.Fatalf("preview must close nothing, done lane = %s", out)
	}

	// JSON preview is the archive shape: {dry_run: true, tasks}.
	out, _ = run(t, "done", "-q", "label:bug", "--json")
	var preview struct {
		DryRun bool        `json:"dry_run"`
		Tasks  []core.Task `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil || !preview.DryRun || len(preview.Tasks) != 2 {
		t.Fatalf("json preview = %s (err %v), want dry_run:true with 2 tasks", out, err)
	}

	// Apply: one batch, the usual always-array envelopes.
	out, code = run(t, "done", "-q", "label:bug", "--yes", "--json")
	if code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	var envs []struct {
		After core.Task `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil || len(envs) != 2 {
		t.Fatalf("apply envelopes = %s (err %v), want an array of 2", out, err)
	}
	for _, e := range envs {
		if e.After.Status != "done" {
			t.Errorf("%s not closed: %+v", e.After.ID, e.After)
		}
	}
	if out, _ := run(t, "show", other, "--json"); strings.Contains(out, `"status": "done"`) {
		t.Errorf("unmatched task was closed: %s", out)
	}
}

// move with a selection takes just <lane>, vets it up front (exit 2 +
// candidates on a typo, matched or not), and applies the batch on --yes.
func TestMoveSelectorVetsLaneAndApplies(t *testing.T) {
	initStore(t)
	id := addTitled(t, "inbox task")

	fe, _ := runErr(t, "move", "-q", "status:inbox", "not-a-lane", "--yes")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Fatalf("typo lane = %+v, want exit 2 with the lane vocabulary in candidates", fe)
	}

	if out, code := run(t, "move", "-q", "status:inbox", "backlog"); code != 0 || !strings.Contains(out, "would move to backlog 1 task(s)") {
		t.Fatalf("preview = %q (exit %d)", out, code)
	}
	if out, code := run(t, "move", "-q", "status:inbox", "backlog", "--yes"); code != 0 || !strings.Contains(out, "moved "+id) {
		t.Fatalf("apply = %q (exit %d)", out, code)
	}
	if out, _ := run(t, "show", id, "--json"); !strings.Contains(out, `"status": "backlog"`) {
		t.Errorf("task not moved: %s", out)
	}
}

// set with a selection applies the same edits to every match in one write.
func TestSetSelectorAppliesEdits(t *testing.T) {
	initStore(t)
	a1 := addTitled(t, "one", "-l", "sweep")
	a2 := addTitled(t, "two", "-l", "sweep")

	if out, code := run(t, "set", "-q", "label:sweep", "-s", "backlog", "--add-label", "triaged", "--yes"); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	for _, id := range []string{a1, a2} {
		out, _ := run(t, "show", id, "--json")
		if !strings.Contains(out, `"status": "backlog"`) || !strings.Contains(out, "triaged") {
			t.Errorf("%s missing the edits: %s", id, out)
		}
	}
}

// The selection contract's refusals, shared by the three commands: ids beside
// a selection, --yes without one, --expect-updated or position flags beside
// one, and a selection with nothing to apply.
func TestSelectorGuards(t *testing.T) {
	initStore(t)
	id := addTitled(t, "guard target")

	for name, args := range map[string][]string{
		"ids beside a selection":    {"done", id, "-q", "status:inbox"},
		"--yes without a selection": {"done", id, "--yes"},
		"--expect-updated riding":   {"done", "-q", "status:inbox", "--expect-updated", "2026-01-01T00:00:00Z"},
		"position flag riding":      {"set", "-q", "status:inbox", "-s", "backlog", "--priority", "10"},
		"set with no edit":          {"set", "-q", "status:inbox"},
	} {
		if fe, out := runErr(t, args...); fe == nil || fe.Code != core.CodeValidation {
			t.Errorf("%s: %v / %q, want exit 2", name, fe, out)
		}
	}
	if out, _ := run(t, "show", id, "--json"); strings.Contains(out, `"status": "done"`) {
		t.Errorf("a refused invocation wrote: %s", out)
	}
}

// A selection matching nothing is a valid empty result — exit 0, [] in JSON —
// on both the preview and the apply, like every empty read.
func TestSelectorEmptyMatchIsExitZero(t *testing.T) {
	initStore(t)
	addTitled(t, "bystander")

	if out, code := run(t, "done", "-q", "label:no-such-tag"); code != 0 || !strings.Contains(out, "would close 0 task(s)") {
		t.Fatalf("empty preview = %q (exit %d)", out, code)
	}
	out, code := run(t, "done", "-q", "label:no-such-tag", "--yes", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty apply = %q (exit %d), want [] at exit 0", out, code)
	}
}

// done --note works through a selection too: the closing word lands on every
// matched task's body.
func TestDoneSelectorWithNote(t *testing.T) {
	initStore(t)
	id := addTitled(t, "noted close", "-l", "wave")

	if out, code := run(t, "done", "-q", "label:wave", "--yes", "--note", "swept"); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	if out, _ := run(t, "show", id); !strings.Contains(out, "swept") {
		t.Errorf("closing note missing from body: %s", out)
	}
}
