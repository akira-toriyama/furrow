package core

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// The v5->v6 migration's pure half. Tasks are built with parked extras exactly
// as UnmarshalTask would leave them on a v5 shard (this binary has no Type or
// Parent field, so those keys arrive as extras).

func v6IDFor(taskID string) string { return "e-" + taskID[len("t-"):] }

func rawExtras(kv map[string]string) Extras {
	e := Extras{}
	for k, v := range kv {
		e[k] = json.RawMessage(v)
	}
	return e
}

func v6ClosedInDoneLane(t *Task) bool {
	return t.Closed != nil || t.Status == "done" || t.Status == "icebox"
}

func TestPlanV6EpicsBuildsTheEpicFromTheTask(t *testing.T) {
	created := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	closed := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	idx := &Index{Tasks: []Task{
		{ID: "t-open", Title: "open box", Status: "backlog", Labels: []string{"x"}, Repos: []string{"o/r"},
			Created: created, Updated: updated, extras: rawExtras(map[string]string{"type": `"epic"`})},
		{ID: "t-done", Title: "done box", Status: "done", Closed: &closed,
			Created: created, Updated: updated, extras: rawExtras(map[string]string{"type": `"epic"`})},
		// icebox: terminal but never stamped Closed — the epic closes at Updated.
		{ID: "t-ice", Title: "iced box", Status: "icebox",
			Created: created, Updated: updated, extras: rawExtras(map[string]string{"type": `"epic"`})},
		{ID: "t-task", Title: "plain", Status: "backlog", extras: rawExtras(map[string]string{"type": `"task"`})},
		{ID: "t-num", Title: "typed with a number", Status: "backlog", extras: rawExtras(map[string]string{"type": `7`})},
		{ID: "t-none", Title: "no type at all", Status: "backlog"},
	}}

	plans := PlanV6Epics(idx, v6IDFor, v6ClosedInDoneLane)
	if len(plans) != 3 {
		t.Fatalf("planned %d conversions, want 3: %+v", len(plans), plans)
	}

	open := plans[0]
	if open.TaskID != "t-open" || open.Epic.ID != "e-open" {
		t.Errorf("conversion = %q -> %q, want t-open -> e-open", open.TaskID, open.Epic.ID)
	}
	if open.Epic.Title != "open box" || !reflect.DeepEqual(open.Epic.Labels, []string{"x"}) ||
		!reflect.DeepEqual(open.Epic.Repos, []string{"o/r"}) {
		t.Errorf("epic did not carry the task's title/labels/repos: %+v", open.Epic)
	}
	if open.Epic.Goal != "" {
		t.Errorf("goal = %q, want empty (a human fills it, never a derivation)", open.Epic.Goal)
	}
	if open.Epic.Active {
		t.Error("a migrated epic must never arrive active")
	}
	if open.Epic.Body != "bodies/e-open.md" {
		t.Errorf("body = %q, want bodies/e-open.md", open.Epic.Body)
	}
	if !open.Epic.Created.Equal(created) || !open.Epic.Updated.Equal(updated) || open.Epic.Closed != nil {
		t.Errorf("open epic timestamps wrong: %+v", open.Epic)
	}

	if plans[1].Epic.Closed == nil || !plans[1].Epic.Closed.Equal(closed) {
		t.Errorf("done epic must close at the task's own Closed, got %+v", plans[1].Epic.Closed)
	}
	if plans[2].Epic.Closed == nil || !plans[2].Epic.Closed.Equal(updated) {
		t.Errorf("icebox epic (no Closed stamp) must close at Updated, got %+v", plans[2].Epic.Closed)
	}

	if len(idx.Tasks) != 6 || idx.Tasks[0].ExtraKeys() == nil {
		t.Error("PlanV6Epics mutated the index")
	}
}

