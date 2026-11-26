package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

func day(d int) time.Time { return time.Date(2026, 6, d, 0, 0, 0, 0, time.UTC) }

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
	if !hasProblem(ps, "reconcile-gap", "t-epic", core.SevWarn) {
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
	if hasProblem(ps, "reconcile-gap", "t-epic", core.SevWarn) {
		t.Errorf("a reconciled epic must not warn: %+v", ps)
	}
}

// reconcile-gap reads the lanes revisit reads. A task parked until its dep
// closes is out of sight by default — and told so once the board names its
// lane in [revisit].parked_lanes, where the dep closing is the very event the
// task waits for. A parked lane nobody named stays quiet either way.
func TestLintReconcileGapFollowsRevisitParkedLanes(t *testing.T) {
	seed := func(a *App) {
		closed := day(24)
		seedTask(t, a, core.Task{ID: "t-wait", Status: "waiting", Deps: []string{"t-slice"}, Updated: day(20)}, "# wait\n")
		seedTask(t, a, core.Task{ID: "t-ice", Status: "icebox", Deps: []string{"t-slice"}, Updated: day(20)}, "# ice\n")
		seedTask(t, a, core.Task{ID: "t-slice", Status: "done", Closed: &closed, Updated: day(24)}, "# slice\n")
	}

	a := newApp()
	seed(a)
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if hasProblem(ps, "reconcile-gap", "t-wait", "") || hasProblem(ps, "reconcile-gap", "t-ice", "") {
		t.Errorf("no terminal-lane task may warn by default: %+v", ps)
	}

	a, _ = newAppWith(watchWaiting())
	seed(a)
	if ps, err = a.Lint(); err != nil {
		t.Fatal(err)
	}
	if !hasProblem(ps, "reconcile-gap", "t-wait", core.SevWarn) {
		t.Errorf("a task in a lane revisit reads must warn when its dep closed after its last update: %+v", ps)
	}
	if hasProblem(ps, "reconcile-gap", "t-ice", "") {
		t.Errorf("a parked lane nobody named must stay quiet: %+v", ps)
	}
}
