package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func day(d int) time.Time { return time.Date(2026, 6, d, 0, 0, 0, 0, time.UTC) }

// staleDepWarns reports whether lint flagged the given task with a reconcile-gap
// warning (a done dep that closed after the task's last update). It matches the
// stable `code`, never the message: CLAUDE.md tells callers to branch on the
// code, and a substring match would both pass for a DIFFERENT warning whose
// prose happens to say "reconcile" and break on a harmless reword.
func staleDepWarns(ps []core.Problem, id string) bool {
	for _, p := range ps {
		if p.ID == id && p.Severity == core.SevWarn && p.Code == "reconcile-gap" {
			return true
		}
	}
	return false
}

func TestLintWarnsUnreconciledDoneDep(t *testing.T) {
	a := newApp()
	closed := day(24)
	// epic last touched on the 20th; its slice shipped on the 24th -> the epic
	// has not been reconciled since the slice landed.
	seedTask(t, a, core.Task{ID: "t-epic", Status: "backlog", Deps: []string{"t-slice"}, Updated: day(20)}, "# epic\n")
	seedTask(t, a, core.Task{ID: "t-slice", Status: "done", Closed: &closed, Updated: day(24)}, "# slice\n")

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if !staleDepWarns(ps, "t-epic") {
		t.Errorf("expected a reconcile-gap warn on t-epic naming t-slice: %+v", ps)
	}
	// It is advisory only: a reconcile gap alone must not make lint fail.
	if core.HasErrors(ps) {
		t.Errorf("a reconcile gap must not be an error: %+v", ps)
	}
}

func TestLintNoWarnWhenReconciled(t *testing.T) {
	a := newApp()
	closed := day(20)
	// The slice shipped on the 20th and the epic was touched on the 24th (after)
	// -> already reconciled, no warn.
	seedTask(t, a, core.Task{ID: "t-epic", Status: "backlog", Deps: []string{"t-slice"}, Updated: day(24)}, "# epic\n")
	seedTask(t, a, core.Task{ID: "t-slice", Status: "done", Closed: &closed, Updated: day(20)}, "# slice\n")

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if staleDepWarns(ps, "t-epic") {
		t.Errorf("a reconciled epic must not warn: %+v", ps)
	}
}
