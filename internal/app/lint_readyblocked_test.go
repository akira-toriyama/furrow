package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// readyBlockedErrors reports whether lint flagged the task with a ready-blocked
// ERROR. Matches the stable code, never the message prose.
func readyBlockedErrors(ps []core.Problem, id string) bool {
	for _, p := range ps {
		if p.ID == id && p.Severity == core.SevError && p.Code == "ready-blocked" {
			return true
		}
	}
	return false
}

// A task in an actionable ([next].lanes) lane whose dep is still open is a
// board-state ERROR: `furrow next` will never hand it out, so its lane is lying.
func TestLintErrorsReadyTaskWithOpenDep(t *testing.T) {
	a := newApp()
	seedTask(t, a, core.Task{ID: "t-work", Status: "ready", Deps: []string{"t-dep"}}, "# work\n")
	seedTask(t, a, core.Task{ID: "t-dep", Status: "backlog"}, "# dep\n")

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if !readyBlockedErrors(ps, "t-work") {
		t.Errorf("expected a ready-blocked error on t-work: %+v", ps)
	}
	if !core.HasErrors(ps) {
		t.Errorf("ready-blocked must fail lint: %+v", ps)
	}
}

// The same blocked shape parked OUTSIDE the next lanes is fine — waiting on a
// dep is exactly what backlog is for. And once the dep is done, the ready task
// is clean.
func TestLintReadyBlockedQuietWhenParkedOrSatisfied(t *testing.T) {
	a := newApp()
	seedTask(t, a, core.Task{ID: "t-parked", Status: "backlog", Deps: []string{"t-open"}}, "# parked\n")
	seedTask(t, a, core.Task{ID: "t-open", Status: "backlog"}, "# open\n")
	closed := a.Clock.Now()
	seedTask(t, a, core.Task{ID: "t-work", Status: "ready", Deps: []string{"t-shipped"}}, "# work\n")
	seedTask(t, a, core.Task{ID: "t-shipped", Status: "done", Closed: &closed}, "# shipped\n")

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t-parked", "t-work"} {
		if readyBlockedErrors(ps, id) {
			t.Errorf("unexpected ready-blocked error on %s: %+v", id, ps)
		}
	}
}
