package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The mutation envelope: every other mutation emits {before, after, changed}, and
// `parent` must too — an agent branches on changed, never on prose.
func TestCLIParentEmitsMutationEnvelope(t *testing.T) {
	initStore(t)
	epic := addTask(t, "an epic")
	slice := addTask(t, "a slice")

	out, code := run(t, "--json", "parent", slice, epic)
	if code != int(core.CodeOK) {
		t.Fatalf("parent exit=%d:\n%s", code, out)
	}
	var env struct {
		After   core.Task `json:"after"`
		Changed []string  `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse parent --json: %v\n%s", err, out)
	}
	if env.After.Parent != epic {
		t.Errorf("after.parent = %q, want %q", env.After.Parent, epic)
	}
	if !containsStr(env.Changed, "parent") {
		t.Errorf("changed must name the field that moved, got %v", env.Changed)
	}

	// --rm detaches, and the envelope says so. Parse into a FRESH value: `parent`
	// is omitempty, so a cleared parent is an ABSENT key — and json.Unmarshal leaves
	// absent keys untouched, which would silently keep the old id in a reused struct.
	out, code = run(t, "--json", "parent", slice, "--rm")
	if code != int(core.CodeOK) {
		t.Fatalf("parent --rm exit=%d:\n%s", code, out)
	}
	var rmEnv struct {
		After   core.Task `json:"after"`
		Changed []string  `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &rmEnv); err != nil {
		t.Fatal(err)
	}
	if rmEnv.After.Parent != "" {
		t.Errorf("after --rm, parent = %q, want empty", rmEnv.After.Parent)
	}
	if !containsStr(rmEnv.Changed, "parent") {
		t.Errorf("detaching is a change, and changed must say so: %v", rmEnv.Changed)
	}
}

// --list is a READ, in both directions, with the same JSON discipline as
// `dep --list`: parent is null-or-object (0-or-1), children is always an array.
func TestCLIParentListBothDirections(t *testing.T) {
	initStore(t)
	epic := addTask(t, "an epic")
	a := addTask(t, "slice a")
	b := addTask(t, "slice b")
	for _, child := range []string{a, b} {
		if _, code := run(t, "parent", child, epic); code != 0 {
			t.Fatalf("parent %s %s failed", child, epic)
		}
	}

	out, code := run(t, "--json", "parent", epic, "--list")
	if code != int(core.CodeOK) {
		t.Fatalf("parent --list exit=%d:\n%s", code, out)
	}
	var v struct {
		Parent *struct {
			ID string `json:"id"`
		} `json:"parent"`
		Children []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if v.Parent != nil {
		t.Errorf("a top-level task's parent must be null, got %+v", v.Parent)
	}
	if len(v.Children) != 2 || v.Children[0].Title == "" || v.Children[0].Status == "" {
		t.Errorf("children must resolve to id+title+lane: %+v", v.Children)
	}

	// A leaf: parent present, children an empty ARRAY (never null).
	out, code = run(t, "--json", "parent", a, "--list")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(out, "\"children\": []") {
		t.Errorf("an empty children list must be [] not null:\n%s", out)
	}

	// Human output labels both directions, like dep --list.
	out, code = run(t, "parent", epic, "--list")
	if code != 0 || !strings.Contains(out, "parent:") || !strings.Contains(out, "children (2):") {
		t.Errorf("human output must label both sections (exit=%d):\n%s", code, out)
	}
}

func TestCLIParentRejectsBadUsage(t *testing.T) {
	initStore(t)
	epic := addTask(t, "an epic")
	slice := addTask(t, "a slice")

	// A cycle is a validation error, not a silent no-op.
	if _, code := run(t, "parent", slice, epic); code != 0 {
		t.Fatal("setup")
	}
	if fe, _ := runErr(t, "parent", epic, slice); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a parent cycle must be exit 2, got %+v", fe)
	}
	// A parent that does not exist would be lint's parent-missing ERROR — never create one.
	if fe, _ := runErr(t, "parent", slice, "t-404"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a missing parent must be exit 2, got %+v", fe)
	}
	// --list reads and --rm writes; asking for both is bad usage.
	if fe, _ := runErr(t, "parent", slice, "--list", "--rm"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("--list with --rm must be exit 2, got %+v", fe)
	}
	// An unknown id is a MISS (exit 1), not a validation error.
	if fe, _ := runErr(t, "parent", "t-404", "--list"); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("an unknown id is exit 1, got %+v", fe)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
