package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// t-79nc, whole stack: `furrow migrate` predates the epic pivot, so on a board
// that HAS boxes an import used to exit 0 and then turn `furrow lint` red with
// one epic-required error per imported open task — with not one line about it
// in migrate's own warnings, the very thing its LOUD contract exists for.
//
// The fix is the `--label` shape carried to boxes: `-e` files every imported
// task, a bare import inherits the scope's single active box exactly as `add`
// does, and the outcome "open tasks under no box on a board that has boxes" is
// a warning in BOTH arms rather than a lint error after the write.

// migrateSrc writes the shared fixture: two Open tasks (a `ready` lane) plus a
// Done archive item. The Done one is load-bearing — epic-required exempts
// terminal lanes, so a warning counting all 3 would over-report.
func migrateSrc(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "Task.md")
	md := "# Demo\n\n## 🎯 Open\n\n### 1. First task\nDetail one.\n\n" +
		"### 2. Second task\nDetail two.\n\n## ✔ Done\n\n- **Old finished thing** shipped last week\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// migrateBox declares a box (with a repo, which `epic activate` requires) and
// returns its id.
func migrateBox(t *testing.T, title string) string {
	t.Helper()
	out, code := run(t, "--json", "epic", "add", title, "--repo", "o/r")
	if code != 0 {
		t.Fatalf("epic add exit %d:\n%s", code, out)
	}
	var box struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &box); err != nil {
		t.Fatalf("parse epic add --json: %v\n%s", err, out)
	}
	return box.ID
}

