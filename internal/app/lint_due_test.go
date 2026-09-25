package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// problemsWithCode narrows to one code so a case asserts on ITS finding and not
// on the rest of a board's lint output.
func problemsWithCode(ps []core.Problem, code string) []core.Problem {
	var out []core.Problem
	for _, p := range ps {
		if p.Code == code {
			out = append(out, p)
		}
	}
	return out
}

// `furrow lint` is the twin of brief's section: an arrived date must be found by
// a whole-board sweep, with overdue an ERROR (it drives the exit code) and today
// a warning.
func TestLintDueSeverities(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC)) // 12:00 JST
	late, _ := a.Add("late", AddOpts{Status: "ready", Due: "2026-08-01"})
	today, _ := a.Add("today", AddOpts{Status: "waiting", Due: "2026-08-04"})
	a.Add("fine", AddOpts{Status: "ready", Due: "2026-09-01"}) //nolint:errcheck // asserted via lint below

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	over := problemsWithCode(ps, "due-overdue")
	if len(over) != 1 || over[0].ID != late.ID || over[0].Severity != core.SevError {
		t.Errorf("due-overdue = %+v, want one ERROR on %s", over, late.ID)
	}
	td := problemsWithCode(ps, "due-today")
	if len(td) != 1 || td[0].ID != today.ID || td[0].Severity != core.SevWarn {
		t.Errorf("due-today = %+v, want one warn on %s", td, today.ID)
	}
	// The error must reach the count sync/brief ride along with, or an overdue
	// board would report itself clean in the loop that always runs.
	sum, err := a.LintErrorCounts()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Codes["due-overdue"] != 1 {
		t.Errorf("LintErrorCounts codes = %v, want one due-overdue", sum.Codes)
	}
}

// The lane rule, which is the whole reason this is not just "terminal lanes are
// exempt": a task parked in `waiting` (terminal on the shipped config) is the
// archetype of a dated task and MUST report; done and icebox must not.
func TestLintDueLaneExclusions(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	waiting, _ := a.Add("waiting on a nightly run", AddOpts{Status: "waiting", Due: "2026-08-01"})
	a.Add("parked", AddOpts{Status: "icebox", Due: "2026-08-01"}) //nolint:errcheck // asserted below
	a.Add("shipped", AddOpts{Status: "done", Due: "2026-08-01"})  //nolint:errcheck // asserted below

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	over := problemsWithCode(ps, "due-overdue")
	if len(over) != 1 || over[0].ID != waiting.ID {
		t.Errorf("due-overdue = %+v, want exactly the waiting task %s", over, waiting.ID)
	}
}

// [due].ignore_lanes is what names the parked lanes, so a board that clears it
// gets nagged everywhere but the done lane — and a board that adds a lane goes
// quiet there without touching furrow.
func TestLintDueRespectsConfiguredIgnoreLanes(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	a.Cfg.DueIgnoreLanes = map[string]bool{}
	a.Add("parked", AddOpts{Status: "icebox", Due: "2026-08-01"}) //nolint:errcheck // asserted below
	a.Add("shipped", AddOpts{Status: "done", Due: "2026-08-01"})  //nolint:errcheck // asserted below

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if over := problemsWithCode(ps, "due-overdue"); len(over) != 1 {
		t.Errorf("with no ignored lanes: due-overdue = %+v, want the icebox task only (done is always exempt)", over)
	}

	a.Cfg.DueIgnoreLanes = map[string]bool{"waiting": true}
	a.Add("waiting", AddOpts{Status: "waiting", Due: "2026-08-01"}) //nolint:errcheck // asserted below
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problemsWithCode(ps, "due-overdue") {
		if tk, _, _ := a.Get(p.ID); tk != nil && tk.Status == "waiting" {
			t.Errorf("waiting is in ignore_lanes but still reported: %+v", p)
		}
	}
}

