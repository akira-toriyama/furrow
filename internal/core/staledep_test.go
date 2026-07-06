package core

import (
	"strings"
	"testing"
	"time"
)

// tm is a terse timestamp helper for these tests: day N of 2026-01, UTC.
func tm(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }

// ptm returns a pointer to tm(day) — for the Closed field.
func ptm(day int) *time.Time { t := tm(day); return &t }

func TestStaleDepProblems(t *testing.T) {
	// The done lane is "done"; "icebox" is a non-done terminal lane.
	terminal := map[string]bool{"done": true, "icebox": true}
	doneIDs := map[string]bool{"slice-done": true}

	cases := []struct {
		name  string
		tasks []Task
		// wantIDs is every task id that must own exactly one warn. len(wantIDs)
		// with wantN lets a case assert both the total and which tasks warned.
		wantN   int
		wantIDs []string
	}{
		{
			name: "done dep closed after the epic's last update warns",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-done"}, Updated: tm(1)},
				{ID: "slice-done", Status: "done", Closed: ptm(3)},
			},
			wantN:   1,
			wantIDs: []string{"epic"},
		},
		{
			name: "done dep closed before the epic's last update is already reconciled",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-done"}, Updated: tm(5)},
				{ID: "slice-done", Status: "done", Closed: ptm(3)},
			},
			wantN: 0,
		},
		{
			name: "dep closed exactly at the epic's update is reconciled (not strictly after)",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-done"}, Updated: tm(3)},
				{ID: "slice-done", Status: "done", Closed: ptm(3)},
			},
			wantN: 0,
		},
		{
			name: "dep not in the done lane does not warn",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-open"}, Updated: tm(1)},
				{ID: "slice-open", Status: "in-progress"},
			},
			wantN: 0,
		},
		{
			name: "a terminal task is never its own reconcile subject",
			tasks: []Task{
				{ID: "epic", Status: "done", Deps: []string{"slice-done"}, Updated: tm(1)},
				{ID: "slice-done", Status: "done", Closed: ptm(3)},
			},
			wantN: 0,
		},
		{
			name: "a done dep with no Closed stamp is skipped (no gap to measure)",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-done"}, Updated: tm(1)},
				{ID: "slice-done", Status: "done", Closed: nil},
			},
			wantN: 0,
		},
		{
			name: "each stale done dep warns once (per-dep, like Validate)",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"slice-done", "slice-two"}, Updated: tm(1)},
				{ID: "slice-done", Status: "done", Closed: ptm(3)},
				{ID: "slice-two", Status: "done", Closed: ptm(4)},
			},
			wantN:   2,
			wantIDs: []string{"epic", "epic"},
		},
		{
			name: "an unknown dep contributes no warn",
			tasks: []Task{
				{ID: "epic", Status: "backlog", Deps: []string{"ghost"}, Updated: tm(1)},
			},
			wantN: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// slice-two is done too in the multi-dep case; extend doneIDs locally.
			dIDs := map[string]bool{}
			for k, v := range doneIDs {
				dIDs[k] = v
			}
			for _, tk := range c.tasks {
				if tk.Status == "done" {
					dIDs[tk.ID] = true
				}
			}
			ps := StaleDepProblems(&Index{Tasks: c.tasks}, terminal, dIDs)
			if len(ps) != c.wantN {
				t.Fatalf("got %d problems, want %d: %+v", len(ps), c.wantN, ps)
			}
			for _, p := range ps {
				if p.Severity != SevWarn {
					t.Errorf("a reconcile gap must warn, not %q: %+v", p.Severity, p)
				}
			}
			// Every expected owner id must appear as a problem id, in order.
			for i, id := range c.wantIDs {
				if i >= len(ps) {
					break
				}
				if ps[i].ID != id {
					t.Errorf("problem %d owned by %q, want %q", i, ps[i].ID, id)
				}
			}
		})
	}
}

func TestStaleDepProblemsMessageNamesTheDep(t *testing.T) {
	terminal := map[string]bool{"done": true}
	doneIDs := map[string]bool{"t-slice": true}
	idx := &Index{Tasks: []Task{
		{ID: "t-epic", Status: "backlog", Deps: []string{"t-slice"}, Updated: tm(1)},
		{ID: "t-slice", Status: "done", Closed: ptm(2)},
	}}
	ps := StaleDepProblems(idx, terminal, doneIDs)
	if len(ps) != 1 {
		t.Fatalf("want 1 problem, got %d: %+v", len(ps), ps)
	}
	if !strings.Contains(ps[0].Msg, "t-slice") {
		t.Errorf("message should name the stale dep t-slice: %q", ps[0].Msg)
	}
}
