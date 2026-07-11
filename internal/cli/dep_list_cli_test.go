package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

type depListJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	DependsOn []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"depends_on"`
	Blocks []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"blocks"`
}

func TestDepListJSONBothDirections(t *testing.T) {
	initStore(t)
	base := addTask(t, "base task")
	mid := addTask(t, "middle task")
	top := addTask(t, "top task")
	if _, code := run(t, "dep", mid, base); code != 0 { // mid depends on base
		t.Fatalf("dep add failed: %d", code)
	}
	if _, code := run(t, "dep", top, mid); code != 0 { // top depends on mid
		t.Fatalf("dep add failed: %d", code)
	}

	out, code := run(t, "--json", "dep", mid, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	var r depListJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if r.ID != mid {
		t.Errorf("subject id = %q, want %q", r.ID, mid)
	}
	if len(r.DependsOn) != 1 || r.DependsOn[0].ID != base || r.DependsOn[0].Title != "base task" || r.DependsOn[0].Status == "" {
		t.Errorf("depends_on should resolve base with title+status, got %+v", r.DependsOn)
	}
	if len(r.Blocks) != 1 || r.Blocks[0].ID != top || r.Blocks[0].Title != "top task" {
		t.Errorf("blocks should resolve top, got %+v", r.Blocks)
	}
}

func TestDepListJSONEmptyArraysNotNull(t *testing.T) {
	initStore(t)
	lone := addTask(t, "lonely")

	out, code := run(t, "--json", "dep", lone, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	// A lone task's edges must serialize as [], never null.
	if !strings.Contains(out, `"depends_on": []`) || !strings.Contains(out, `"blocks": []`) {
		t.Errorf("empty edges must be [] not null:\n%s", out)
	}
}

func TestDepListNDJSONSingleObjectLine(t *testing.T) {
	initStore(t)
	lone := addTask(t, "lonely")

	out, code := run(t, "--ndjson", "dep", lone, "--list")
	if code != 0 {
		t.Fatalf("dep --list --ndjson exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "{") || !strings.Contains(lines[0], `"blocks"`) {
		t.Errorf("--ndjson should emit one compact object line:\n%s", out)
	}
}

func TestDepListHumanTwoSections(t *testing.T) {
	initStore(t)
	base := addTask(t, "base task")
	mid := addTask(t, "middle task")
	run(t, "dep", mid, base)

	out, code := run(t, "dep", mid, "--list")
	if code != 0 {
		t.Fatalf("dep --list human exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "depends on (1):") || !strings.Contains(out, "blocks (0):") {
		t.Errorf("human output should label both sections with counts:\n%s", out)
	}
	if !strings.Contains(out, base) || !strings.Contains(out, "base task") {
		t.Errorf("human output should resolve the dep to id+title:\n%s", out)
	}
}

func TestDepListNotFoundExit1(t *testing.T) {
	initStore(t)
	addTask(t, "something")

	fe, _ := runErr(t, "--json", "dep", "t-zzzzz", "--list")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("an unknown id should be NotFound (exit 1), got %+v", fe)
	}
}

func TestDepListRejectsRmCombo(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")
	run(t, "dep", a, b)

	// --list and --rm are mutually exclusive (cobra usage error -> exit 2).
	_, code := run(t, "dep", a, "--list", "--rm")
	if code != int(core.CodeValidation) {
		t.Fatalf("--list with --rm should exit 2, got %d", code)
	}
}

func TestDepListRejectsExtraArgs(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	// --list takes exactly the subject id; a stray dep-id is a usage error.
	_, code := run(t, "dep", a, b, "--list")
	if code != int(core.CodeValidation) {
		t.Fatalf("--list with an extra arg should exit 2, got %d", code)
	}
}

func TestDepAddStillWorks(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	// Regression: the mutation path (no --list) is unchanged.
	out, code := run(t, "--json", "dep", a, b)
	if code != 0 {
		t.Fatalf("dep add exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, `"deps"`) || !strings.Contains(out, b) {
		t.Errorf("dep add should still emit the mutation envelope with the new dep:\n%s", out)
	}
}
