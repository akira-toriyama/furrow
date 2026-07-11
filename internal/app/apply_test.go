package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// newSeededApp builds an App over a memstore with deterministic sequential ids
// (t-00001, t-00002, …) so directive links can be built against known ids.
func newSeededApp() *App {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	st.SeedSequentialIDs()
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk)
}

func bodyLink(id string) string {
	return "https://github.com/akira-toriyama/projects/blob/main/.furrow/bodies/" + id + ".md"
}

func TestParseDirectives(t *testing.T) {
	text := "Implements the thing.\n\n" +
		"SetStatus-task: " + bodyLink("t-0011") + " done\n" +
		"- SetStatus-task: t-0042 in-progress\n" +
		"> SetStatus-task: " + bodyLink("t-00007") + "\n" +
		"SetStatus-task:\n" + // no payload -> skipped
		"this is not a directive\n"

	ds := ParseDirectives(text)
	if len(ds) != 3 {
		t.Fatalf("expected 3 directives, got %d: %+v", len(ds), ds)
	}
	if ds[0].ID != "t-0011" || ds[0].Lane != "done" {
		t.Errorf("d0 = %+v, want id t-0011 lane done", ds[0])
	}
	if ds[1].ID != "t-0042" || ds[1].Lane != "in-progress" {
		t.Errorf("d1 = %+v, want id t-0042 lane in-progress (bare id + markdown bullet)", ds[1])
	}
	if ds[2].ID != "t-00007" || ds[2].Lane != "" {
		t.Errorf("d2 = %+v, want id t-00007 no lane (annotate-only)", ds[2])
	}
}

func TestApplyOnMergeMovesToLaneAndAnnotates(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("ship", AddOpts{Status: "ready"})

	res, err := a.ApplyDirectives("SetStatus-task: "+bodyLink(tk.ID)+" done", "furrow#42", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].Action != "moved" || res.Outcomes[0].To != "done" {
		t.Fatalf("expected one moved->done outcome, got %+v", res.Outcomes)
	}
	got, body, _ := a.Get(tk.ID)
	if got.Status != "done" || got.Closed == nil {
		t.Errorf("merge with done should set lane=done + Closed: %+v", got)
	}
	if !strings.Contains(body, "furrow#42") || !strings.Contains(body, "merged") {
		t.Errorf("body should record the merged PR, got %q", body)
	}
}

