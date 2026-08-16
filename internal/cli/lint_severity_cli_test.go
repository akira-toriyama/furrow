package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The [lint.severity] loop, end to end through the CLI: a board with an
// overdue task reds `furrow lint` (exit 2) until the board demotes the code —
// written through `furrow config set lint.severity.due-overdue warn`, the
// dynamic sub-table spelling — after which the finding is still shown but the
// exit is 0. A typo'd LEVEL is refused at set time (the regression guard sees
// the loader's clamp warning); a typo'd CODE writes fine and `furrow lint`
// carries the config-clamp warn naming it (the ignore_codes split).
func TestCLILintSeverityOverride(t *testing.T) {
	initStore(t)
	run(t, "add", "overdue promise", "--due", "2020-01-02")

	if _, code := lintProblems(t); code != 2 {
		t.Fatalf("lint on an overdue board = exit %d, want 2", code)
	}

	if out, code := run(t, "config", "set", "lint.severity.due-overdue", "warn"); code != 0 {
		t.Fatalf("config set lint.severity.due-overdue: exit %d\n%s", code, out)
	}
	ps, code := lintProblems(t)
	if code != 0 {
		t.Fatalf("lint after the demotion = exit %d, want 0", code)
	}
	var demoted bool
	for _, p := range ps {
		if p.Code == "due-overdue" && p.Severity == core.SevWarn {
			demoted = true
		}
	}
	if !demoted {
		t.Errorf("due-overdue is not a warn after the demotion: %+v", ps)
	}

	// A level the reader would clamp away is refused BEFORE the write.
	if fe, _ := runErr(t, "config", "set", "lint.severity.due-overdue", "loud"); fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "clamp") {
		t.Fatalf("config set with a bad level = %+v, want the exit-2 clamp refusal", fe)
	}

	// A dead CODE is the app layer's to warn about, not the writer's to refuse.
	if _, code := run(t, "config", "set", "lint.severity.reconile-gap", "warn"); code != 0 {
		t.Fatalf("config set with an unknown code should write (lint warns later), got exit %d", code)
	}
	ps, _ = lintProblems(t)
	var dead bool
	for _, p := range ps {
		if p.Code == "config-clamp" && strings.Contains(p.Msg, `"reconile-gap"`) {
			dead = true
		}
	}
	if !dead {
		t.Errorf("no config-clamp warn names the dead lint.severity entry: %+v", ps)
	}
}
