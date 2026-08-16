package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// [lint.severity] is a board POLICY knob, so what must hold is not "the string
// changed" but that every consumer sees the effective level: the problem list,
// the exit-code driver (HasErrors via the CLI), and the sync ride-along's
// error count. due-overdue is the motivating code — an error whose consumer is
// a shared board's CI gate, demoted on a board that has no CI to redden.
func TestLintSeverityDemotesAnError(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	a.Cfg.LintSeverity = map[string]string{"due-overdue": "warn"}
	late, _ := a.Add("late", AddOpts{Status: "ready", Due: "2026-08-01"})

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	over := problemsWithCode(ps, "due-overdue")
	if len(over) != 1 || over[0].ID != late.ID || over[0].Severity != core.SevWarn {
		t.Errorf("due-overdue = %+v, want one WARN on %s (demoted)", over, late.ID)
	}
	// The demotion must reach the error count sync/brief ride along with — a
	// demoted code that still reddened the sync line would be a policy the loop
	// ignores.
	sum, err := a.LintErrorCounts()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Codes["due-overdue"] != 0 || sum.Errors != 0 {
		t.Errorf("LintErrorCounts = %+v, want no due-overdue error after the demotion", sum)
	}
}

// The other direction: a board may promote a warn it wants gating. orphan-body
// is a convenient structurally-produced warn on a memstore (a body with no
// task), and the promotion must land in the error count exactly as a shipped
// error would.
func TestLintSeverityPromotesAWarn(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	a.Cfg.LintSeverity = map[string]string{"orphan-body": "error"}
	if err := a.Store.SaveBody("t-orphan", "# stray\n"); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	orphan := problemsWithCode(ps, "orphan-body")
	if len(orphan) != 1 || orphan[0].Severity != core.SevError {
		t.Errorf("orphan-body = %+v, want one ERROR (promoted)", orphan)
	}
	sum, err := a.LintErrorCounts()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Codes["orphan-body"] != 1 {
		t.Errorf("LintErrorCounts codes = %v, want the promoted orphan-body", sum.Codes)
	}
}

// A severity entry naming no real code overrides nothing, and this warn is its
// only signal — the ignore_codes contract carried over. The dead entry must
// also be absent from LintSeverityOverrides, so ApplySeverity never consumes it.
func TestLintSeverityUnknownCodeWarns(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	a.Cfg.LintSeverity = map[string]string{"due-overdu": "warn"}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var hit bool
	for _, p := range problemsWithCode(ps, "config-clamp") {
		if strings.Contains(p.Msg, `lint.severity entry "due-overdu"`) {
			hit = true
		}
	}
	if !hit {
		t.Errorf("no config-clamp warn names the dead lint.severity entry; got %+v", ps)
	}
	if ov := a.LintSeverityOverrides(); len(ov) != 0 {
		t.Errorf("LintSeverityOverrides = %v, want the dead code dropped", ov)
	}
}