func TestApplyV6RemovesRehomesAndConsumes(t *testing.T) {
	conv := map[string]string{"t-box": "e-box"}
	idx := &Index{Tasks: []Task{
		{ID: "t-box", Title: "the box", Status: "backlog", extras: rawExtras(map[string]string{"type": `"epic"`})},
		{ID: "t-kid", Title: "child", Status: "backlog", extras: rawExtras(map[string]string{"parent": `"t-box"`})},
		{ID: "t-both", Title: "typed child", Status: "backlog",
			extras: rawExtras(map[string]string{"type": `"task"`, "parent": `"t-box"`})},
		{ID: "t-str", Title: "parent of a plain task", Status: "backlog",
			extras: rawExtras(map[string]string{"parent": `"t-kid"`})},
		{ID: "t-raw", Title: "unreadable parent", Status: "backlog",
			extras: rawExtras(map[string]string{"parent": `{"id":"t-box"}`})},
		{ID: "t-for", Title: "custom type", Status: "backlog",
			extras: rawExtras(map[string]string{"type": `"milestone"`})},
		{ID: "t-set", Title: "epic already set", Status: "backlog", Epic: "e-other",
			extras: rawExtras(map[string]string{"parent": `"t-box"`})},
		{ID: "t-same", Title: "epic already equals the mapping", Status: "backlog", Epic: "e-box",
			extras: rawExtras(map[string]string{"parent": `"t-box"`})},
		{ID: "t-dep", Title: "waited on the box", Status: "backlog",
			Deps: []string{"t-box", "t-kid"}},
	}}

	ch := ApplyV6(idx, conv)

	ids := make([]string, 0, len(idx.Tasks))
	for _, tk := range idx.Tasks {
		ids = append(ids, tk.ID)
	}
	want := []string{"t-kid", "t-both", "t-str", "t-raw", "t-for", "t-set", "t-same", "t-dep"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("remaining tasks = %v, want %v (t-box converted away)", ids, want)
	}

	byID := map[string]*Task{}
	for i := range idx.Tasks {
		byID[idx.Tasks[i].ID] = &idx.Tasks[i]
	}
	if byID["t-kid"].Epic != "e-box" || byID["t-kid"].ExtraKeys() != nil {
		t.Errorf("t-kid: epic=%q extras=%v, want membership e-box and the parent key consumed",
			byID["t-kid"].Epic, byID["t-kid"].ExtraKeys())
	}
	if byID["t-both"].Epic != "e-box" || byID["t-both"].ExtraKeys() != nil {
		t.Errorf("t-both: epic=%q extras=%v, want both retired keys consumed",
			byID["t-both"].Epic, byID["t-both"].ExtraKeys())
	}
	if ch.Rehomed != 2 {
		t.Errorf("rehomed = %d, want 2 (t-kid, t-both)", ch.Rehomed)
	}

	// t-same: the edge agrees with the field — consumed, but NOT counted as a
	// re-home (nothing moved).
	if byID["t-same"].Epic != "e-box" || byID["t-same"].ExtraKeys() != nil {
		t.Errorf("t-same: epic=%q extras=%v, want the redundant parent consumed", byID["t-same"].Epic, byID["t-same"].ExtraKeys())
	}

	wantKept := []V6Kept{
		{TaskID: "t-str", Key: "parent", Value: "t-kid", Reason: "parent-not-epic"},
		{TaskID: "t-raw", Key: "parent", Reason: "not-a-string"},
		{TaskID: "t-for", Key: "type", Value: "milestone", Reason: "foreign-type"},
		{TaskID: "t-set", Key: "parent", Value: "t-box", Reason: "epic-already-set"},
	}
	if !reflect.DeepEqual(ch.Kept, wantKept) {
		t.Errorf("kept = %+v,\nwant %+v", ch.Kept, wantKept)
	}
	for _, id := range []string{"t-str", "t-raw", "t-for", "t-set"} {
		if byID[id].ExtraKeys() == nil {
			t.Errorf("%s: a kept key must stay parked in extras", id)
		}
	}
	if byID["t-set"].Epic != "e-other" {
		t.Errorf("t-set: epic=%q, want the existing e-other untouched (the field wins)", byID["t-set"].Epic)
	}

	// A dep onto the converted epic is dropped (an unresolvable dep would wedge
	// the task out of `next` forever); a dep onto a plain task survives.
	if !reflect.DeepEqual(byID["t-dep"].Deps, []string{"t-kid"}) {
		t.Errorf("t-dep deps = %v, want only t-kid (the dep on the box dropped)", byID["t-dep"].Deps)
	}
	if !reflect.DeepEqual(ch.DroppedDeps, []V6DroppedDep{{TaskID: "t-dep", DepID: "t-box", EpicID: "e-box"}}) {
		t.Errorf("dropped deps = %+v, want the one t-dep -> t-box edge", ch.DroppedDeps)
	}
}

