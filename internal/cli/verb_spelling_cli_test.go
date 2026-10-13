package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// t-5r9g: the verb-first spellings a first-time agent reaches for — `dep add
// <id> <dep-id>` / `dep rm <id> <dep-id>` — are the same calls as the bare
// `dep <id> <dep-id>` and `dep ... --rm`. A leading add/rm token can never be
// a task id (ids carry a prefix and a dash), so reading it as the verb loses
// nothing.
func TestDepAcceptsVerbFirstSpelling(t *testing.T) {
	initStore(t)
	a := addTask(t, "a task", "-s", "ready")
	b := addTask(t, "b task", "-s", "ready")

	if out, code := run(t, "dep", "add", a, b); code != 0 {
		t.Fatalf("dep add exit=%d:\n%s", code, out)
	}
	if deps := depsOf(t, a); !slices.Equal(deps, []string{b}) {
		t.Fatalf("after `dep add`, %s depends on %v, want [%s]", a, deps, b)
	}
	if out, code := run(t, "dep", "rm", a, b); code != 0 {
		t.Fatalf("dep rm exit=%d:\n%s", code, out)
	}
	if deps := depsOf(t, a); len(deps) != 0 {
		t.Fatalf("after `dep rm`, %s still depends on %v", a, deps)
	}

	// The verb and a contradicting or unrelated flag is a usage error, not a
	// silent pick of either.
	for _, args := range [][]string{
		{"dep", "add", a, b, "--rm"},
		{"dep", "add", a, "--list"},
		{"dep", "rm", a, "--list"},
	} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Kind != core.KindValidation {
			t.Errorf("%v: want kind validation, got %+v", args, fe)
		}
	}
	// The verb alone still needs the two ids.
	if fe, _ := runErr(t, "dep", "add", a); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("`dep add <id>` without a dep-id: want exit 2, got %+v", fe)
	}
}

// The same two spellings on the box side: `epic dep add` / `epic dep rm`.
func TestEpicDepAcceptsVerbFirstSpelling(t *testing.T) {
	initStore(t)
	x := addEpic(t, "box x")
	y := addEpic(t, "box y")

	if out, code := run(t, "epic", "dep", "add", x, y); code != 0 {
		t.Fatalf("epic dep add exit=%d:\n%s", code, out)
	}
	if deps := epicDepsOf(t, x); !slices.Equal(deps, []string{y}) {
		t.Fatalf("after `epic dep add`, %s waits on %v, want [%s]", x, deps, y)
	}
	if out, code := run(t, "epic", "dep", "rm", x, y); code != 0 {
		t.Fatalf("epic dep rm exit=%d:\n%s", code, out)
	}
	if deps := epicDepsOf(t, x); len(deps) != 0 {
		t.Fatalf("after `epic dep rm`, %s still waits on %v", x, deps)
	}
	if fe, _ := runErr(t, "epic", "dep", "add", x, y, "--rm"); fe == nil || fe.Kind != core.KindValidation {
		t.Errorf("`epic dep add --rm`: want kind validation, got %+v", fe)
	}
}

// t-5r9g: `epic add <epic> <task>` is the other first-time mistake (the agent
// means "put this task into that box"). The usage error must say which
// command does that, and when the arguments look like ids it names the exact
// call in candidates. A multi-word unquoted title gets the quoting hint and no
// candidate.
func TestEpicAddWithIDsPointsAtSetEpic(t *testing.T) {
	initStore(t)
	task := addTask(t, "a task")
	box := addEpic(t, "a box")

	fe, _ := runErr(t, "epic", "add", box, task)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("epic add <epic> <task>: want exit 2, got %+v", fe)
	}
	if !strings.Contains(fe.Msg, "set") || !strings.Contains(fe.Msg, "-e") {
		t.Errorf("message must point at `set -e`: %q", fe.Msg)
	}
	want := "furrow set " + task + " -e " + box
	if !slices.Contains(fe.Candidates, want) {
		t.Errorf("candidates = %v, want to contain %q", fe.Candidates, want)
	}

	fe, _ = runErr(t, "epic", "add", "two", "words", "here")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("epic add with an unquoted title: want exit 2, got %+v", fe)
	}
	if !strings.Contains(fe.Msg, "quote") {
		t.Errorf("message must say to quote the title: %q", fe.Msg)
	}
	if len(fe.Candidates) != 0 {
		t.Errorf("an unquoted title has no concrete remedy; candidates = %v", fe.Candidates)
	}

	// The one-argument call is untouched.
	if _, code := run(t, "epic", "add", "a quoted title"); code != 0 {
		t.Errorf("epic add with one title: exit %d", code)
	}
}

func depsOf(t *testing.T, id string) []string {
	t.Helper()
	out, code := run(t, "--json", "dep", id, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	var res depListJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse dep --list --json: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(res.DependsOn))
	for _, d := range res.DependsOn {
		ids = append(ids, d.ID)
	}
	return ids
}

func epicDepsOf(t *testing.T, id string) []string {
	t.Helper()
	out, code := run(t, "--json", "epic", "dep", id, "--list")
	if code != 0 {
		t.Fatalf("epic dep --list exit=%d:\n%s", code, out)
	}
	var res epicDepListJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse epic dep --list --json: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(res.DependsOn))
	for _, d := range res.DependsOn {
		ids = append(ids, d.ID)
	}
	return ids
}
