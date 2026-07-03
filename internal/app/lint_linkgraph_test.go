package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// seedTask injects a task straight into the store, bypassing the app mutators so
// a test can build states the mutators forbid (like a dependency cycle).
func seedTask(t *testing.T, a *App, task core.Task, body string) {
	t.Helper()
	if task.Body == "" {
		task.Body = core.BodyPath(task.ID)
	}
	if task.Created.IsZero() {
		task.Created = a.Clock.Now()
	}
	if task.Updated.IsZero() {
		task.Updated = a.Clock.Now()
	}
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(task)
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}
}

func TestLintReportsDepCycle(t *testing.T) {
	a := newApp()
	seedTask(t, a, core.Task{ID: "t-aa", Status: "ready", Deps: []string{"t-bb"}}, "# a\n")
	seedTask(t, a, core.Task{ID: "t-bb", Status: "ready", Deps: []string{"t-aa"}}, "# b\n")
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	cycles := 0
	for _, p := range ps {
		if p.Severity == core.SevError && strings.Contains(p.Msg, "dependency cycle") {
			cycles++
		}
	}
	if cycles != 1 {
		t.Errorf("expected one dependency-cycle error, got %d: %+v", cycles, ps)
	}
	if !core.HasErrors(ps) {
		t.Error("a dependency cycle must make lint fail (HasErrors)")
	}
}

func TestLintSelfDepReportedOnce(t *testing.T) {
	// A self-dep is a degenerate cycle; it must be reported exactly once and not
	// also duplicated by any other rule.
	a := newApp()
	seedTask(t, a, core.Task{ID: "t-xx", Status: "ready", Deps: []string{"t-xx"}}, "# x\n")
	ps, _ := a.Lint()
	errs := 0
	for _, p := range ps {
		if p.Severity == core.SevError {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("self-dep should yield exactly one error, got %d: %+v", errs, ps)
	}
}

func TestLintReportsDanglingLink(t *testing.T) {
	a := newApp()
	live, _ := a.Add("live", AddOpts{Status: "ready"})
	// One reference resolves (self), one dangles.
	if err := a.Store.SaveBody(live.ID, "# live\n\nrelates to [["+live.ID+"]] and [[t-gone0]]\n"); err != nil {
		t.Fatal(err)
	}
	ps, _ := a.Lint()
	dangling := 0
	for _, p := range ps {
		if p.Severity == core.SevWarn && strings.Contains(p.Msg, "t-gone0") {
			dangling++
		}
		if strings.Contains(p.Msg, live.ID) && strings.Contains(p.Msg, "no such task") {
			t.Errorf("link to an existing task must not be dangling: %q", p.Msg)
		}
	}
	if dangling != 1 {
		t.Errorf("expected one dangling-link warn for [[t-gone0]], got %d: %+v", dangling, ps)
	}
}

func TestLintCodeSpanLinkNotDangling(t *testing.T) {
	// A [[t-x]] written as a documented example inside a code span must not raise
	// a dangling-link warning — the dangling scan inherits ExtractLinks' code
	// stripping. This keeps furrow's own notation-documenting bodies from
	// self-flagging.
	a := newApp()
	live, _ := a.Add("documents the notation", AddOpts{Status: "ready"})
	if err := a.Store.SaveBody(live.ID, "# doc\n\nuse `[[t-x]]` to link a task\n"); err != nil {
		t.Fatal(err)
	}
	ps, _ := a.Lint()
	for _, p := range ps {
		if strings.Contains(p.Msg, "t-x") && strings.Contains(p.Msg, "no such task") {
			t.Errorf("a [[t-x]] inside a code span must not be dangling: %q", p.Msg)
		}
	}
}

func TestLintArchivedIDNotDangling(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Clock = &fixedClock{t: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-arch1", Title: "old done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Body: core.BodyPath("t-arch1")})
	idx.Add(core.Task{ID: "t-live1", Title: "live", Status: "ready", Priority: 100,
		Created: old, Updated: old, Body: core.BodyPath("t-live1")})
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	a.Store.SaveBody("t-arch1", "# old done\n")
	a.Store.SaveBody("t-live1", "depends on [[t-arch1]] and [[t-mssng]]\n")

	// Move the aged done task into .furrow/archive/ (out of the hot store).
	if _, err := a.Archive(30, false); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, p := range ps {
		if strings.Contains(p.Msg, "t-arch1") && strings.Contains(p.Msg, "no such task") {
			t.Errorf("a link to an archived task must not be dangling: %q", p.Msg)
		}
		if strings.Contains(p.Msg, "t-mssng") && strings.Contains(p.Msg, "no such task") {
			missing++
		}
	}
	if missing != 1 {
		t.Errorf("a truly-missing [[t-mssng]] should warn once, got %d: %+v", missing, ps)
	}
}