// migrateJSON runs migrate with --json and returns the epic + warnings it
// reports (the same two keys in both the dry-run and the apply payload).
func migrateJSON(t *testing.T, args ...string) (string, []string) {
	t.Helper()
	out, code := run(t, append([]string{"--json", "migrate"}, args...)...)
	if code != 0 {
		t.Fatalf("migrate %v exit %d:\n%s", args, code, out)
	}
	var got struct {
		Epic     string   `json:"epic"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse migrate --json: %v\n%s", err, out)
	}
	return got.Epic, got.Warnings
}

func hasUnfiledWarning(warnings []string) bool {
	for _, w := range warnings {
		if strings.Contains(w, "epic-required") {
			return true
		}
	}
	return false
}

// TestCLIMigrateFilesUnderEpic is the repro from the task, closed: on a board
// with a box, `migrate -e <ref> --yes` files every imported task and lint stays
// green. The ref is a title substring, so it also pins that migrate resolves
// through the normal epic-ref contract rather than storing the raw reference.
func TestCLIMigrateFilesUnderEpic(t *testing.T) {
	initStore(t)
	box := migrateBox(t, "current work")
	src := migrateSrc(t)

	// The dry-run reports the resolved id, so the preview states what --yes does.
	epic, warnings := migrateJSON(t, src, "-e", "current work")
	if epic != box {
		t.Errorf("dry-run epic = %q, want the resolved %s", epic, box)
	}
	if hasUnfiledWarning(warnings) {
		t.Errorf("a filed import must not warn about epic-required: %v", warnings)
	}

	epic, warnings = migrateJSON(t, src, "-e", "current work", "--yes")
	if epic != box {
		t.Errorf("apply epic = %q, want %s", epic, box)
	}
	if hasUnfiledWarning(warnings) {
		t.Errorf("a filed import must not warn about epic-required: %v", warnings)
	}

	// Every imported task carries the box...
	out, code := run(t, "--json", "ls", "-s", "ready,done")
	if code != 0 {
		t.Fatalf("ls exit %d:\n%s", code, out)
	}
	var tasks []struct {
		Title string `json:"title"`
		Epic  string `json:"epic"`
	}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("parse ls --json: %v\n%s", err, out)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 imported tasks, got %d:\n%s", len(tasks), out)
	}
	for _, task := range tasks {
		if task.Epic != box {
			t.Errorf("task %q epic = %q, want %s", task.Title, task.Epic, box)
		}
	}
	// ...which is the whole point: lint is green immediately after the import.
	if lout, code := run(t, "lint"); code != 0 {
		t.Errorf("lint after a filed migrate must be green, exit %d:\n%s", code, lout)
	}
}

// TestCLIMigrateWarnsUnfiledImport pins the LOUD half: with a box on the board
// and no -e, the import still succeeds (migrate never blocks a bootstrap) but
// says so — in the dry-run and in the apply, human and JSON alike — and the
// count excludes the terminal-lane task, so it equals what lint then reports.
func TestCLIMigrateWarnsUnfiledImport(t *testing.T) {
	initStore(t)
	migrateBox(t, "current work")
	src := migrateSrc(t)

	// 2 of 3: the Done archive item is terminal, and epic-required exempts it.
	const want = "2 of 3 imported task(s) land in an open lane with no epic"

	epic, warnings := migrateJSON(t, src)
	if epic != "" {
		t.Errorf("dry-run epic = %q, want unfiled", epic)
	}
	if !hasUnfiledWarning(warnings) {
		t.Errorf("the dry-run must warn about the unfiled import: %v", warnings)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), want) {
		t.Errorf("warning must count only the open tasks (%q): %v", want, warnings)
	}

	out, code := run(t, "migrate", src)
	if code != 0 {
		t.Fatalf("migrate dry-run exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("human dry-run is missing the warning:\n%s", out)
	}

	// The apply says the same thing, and exits 0 — the import is not blocked.
	out, code = run(t, "migrate", src, "--yes")
	if code != 0 {
		t.Fatalf("migrate --yes exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("human apply is missing the warning:\n%s", out)
	}
	// The warning's claim, measured: lint reports exactly those 2.
	lout, code := run(t, "lint", "--code", "epic-required", "--json")
	if code == 0 {
		t.Errorf("lint must be red after an unfiled import on a board with boxes:\n%s", lout)
	}
	if n := strings.Count(lout, `"code": "epic-required"`); n != 2 {
		t.Errorf("lint reported %d epic-required problems, warning promised 2:\n%s", n, lout)
	}
}

// TestCLIMigrateQuietWithoutBoxes is the other side of lint's own gate: a board
// that never declared a box has not adopted membership, so an unfiled import is
// not a defect and must not be nagged about.
func TestCLIMigrateQuietWithoutBoxes(t *testing.T) {
	initStore(t)
	src := migrateSrc(t)

	if _, warnings := migrateJSON(t, src); hasUnfiledWarning(warnings) {
		t.Errorf("a board with no boxes must not warn: %v", warnings)
	}
	if _, warnings := migrateJSON(t, src, "--yes"); hasUnfiledWarning(warnings) {
		t.Errorf("a board with no boxes must not warn: %v", warnings)
	}
	if out, code := run(t, "lint"); code != 0 {
		t.Errorf("lint must stay green there, exit %d:\n%s", code, out)
	}
}

// TestCLIMigrateInheritsActiveEpic carries `add`'s inheritance to the batch: a
// bare import during a declared focus files under it, disclosed on stderr, and
// `-e ”` opts out on purpose (which is then an unfiled import, and warned
// about as one — the warning states the OUTCOME, not a mistake).
func TestCLIMigrateInheritsActiveEpic(t *testing.T) {
	initStore(t)
	box := migrateBox(t, "focus box")
	if _, code := run(t, "epic", "activate", box); code != 0 {
		t.Fatalf("epic activate exit %d", code)
	}
	src := migrateSrc(t)

	if epic, warnings := migrateJSON(t, src); epic != box || hasUnfiledWarning(warnings) {
		t.Errorf("dry-run epic = %q (want %s), warnings %v", epic, box, warnings)
	}

	stdout, stderr, code := runSplit(t, "migrate", src, "--yes")
	if code != 0 {
		t.Fatalf("migrate --yes exit %d:\n%s", code, stdout)
	}
	if !strings.Contains(stderr, "filed under active epic "+box) {
		t.Errorf("the inheritance must be disclosed on stderr, got: %q", stderr)
	}
	if out, code := run(t, "lint"); code != 0 {
		t.Errorf("an inherited import must leave lint green, exit %d:\n%s", code, out)
	}

	// -e '' opts out of the inheritance for the whole batch.
	epic, warnings := migrateJSON(t, src, "-e", "", "--yes")
	if epic != "" {
		t.Errorf("-e '' must import unfiled, got epic %q", epic)
	}
	if !hasUnfiledWarning(warnings) {
		t.Errorf("an unfiled outcome is warned about however it was chosen: %v", warnings)
	}
}

// TestCLIMigrateRejectsUnknownEpic pins that the ref is resolved BEFORE the
// write, in both arms: the dry-run must not preview a plan `--yes` would then
// refuse, and the apply must fail before the first body hits disk.
func TestCLIMigrateRejectsUnknownEpic(t *testing.T) {
	initStore(t)
	migrateBox(t, "current work")
	src := migrateSrc(t)

	for _, args := range [][]string{
		{"migrate", src, "-e", "no-such-box"},
		{"migrate", src, "-e", "no-such-box", "--yes"},
	} {
		// The KIND, not just the exit code: a binary with no -e flag at all
		// also exits 2 (cobra's "unknown shorthand flag"), so asserting the
		// code alone would leave this the one test in the file that cannot
		// tell the feature from its absence.
		fe, out := runErr(t, args...)
		if exitOf(fe) != 2 {
			t.Errorf("%v exit = %d, want 2:\n%s", args, exitOf(fe), out)
			continue
		}
		if fe.Kind != core.KindEpicNotFound {
			t.Errorf("%v kind = %q, want %q (the ref must fail epic RESOLUTION, not flag parsing)",
				args, fe.Kind, core.KindEpicNotFound)
		}
		if len(fe.Candidates) == 0 {
			t.Errorf("%v must carry the board's boxes in candidates (the did-you-mean guard)", args)
		}
	}
	out, code := run(t, "--json", "ls")
	if code != 0 {
		t.Fatalf("ls exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "First task") {
		t.Errorf("a refused migrate must write nothing:\n%s", out)
	}
}
