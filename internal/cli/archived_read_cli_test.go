package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// archiveOne creates a task, moves it to done, and archives it by id; returns
// the id (now living only in .furrow/archive/).
func archiveOne(t *testing.T, title string) string {
	t.Helper()
	id := addTask(t, title)
	if _, code := run(t, "done", id); code != 0 {
		t.Fatalf("done failed for %s", id)
	}
	if _, code := run(t, "archive", id, "--yes"); code != 0 {
		t.Fatalf("archive failed for %s", id)
	}
	return id
}

func TestShowArchivedReadsRetiredTask(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired task")

	out, code := run(t, "--json", "show", id, "--archived")
	if code != 0 {
		t.Fatalf("show --archived should find the task, exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, id) || !strings.Contains(out, "retired task") {
		t.Errorf("show --archived should return the archived task:\n%s", out)
	}
}

func TestShowPlainMissHintsArchived(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired task")

	fe, _ := runErr(t, "--json", "show", id)
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("a plain show of an archived id should miss, got %+v", fe)
	}
	d, ok := fe.Details.(map[string][]string)
	if !ok {
		t.Fatalf("details should be map[string][]string, got %T", fe.Details)
	}
	if arc := d["archived"]; len(arc) != 1 || arc[0] != id {
		t.Errorf("a miss on an archived id should list it in details.archived, got %+v", d)
	}
	if !strings.Contains(fe.Msg, "archived") {
		t.Errorf("the message should hint --archived, got %q", fe.Msg)
	}
}

func TestShowGenuineMissHasNoArchivedKey(t *testing.T) {
	initStore(t)
	addTask(t, "live one")

	fe, _ := runErr(t, "--json", "show", "t-zzzz9")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("unknown id should miss, got %+v", fe)
	}
	d, ok := fe.Details.(map[string][]string)
	if !ok {
		t.Fatalf("details should be map[string][]string, got %T", fe.Details)
	}
	if _, hasArchived := d["archived"]; hasArchived {
		t.Errorf("a genuine miss must NOT carry an archived key, got %+v", d)
	}
	if fe.Msg != "task not found: t-zzzz9" {
		t.Errorf("a genuine miss keeps the classic message, got %q", fe.Msg)
	}
}

func TestLsArchivedListsRetired(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired task")
	live := addTask(t, "live task")

	// hot ls does not show the archived task...
	out, code := run(t, "--json", "ls", "-r", "")
	if code != 0 {
		t.Fatalf("ls exit=%d:\n%s", code, out)
	}
	if strings.Contains(out, id) {
		t.Errorf("hot ls must not include the archived task:\n%s", out)
	}
	if !strings.Contains(out, live) {
		t.Errorf("hot ls should include the live task:\n%s", out)
	}

	// ...but ls --archived does (and not the live one).
	out, code = run(t, "--json", "ls", "--archived", "-r", "")
	if code != 0 {
		t.Fatalf("ls --archived exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("ls --archived should include the archived task:\n%s", out)
	}
	if strings.Contains(out, live) {
		t.Errorf("ls --archived must not include the live task:\n%s", out)
	}
}

func TestLsArchivedEmptyWhenNothingArchived(t *testing.T) {
	initStore(t)
	addTask(t, "live only")

	out, code := run(t, "--json", "ls", "--archived", "-r", "")
	if code != 0 {
		t.Fatalf("ls --archived on a never-archived board should exit 0, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("nothing archived should be [], got:\n%s", out)
	}
}

func TestShowArchivedRejectsBacklinks(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired task")

	// --archived and --backlinks are mutually exclusive (backlinks scans the hot
	// bodies, which can't reference an archived id in the hot index).
	_, code := run(t, "show", id, "--archived", "--backlinks")
	if code != int(core.CodeValidation) {
		t.Fatalf("--archived with --backlinks should exit 2, got %d", code)
	}
}
