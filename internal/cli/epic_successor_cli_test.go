package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

type successorJSON struct {
	Declared string            `json:"declared"`
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	State    string            `json:"state"`
	Reason   string            `json:"reason"`
	Skipped  []string          `json:"skipped"`
	OpenDeps []string          `json:"open_deps"`
	Held     map[string]string `json:"held"`
}

type doneEnvelope struct {
	Changed   []string             `json:"changed"`
	Previous  *struct{ ID string } `json:"previous"`
	Successor *successorJSON       `json:"successor"`
}

func parseDone(t *testing.T, out string) doneEnvelope {
	t.Helper()
	var env doneEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, out)
	}
	return env
}

// `epic done` proposes the declared successor in place of the previous box:
// one human line with the command, a `successor` object in --json, and no
// previous line when the proposal covers every freed slot.
func TestEpicDoneProposesTheDeclaredSuccessor(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "aws migration", "-r", "me/r1")
	eb := addEpic(t, "standardization", "-r", "me/r1")
	older := addEpic(t, "older focus", "-r", "me/r1")
	mustRun(t, "epic", "activate", older)
	mustRun(t, "epic", "deactivate", older)

	out, code := run(t, "--json", "epic", "set", ea, "--successor", "standard")
	if code != 0 {
		t.Fatalf("epic set --successor exit = %d:\n%s", code, out)
	}
	if env := parseDone(t, out); len(env.Changed) != 1 || env.Changed[0] != "successor" {
		t.Errorf("changed = %v, want [successor]", env.Changed)
	}
	if ls := mustRun(t, "epic", "ls"); !strings.Contains(ls, "→ then "+eb) {
		t.Errorf("epic ls must show the plan on the declarer's row:\n%s", ls)
	}
	if show := mustRun(t, "epic", "show", ea); !strings.Contains(show, "successor: "+eb+" (open)") {
		t.Errorf("epic show must print the resolved successor:\n%s", show)
	}

	mustRun(t, "epic", "activate", ea)
	got, code := run(t, "epic", "done", ea)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if !strings.Contains(got, "successor: "+eb) || !strings.Contains(got, "furrow epic activate "+eb) {
		t.Errorf("the successor line must name the box and the command:\n%s", got)
	}
	if strings.Contains(got, "previous:") {
		t.Errorf("the proposal covers the freed slot, so no previous line:\n%s", got)
	}
	if brief := mustRun(t, "brief"); !strings.Contains(brief, "epic: (none active — "+eb) || !strings.Contains(brief, "to follow "+ea) {
		t.Errorf("brief must name the plan while nothing is active:\n%s", brief)
	}
	if ls := mustRun(t, "epic", "ls"); strings.Contains(ls, "→ then") {
		t.Errorf("the closed declarer leaves the default ls, so no plan tag remains:\n%s", ls)
	}

	// The JSON side, on a second declarer: the proposal is a plain object,
	// previous rides alongside as always.
	ec := addEpic(t, "c", "-r", "me/r1")
	mustRun(t, "epic", "set", ec, "--successor", eb)
	mustRun(t, "epic", "activate", ec)
	env := parseDone(t, mustRun(t, "--json", "epic", "done", ec))
	if env.Successor == nil || env.Successor.ID != eb || env.Successor.Declared != eb || env.Successor.Reason != "" || env.Successor.State != "open" {
		t.Errorf("successor = %+v, want the plain proposal of %s", env.Successor, eb)
	}
	if env.Successor != nil && (env.Successor.Skipped == nil || env.Successor.OpenDeps == nil) {
		t.Errorf("skipped and open_deps are arrays, never null: %+v", env.Successor)
	}
	if env.Previous == nil {
		t.Errorf("previous is always computed in --json:\n%s", out)
	}
}

// With no declaration the close behaves exactly as before (previous line,
// successor null); with a spent one the previous line speaks for the slot.
func TestEpicDoneFallsBackToPreviousWhenThePlanIsSpent(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "a", "-r", "me/r1")
	eb := addEpic(t, "b", "-r", "me/r1")
	older := addEpic(t, "older", "-r", "me/r1")
	mustRun(t, "epic", "activate", older)
	mustRun(t, "epic", "deactivate", older)
	mustRun(t, "epic", "set", ea, "--successor", eb)
	ec := addEpic(t, "c", "-r", "me/r1")
	mustRun(t, "epic", "set", ec, "--successor", eb)
	mustRun(t, "epic", "done", eb)

	mustRun(t, "epic", "activate", ea)
	got := mustRun(t, "epic", "done", ea)
	if !strings.Contains(got, "successor: "+eb) || !strings.Contains(got, "is closed") {
		t.Errorf("a spent plan is reported as closed:\n%s", got)
	}
	if !strings.Contains(got, "previous: "+older) {
		t.Errorf("the previous line speaks for the uncovered slot:\n%s", got)
	}

	env := parseDone(t, mustRun(t, "--json", "epic", "done", older))
	if env.Successor != nil {
		t.Errorf("a box that declared nothing carries successor: null, got %+v", env.Successor)
	}

	// An INACTIVE declarer frees no slot: the spent plan is reported and no
	// previous line follows (nor any text pointing at one).
	got = mustRun(t, "epic", "done", ec)
	if !strings.Contains(got, "is closed") || strings.Contains(got, "previous") {
		t.Errorf("an inactive declarer with a spent plan prints the successor line alone:\n%s", got)
	}
}

// The refusals surface as exit 2 with the reason, and the two flags cannot
// ride one line.
func TestEpicSetSuccessorRefusals(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "a", "-r", "me/r1")
	eb := addEpic(t, "b", "-r", "me/r1")
	mustRun(t, "epic", "done", eb)
	fe, _ := runErr(t, "epic", "set", ea, "--successor", eb)
	if fe == nil || core.ExitCode(fe) != 2 || !strings.Contains(fe.Msg, "is closed") {
		t.Errorf("a closed target is exit 2 naming it: %+v", fe)
	}
	fe, _ = runErr(t, "epic", "set", ea, "--successor", "")
	if fe == nil || core.ExitCode(fe) != 2 || !strings.Contains(fe.Msg, "empty value") {
		t.Errorf("an empty --successor is exit 2: %+v", fe)
	}
	if _, code := run(t, "epic", "set", ea, "--successor", eb, "--clear-successor"); code == 0 {
		t.Error("--successor with --clear-successor must be refused")
	}
	ec := addEpic(t, "c", "-r", "me/r1")
	mustRun(t, "epic", "set", ea, "--successor", ec)
	fe, _ = runErr(t, "epic", "rm", ec, "--yes")
	if fe == nil || !strings.Contains(fe.Msg, "successor declaration") {
		t.Errorf("rm must refuse over the declaration: %+v", fe)
	}
	out := mustRun(t, "epic", "rm", ec, "--force", "--yes")
	if !strings.Contains(out, "successor "+ea+" -> "+ec) {
		t.Errorf("the severed declaration is named:\n%s", out)
	}
	if lint := mustRun(t, "lint", "--code", "epic-successor-missing", "--code", "epic-successor-closed"); strings.Contains(lint, ea) {
		t.Errorf("after the sever nothing dangles:\n%s", lint)
	}
}