// The board-wide claim: no epic scope. A dated task filed under a box nobody
// activated — the case the field exists for — must still be found.
func TestLintDueIgnoresEpicScope(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	active, err := a.EpicAdd("the focus", EpicAddOpts{Repos: []string{"akira-toriyama/furrow"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.EpicActivate(active.ID, ""); err != nil {
		t.Fatal(err)
	}
	other, err := a.EpicAdd("operations", EpicAddOpts{Repos: []string{"akira-toriyama/furrow"}})
	if err != nil {
		t.Fatal(err)
	}
	parked, err := a.Add("check the nightly run", AddOpts{Status: "waiting", Epic: other.ID, Due: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}

	// `next` cannot see it — that is exactly why lint must.
	nx, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range nx {
		if tk.ID == parked.ID {
			t.Fatalf("next surfaced %s; this test's premise is that it cannot", parked.ID)
		}
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if over := problemsWithCode(ps, "due-overdue"); len(over) != 1 || over[0].ID != parked.ID {
		t.Errorf("due-overdue = %+v, want %s (a box nobody activated)", over, parked.ID)
	}
}

// The remedy follows the lane (t-8dtm, furrow-test drills 3 and 4): a task
// parked in a terminal lane is not being worked, so "do it" is the one remedy
// that is wrong there — its date is a reminder to chase whoever it waits on —
// while an open lane is offered the work itself. Both name the close, because
// a finished wait is closed from where it sits.
func TestLintDueOverdueRemedyFollowsTheLane(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	open, _ := a.Add("open and late", AddOpts{Status: "ready", Due: "2026-08-01"})
	parked, _ := a.Add("waiting on a reply", AddOpts{Status: "waiting", Due: "2026-08-01"})

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]string{}
	for _, p := range problemsWithCode(ps, "due-overdue") {
		msgs[p.ID] = p.Msg
	}
	if m := msgs[open.ID]; !strings.Contains(m, "do it, close it (`furrow done "+open.ID+"`)") || strings.Contains(m, "parked") {
		t.Errorf("open-lane remedy = %q, want the work and the close, no chase", m)
	}
	if m := msgs[parked.ID]; !strings.Contains(m, `parked in "waiting"`) || !strings.Contains(m, "chase or escalate, close it (`furrow done "+parked.ID+"`)") || strings.Contains(m, "do it") {
		t.Errorf("terminal-lane remedy = %q, want the chase and the close, never \"do it\"", m)
	}
}

// due-inversion through App.Lint (t-e19e): the shipped skip set (done plus
// [due].ignore_lanes) silences a parked or settled dep, and re-dating the dep
// to the task's own day clears the warn.
func TestLintDueInversion(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 4, 3, 0, 0, 0, time.UTC))
	dep, _ := a.Add("the input", AddOpts{Status: "ready", Due: "2026-09-15"})
	task, _ := a.Add("the work", AddOpts{Status: "backlog", Due: "2026-09-10", Deps: []string{dep.ID}})
	parked, _ := a.Add("parked input", AddOpts{Status: "icebox", Due: "2026-09-30"})
	a.Add("waits on a parked date", AddOpts{Status: "backlog", Due: "2026-09-10", Deps: []string{parked.ID}}) //nolint:errcheck // asserted via lint below

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	inv := problemsWithCode(ps, "due-inversion")
	if len(inv) != 1 || inv[0].ID != task.ID || inv[0].Severity != core.SevWarn || !strings.Contains(inv[0].Msg, dep.ID) {
		t.Fatalf("due-inversion = %+v, want one warn on %s naming %s", inv, task.ID, dep.ID)
	}

	due := "2026-09-10"
	if _, _, _, err := a.Set(dep.ID, SetOpts{Due: &due}); err != nil {
		t.Fatal(err)
	}
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if inv := problemsWithCode(ps, "due-inversion"); len(inv) != 0 {
		t.Errorf("after re-dating the dep to the task's day: %+v, want none", inv)
	}
}