func TestApplyV6Idempotent(t *testing.T) {
	conv := map[string]string{"t-box": "e-box"}
	idx := &Index{Tasks: []Task{
		{ID: "t-box", Status: "backlog", extras: rawExtras(map[string]string{"type": `"epic"`})},
		{ID: "t-kid", Status: "backlog", extras: rawExtras(map[string]string{"parent": `"t-box"`})},
	}}
	ApplyV6(idx, conv)

	before := make([]Task, len(idx.Tasks))
	copy(before, idx.Tasks)
	ch := ApplyV6(idx, conv)
	if ch.Rehomed != 0 || len(ch.Kept) != 0 {
		t.Errorf("second run reported changes: %+v", ch)
	}
	if !reflect.DeepEqual(idx.Tasks, before) {
		t.Error("second run mutated the index")
	}
}

func TestRewriteV6Links(t *testing.T) {
	conv := map[string]string{"t-box": "e-box", "t-two": "e-two"}
	cases := []struct {
		name string
		in   string
		want string
		n    int
	}{
		{"basic", "see [[t-box]] for the plan", "see [[e-box]] for the plan", 1},
		{"two on one line", "[[t-box]] then [[t-two]]", "[[e-box]] then [[e-two]]", 2},
		{"unconverted id untouched", "see [[t-kid]]", "see [[t-kid]]", 0},
		{"bare id is not a link", "progress on t-box today", "progress on t-box today", 0},
		{"fence is documentation", "before\n```\n[[t-box]]\n```\nafter [[t-box]]", "before\n```\n[[t-box]]\n```\nafter [[e-box]]", 1},
		{"tilde fence too", "~~~\n[[t-box]]\n~~~\n", "~~~\n[[t-box]]\n~~~\n", 0},
		{"inline code is documentation", "the `[[t-box]]` notation, but [[t-box]] is real", "the `[[t-box]]` notation, but [[e-box]] is real", 1},
		{"unterminated span code-quotes the tail", "a [[t-box]] then `broken [[t-box]]", "a [[e-box]] then `broken [[t-box]]", 1},
		{"trailing newline survives", "[[t-box]]\n", "[[e-box]]\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n := RewriteV6Links(tc.in, conv, "t-")
			if got != tc.want || n != tc.n {
				t.Errorf("RewriteV6Links(%q) = %q, %d; want %q, %d", tc.in, got, n, tc.want, tc.n)
			}
		})
	}
}

// The rewrite must agree with ExtractLinks about which mentions are REAL: after
// rewriting, the live links are exactly the mapped ids — and the documentation
// examples the extractor ignores are still ignored (untouched).
func TestRewriteV6LinksAgreesWithExtractLinks(t *testing.T) {
	conv := map[string]string{"t-box": "e-box"}
	body := "intro [[t-box]] and [[t-kid]]\n```\n[[t-box]] example\n```\nand `[[t-box]]` inline\n"
	re := LinkPattern("t-")

	got, n := RewriteV6Links(body, conv, "t-")
	if n != 1 {
		t.Fatalf("rewrote %d links, want exactly the one ExtractLinks sees", n)
	}
	if links := ExtractLinks(got, re); !reflect.DeepEqual(links, []string{"t-kid"}) {
		t.Errorf("t- links after rewrite = %v, want only the unconverted t-kid", links)
	}
	if links := ExtractLinks(got, LinkPattern("e-")); !reflect.DeepEqual(links, []string{"e-box"}) {
		t.Errorf("e- links after rewrite = %v, want the converted e-box", links)
	}
}