func TestApplyOnMergeNoLaneAnnotatesOnly(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("refd", AddOpts{Status: "ready"})

	res, err := a.ApplyDirectives("SetStatus-task: "+bodyLink(tk.ID), "furrow#7", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcomes[0].Action != "annotated" {
		t.Fatalf("no-lane merge should annotate only, got %+v", res.Outcomes[0])
	}
	got, _, _ := a.Get(tk.ID)
	if got.Status != "ready" {
		t.Errorf("status must be unchanged on a no-lane (referenced) merge, got %q", got.Status)
	}
}

func TestApplyOnOpenNudgesToInProgressNotDone(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("wip", AddOpts{Status: "ready"})

	// Directive names `done`, but on OPEN it must only reach in-progress.
	res, err := a.ApplyDirectives("SetStatus-task: "+bodyLink(tk.ID)+" done", "furrow#9", OnOpen, "")
	if err != nil {
		t.Fatal(err)
	}
	got, body, _ := a.Get(tk.ID)
	if got.Status != "in-progress" {
		t.Errorf("open should nudge to in-progress, not the merge target; got %q", got.Status)
	}
	if got.Closed != nil {
		t.Errorf("open must never stamp Closed: %v", got.Closed)
	}
	if res.Outcomes[0].To != "in-progress" {
		t.Errorf("outcome should report in-progress, got %+v", res.Outcomes[0])
	}
	if !strings.Contains(body, "opened") {
		t.Errorf("body should record the opened PR, got %q", body)
	}
}

func TestApplyOnOpenSkipsTerminalTask(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("already done", AddOpts{Status: "ready"})
	a.Done(tk.ID)

	res, err := a.ApplyDirectives("SetStatus-task: "+bodyLink(tk.ID)+" done", "furrow#11", OnOpen, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := a.Get(tk.ID)
	if got.Status != "done" {
		t.Errorf("opening a PR must not move a terminal task out of done, got %q", got.Status)
	}
	// it still records the event (annotation), but does not move.
	if res.Outcomes[0].Action == "moved" {
		t.Errorf("terminal task should not be moved on open, got %+v", res.Outcomes[0])
	}
}

func TestApplyUnknownIDAndLaneAreNonFatal(t *testing.T) {
	a := newSeededApp()
	good, _ := a.Add("good", AddOpts{Status: "ready"})

	text := "SetStatus-task: " + bodyLink("t-99999") + " done\n" + // unknown id -> NotFound (1)
		"SetStatus-task: " + bodyLink(good.ID) + " ghostlane\n" + // unknown lane -> Validation (2)
		"SetStatus-task: " + bodyLink(good.ID) + " done\n" // valid -> applied

	res, err := a.ApplyDirectives(text, "furrow#1", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outcomes) != 3 {
		t.Fatalf("want 3 outcomes, got %d", len(res.Outcomes))
	}
	if res.Outcomes[0].Code != 1 {
		t.Errorf("unknown id should be code 1 (not-found), got %+v", res.Outcomes[0])
	}
	if res.Outcomes[1].Code != 2 {
		t.Errorf("unknown lane should be code 2 (validation), got %+v", res.Outcomes[1])
	}
	// the unknown-lane directive carries the configured lanes as candidates, so a
	// batch consumer branches on the array not the prose (t-bec7).
	if !reflect.DeepEqual(res.Outcomes[1].Candidates, a.Cfg.Lanes) {
		t.Errorf("unknown-lane outcome should carry lane candidates, got %v", res.Outcomes[1].Candidates)
	}
	if res.Outcomes[2].Action != "moved" || res.Outcomes[2].To != "done" {
		t.Errorf("the valid directive should still apply, got %+v", res.Outcomes[2])
	}
	if res.WorstCode() != 2 {
		t.Errorf("WorstCode should be 2 (validation), got %d", res.WorstCode())
	}
	// the valid one really did move despite the earlier failures.
	if got, _, _ := a.Get(good.ID); got.Status != "done" {
		t.Errorf("valid directive should have closed the task, got %q", got.Status)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("idem", AddOpts{Status: "ready"})
	directive := "SetStatus-task: " + bodyLink(tk.ID) + " done"

	if _, err := a.ApplyDirectives(directive, "furrow#5", OnMerge, ""); err != nil {
		t.Fatal(err)
	}
	_, body1, _ := a.Get(tk.ID)

	res2, err := a.ApplyDirectives(directive, "furrow#5", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	_, body2, _ := a.Get(tk.ID)

	if body1 != body2 {
		t.Errorf("re-running the same merge must not duplicate the annotation:\n1: %q\n2: %q", body1, body2)
	}
	// Second run: already in the target lane → no move (no `updated` churn) and
	// the annotation already exists → a true no-op reported as "skipped".
	if res2.Outcomes[0].Action != "skipped" {
		t.Errorf("idempotent re-run should skip the move, got action=%q (%+v)", res2.Outcomes[0].Action, res2.Outcomes[0])
	}
	if res2.Outcomes[0].Note != "" {
		t.Errorf("idempotent re-run should add no new note, got %+v", res2.Outcomes[0])
	}
}

func TestApplyNoDirectivesIsCleanNoop(t *testing.T) {
	a := newSeededApp()
	res, err := a.ApplyDirectives("just a normal PR body, nothing to see", "furrow#3", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outcomes) != 0 || res.WorstCode() != 0 {
		t.Errorf("no directives should yield an empty, ok result, got %+v", res)
	}
}
