package app

import (
	"reflect"
	"strings"
	"testing"
)

// newSeededApp builds an App over a memstore with deterministic sequential ids
// (t-00001, t-00002, …) so directive links can be built against known ids.
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

	ds := parseDirectives(text)
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

// TestPreviewDirectivesProjectsWithoutWriting pins the dry-run contract: the
// preview's outcomes are the real apply's outcomes (id, lane, action, to, note,
// error, code, candidates — everything but dry_run), and the store is untouched
// by the preview. Both halves on one fixture: the projection only proves
// something because the same text then applies for real and matches.
func TestPreviewDirectivesProjectsWithoutWriting(t *testing.T) {
	a := newSeededApp()
	good, _ := a.Add("ship", AddOpts{Status: "ready"})
	same, _ := a.Add("already there", AddOpts{Status: "done"})
	text := "SetStatus-task: " + bodyLink(good.ID) + " done\n" +
		"SetStatus-task: " + bodyLink(same.ID) + " done\n" + // already in lane: no change
		"SetStatus-task: " + bodyLink("t-99999") + " done\n" + // unknown id
		"SetStatus-task: " + bodyLink(good.ID) + " ghostlane\n" // unknown lane

	before, beforeBody, _ := a.Get(good.ID)
	preview, err := a.PreviewDirectives(text, "furrow#42", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun {
		t.Errorf("preview must be marked dry_run, got %+v", preview)
	}
	after, afterBody, _ := a.Get(good.ID)
	if !reflect.DeepEqual(before, after) || beforeBody != afterBody {
		t.Errorf("preview wrote to the store:\nbefore %+v %q\nafter  %+v %q", before, beforeBody, after, afterBody)
	}
	if preview.WorstCode() == 0 {
		t.Errorf("preview must carry the validation exit code, got outcomes %+v", preview.Outcomes)
	}

	real, err := a.ApplyDirectives(text, "furrow#42", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	real.DryRun = true
	if !reflect.DeepEqual(preview, real) {
		t.Errorf("preview should equal the real apply's report:\npreview %+v\nreal    %+v", preview, real)
	}
	if got, _, _ := a.Get(good.ID); got.Status != "done" {
		t.Errorf("the real apply after the preview should still move, got %q", got.Status)
	}
}

// A second preview after the real apply projects "no change": the lane is
// already reached and the annotation line is already in the body.
func TestPreviewDirectivesIsIdempotentLikeApply(t *testing.T) {
	a := newSeededApp()
	tk, _ := a.Add("ship", AddOpts{Status: "ready"})
	text := "SetStatus-task: " + bodyLink(tk.ID) + " done"
	if _, err := a.ApplyDirectives(text, "furrow#42", OnMerge, ""); err != nil {
		t.Fatal(err)
	}
	preview, err := a.PreviewDirectives(text, "furrow#42", OnMerge, "")
	if err != nil {
		t.Fatal(err)
	}
	if o := preview.Outcomes[0]; o.Action != "skipped" || o.To != "done" || o.Note != "" {
		t.Errorf("re-preview should be a no-op projection, got %+v", o)
	}
}

// TestApplyLaneIsTheMergeTarget pins what a directive's lane means: the task's
// merge target. Open nudges only for a target that is the task's work (the done
// lane, or a non-terminal lane at or after the open lane), merge never reopens
// a terminal task (a terminal target still applies), and every deliberate
// non-move says why in Kept. Each row runs
// the preview first and then the real apply on the same store, so the dry-run
// is held to the identical decision. The two measured accidents (t-9vtf) are
// the "backlog footer" and "done task" rows.
func TestApplyLaneIsTheMergeTarget(t *testing.T) {
	cases := []struct {
		name     string
		from     string // the task's lane when the event fires
		lane     string // the footer's lane
		mode     ApplyMode
		want     string // the task's lane afterwards
		action   string
		kept     string
		annotate string // the body line the event records
	}{
		{"open with a backlog footer keeps a backlog task", "backlog", "backlog", OnOpen, "backlog", "annotated", KeptTargetBeforeOpenLane, "- 🚧 `x#1` opened"},
		{"open with an inbox footer keeps a ready task", "ready", "inbox", OnOpen, "ready", "annotated", KeptTargetBeforeOpenLane, "- 🚧 `x#1` opened"},
		{"open with an icebox footer keeps a ready task", "ready", "icebox", OnOpen, "ready", "annotated", KeptTargetParked, "- 🚧 `x#1` opened"},
		{"open with a waiting footer keeps a backlog task", "backlog", "waiting", OnOpen, "backlog", "annotated", KeptTargetParked, "- 🚧 `x#1` opened"},
		{"open with a done footer nudges", "ready", "done", OnOpen, "in-progress", "moved", "", "- 🚧 `x#1` opened"},
		{"open with an in-progress footer nudges", "backlog", "in-progress", OnOpen, "in-progress", "moved", "", "- 🚧 `x#1` opened"},
		{"open never nudges a done task", "done", "done", OnOpen, "done", "annotated", KeptTaskTerminal, "- 🚧 `x#1` opened"},
		{"merge with an in-progress footer keeps a done task", "done", "in-progress", OnMerge, "done", "annotated", KeptTaskTerminal, "- 🔗 `x#1` merged"},
		{"merge with a backlog footer keeps an icebox task", "icebox", "backlog", OnMerge, "icebox", "annotated", KeptTaskTerminal, "- 🔗 `x#1` merged"},
		{"merge with a done footer closes an icebox task", "icebox", "done", OnMerge, "done", "moved", "", "- ✅ `x#1` merged → `done`"},
		{"merge with a done footer closes a waiting task", "waiting", "done", OnMerge, "done", "moved", "", "- ✅ `x#1` merged → `done`"},
		{"merge with an icebox footer parks a done task", "done", "icebox", OnMerge, "icebox", "moved", "", "- ✅ `x#1` merged → `icebox`"},
		{"merge with a done footer closes an in-progress task", "in-progress", "done", OnMerge, "done", "moved", "", "- ✅ `x#1` merged → `done`"},
		{"merge with a backlog footer moves an in-progress task", "in-progress", "backlog", OnMerge, "backlog", "moved", "", "- ✅ `x#1` merged → `backlog`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newSeededApp()
			tk, err := a.Add("task", AddOpts{Status: c.from})
			if c.from == "done" {
				_, err = a.Done(tk.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			text := "SetStatus-task: " + bodyLink(tk.ID) + " " + c.lane

			before, beforeBody, _ := a.Get(tk.ID)
			preview, err := a.PreviewDirectives(text, "x#1", c.mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if after, afterBody, _ := a.Get(tk.ID); !reflect.DeepEqual(before, after) || beforeBody != afterBody {
				t.Fatalf("the preview wrote to the store")
			}
			res, err := a.ApplyDirectives(text, "x#1", c.mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if res.DryRun = true; !reflect.DeepEqual(preview, res) {
				t.Errorf("the preview projected a different decision:\npreview %+v\nreal    %+v", preview.Outcomes, res.Outcomes)
			}

			o := res.Outcomes[0]
			if o.Action != c.action || o.Kept != c.kept || o.To != c.want || o.Code != 0 {
				t.Errorf("outcome = %+v; want action %q, kept %q, to %q, code 0", o, c.action, c.kept, c.want)
			}
			if res.WorstCode() != 0 {
				t.Errorf("a kept directive is not a failure: WorstCode = %d", res.WorstCode())
			}
			got, body, _ := a.Get(tk.ID)
			if got.Status != c.want {
				t.Errorf("status = %q, want %q", got.Status, c.want)
			}
			if c.want == "done" && got.Closed == nil {
				t.Errorf("a task left in done must keep its closed stamp: %+v", got)
			}
			if !strings.Contains(body, c.annotate) {
				t.Errorf("body should record %q, got %q", c.annotate, body)
			}

			// The re-run of the same event writes nothing and still says why.
			again, err := a.ApplyDirectives(text, "x#1", c.mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if o := again.Outcomes[0]; o.Action != "skipped" || o.Kept != c.kept || o.Note != "" {
				t.Errorf("re-run outcome = %+v; want skipped, kept %q, no note", o, c.kept)
			}
		})
	}
}
