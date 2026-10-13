package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/config"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// TestSetCombinedEdit pins t-kx76 (e): `set` applies lane+value+effort+labels in
// one write, honors clear/rm, rejects an empty change, and validates the lane
// like Move (unknown → candidates).
func TestSetCombinedEdit(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("triage me", AddOpts{Status: "inbox"})

	got, _, _, err := a.Set(tk.ID, SetOpts{Status: ptr("ready"), Value: ptr(4), Effort: ptr(2), AddLabels: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.Value == nil || *got.Value != 4 || got.Effort == nil || *got.Effort != 2 || !contains(got.Labels, "bug") {
		t.Fatalf("set should apply every edit at once: %+v", got)
	}

	got, _, _, err = a.Set(tk.ID, SetOpts{ClearValue: true, RmLabels: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != nil || contains(got.Labels, "bug") {
		t.Errorf("set --clear-value/--rm-label should unset: %+v", got)
	}

	if _, _, _, err := a.Set(tk.ID, SetOpts{}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty set should be a validation error, got %v", err)
	}
	if _, _, _, err := a.Set(tk.ID, SetOpts{Status: ptr("ghost")}); !hasLaneCandidates(a, err) {
		t.Errorf("set to an unknown lane should carry lane candidates, got %v", err)
	}
}

// TestDepsVariadicBatch pins t-kx76 (e): AddDeps/RemoveDeps apply several deps in
// one write, and a bad dep aborts the whole batch (no partial add).
func TestDepsVariadicBatch(t *testing.T) {
	a := newApp()
	base, _ := a.Add("base", AddOpts{})
	b1, _ := a.Add("b1", AddOpts{})
	b2, _ := a.Add("b2", AddOpts{})

	got, err := a.AddDeps(base.ID, []string{b1.ID, b2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got.Deps, b1.ID) || !contains(got.Deps, b2.ID) {
		t.Fatalf("AddDeps should add both: %v", got.Deps)
	}

	b3, _ := a.Add("b3", AddOpts{})
	if _, err := a.AddDeps(base.ID, []string{b3.ID, "t-nope"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Fatalf("AddDeps with a missing dep should be a validation error, got %v", err)
	}
	after, _, _ := a.Get(base.ID)
	if contains(after.Deps, b3.ID) {
		t.Errorf("a failed batch must not partially add b3: %v", after.Deps)
	}

	got, err = a.RemoveDeps(base.ID, []string{b1.ID, b2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deps) != 0 {
		t.Errorf("RemoveDeps should drop both: %v", got.Deps)
	}
}

// TestChecklistEditSurface pins t-abj3 (c-extra): add --check seeds items, and
// RewordCheck/RemoveCheck edit them (out-of-range / empty are validation errors).
// The blank `--check` value it originally pinned as "silently dropped" is now
// exit 2 (t-3gy1): that drop existed only to mirror `check --add`, which t-fr3e
// turned into exit 2, so the drop had outlived its own rationale.
func TestChecklistEditSurface(t *testing.T) {
	a := newApp()
	if _, err := a.Add("blank step", AddOpts{Checklist: UncheckedItems([]string{"one", "", "two"})}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("a blank --check must be exit 2, like check --add, got %v", err)
	}
	tk, err := a.Add("has steps", AddOpts{Checklist: UncheckedItems([]string{"one", "two", "three"})})
	if err != nil {
		t.Fatal(err)
	}
	if len(tk.Checklist) != 3 {
		t.Fatalf("add --check should seed 3 items: %+v", tk.Checklist)
	}

	got, err := a.RewordCheck(tk.ID, 1, "TWO")
	if err != nil {
		t.Fatal(err)
	}
	if got.Checklist[1].Text != "TWO" {
		t.Errorf("reword should replace item 1's text: %+v", got.Checklist)
	}

	got, err = a.RemoveCheck(tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Checklist) != 2 || got.Checklist[0].Text != "TWO" {
		t.Errorf("remove should delete item 0: %+v", got.Checklist)
	}

	if _, err := a.RemoveCheck(tk.ID, 9); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("out-of-range remove should be a validation error, got %v", err)
	}
	if _, err := a.RewordCheck(tk.ID, 0, "  "); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty reword should be a validation error, got %v", err)
	}
}

func TestCheckTogglesChecklist(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("with steps", AddOpts{})
	a.AddCheck(tk.ID, "step one")
	a.AddCheck(tk.ID, "step two")
	got, _ := a.Check(tk.ID, 1, true)
	if len(got.Checklist) != 2 || !got.Checklist[1].Done || got.Checklist[0].Done {
		t.Errorf("check should toggle only item 1: %+v", got.Checklist)
	}
}

func TestLabelsRequiredEnforced(t *testing.T) {
	a, _ := newAppWith(withCfg(func(c *config.Config) { c.LabelsRequired = true }))

	// add without a label -> validation error.
	if _, err := a.Add("no label", AddOpts{Status: "ready"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add without a label should be a validation error when required, got %v", err)
	}
	// add with a label -> ok.
	if _, err := a.Add("with label", AddOpts{Status: "ready", Labels: []string{"furrow"}}); err != nil {
		t.Errorf("add with a label should succeed, got %v", err)
	}

	// lint flags a label-less task (injected directly, with a body so the only
	// problem is the missing label).
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-0099", Title: "ghost", Status: "ready", Body: core.BodyPath("t-0099")})
	a.Store.Save(idx)
	a.Store.SaveBody("t-0099", "# ghost\n")
	ps, _ := a.Lint()
	var found bool
	for _, p := range ps {
		if p.ID == "t-0099" && p.Severity == core.SevError && strings.Contains(p.Msg, "label") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 as label-less, got %+v", ps)
	}

	// default config (not required) accepts a label-less add.
	a2 := newApp()
	if _, err := a2.Add("fine", AddOpts{Status: "ready"}); err != nil {
		t.Errorf("label-less add should succeed when not required, got %v", err)
	}
}

func TestCheckOutOfRangeIsValidationError(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("steps", AddOpts{})
	a.AddCheck(tk.ID, "only item") // index 0 valid; 1+ and negatives invalid
	if _, err := a.Check(tk.ID, 5, true); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("out-of-range index should be a validation error, got %v", err)
	}
	if _, err := a.Check(tk.ID, -1, true); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("negative index should be a validation error, got %v", err)
	}
	// a missing id is still NotFound, not validation.
	if _, err := a.Check("t-9999", 0, true); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("missing id should be not-found, got %v", err)
	}
	if _, err := a.Check(tk.ID, 0, true); err != nil {
		t.Errorf("in-range check should succeed, got %v", err)
	}
}

func TestRelabelAddsRemovesIdempotently(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("x", AddOpts{Labels: []string{"chord", "shared"}})

	// Add one label, remove another in a single call.
	got, err := a.Relabel(tk.ID, []string{"sill"}, []string{"chord"})
	if err != nil {
		t.Fatalf("Relabel: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("labels = %q, want %q", join, "shared,sill")
	}

	// Adding an existing label is a no-op (no duplicate).
	got, err = a.Relabel(tk.ID, []string{"sill"}, nil)
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("idempotent add changed labels: %q", join)
	}

	// Removing an absent label is a no-op (not an error).
	got, err = a.Relabel(tk.ID, nil, []string{"nope"})
	if err != nil {
		t.Fatalf("removing an absent label should be a no-op, got %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "shared,sill" {
		t.Errorf("absent remove changed labels: %q", join)
	}

	// No flags at all is a bad-usage validation error (never a silent no-op).
	if _, err := a.Relabel(tk.ID, nil, nil); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("relabel with no add/remove should be a validation error, got %v", err)
	}

	// Unknown id is NotFound.
	if _, err := a.Relabel("t-9999", []string{"x"}, nil); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("relabel on unknown id should be NotFound, got %v", err)
	}
}

func TestRerefAddsRemovesIdempotentlyKeepingOrder(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("x", AddOpts{Refs: []string{"docs/a.md:10", "https://example.com/b"}})

	// Add one ref, remove another in a single call. Refs are a user-ordered
	// SEQUENCE (the marshaller does not sort them, unlike labels): survivors
	// keep their order and adds append at the end.
	got, err := a.Reref(tk.ID, []string{"internal/cli/root.go:42"}, []string{"docs/a.md:10"})
	if err != nil {
		t.Fatalf("Reref: %v", err)
	}
	want := "https://example.com/b,internal/cli/root.go:42"
	if join := strings.Join(got.Refs, ","); join != want {
		t.Errorf("refs = %q, want %q", join, want)
	}

	// Adding an existing ref is a no-op (no duplicate, order unchanged).
	got, err = a.Reref(tk.ID, []string{"https://example.com/b"}, nil)
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if join := strings.Join(got.Refs, ","); join != want {
		t.Errorf("idempotent add changed refs: %q", join)
	}

	// Removing an absent ref is a no-op (not an error).
	got, err = a.Reref(tk.ID, nil, []string{"nope.md:1"})
	if err != nil {
		t.Fatalf("removing an absent ref should be a no-op, got %v", err)
	}
	if join := strings.Join(got.Refs, ","); join != want {
		t.Errorf("absent remove changed refs: %q", join)
	}

	// No flags at all is a bad-usage validation error (never a silent no-op).
	if _, err := a.Reref(tk.ID, nil, nil); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("reref with no add/rm should be a validation error, got %v", err)
	}

	// Unknown id is NotFound.
	if _, err := a.Reref("t-9999", []string{"x.md:1"}, nil); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("reref on unknown id should be NotFound, got %v", err)
	}
}

func TestRelabelRespectsLabelsRequired(t *testing.T) {
	a, _ := newAppWith(withCfg(func(c *config.Config) { c.LabelsRequired = true }))
	tk, err := a.Add("x", AddOpts{Labels: []string{"only"}})
	if err != nil {
		t.Fatal(err)
	}

	// Removing the last label is rejected when labels are required.
	if _, err := a.Relabel(tk.ID, nil, []string{"only"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("removing the last required label should be a validation error, got %v", err)
	}

	// Swapping (add a new one while removing the old) is fine: the result is non-empty.
	got, err := a.Relabel(tk.ID, []string{"new"}, []string{"only"})
	if err != nil {
		t.Fatalf("swap relabel: %v", err)
	}
	if join := strings.Join(got.Labels, ","); join != "new" {
		t.Errorf("labels = %q, want %q", join, "new")
	}
}

func TestSetValueAndEffort(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("estimate me", AddOpts{})
	if tk.Value != nil || tk.Effort != nil {
		t.Fatalf("a fresh task must have unset value/effort: %+v", tk)
	}

	got, err := a.SetValue(tk.ID, ptr(4))
	if err != nil {
		t.Fatal(err)
	}
	if got.Value == nil || *got.Value != 4 || got.Effort != nil {
		t.Errorf("after SetValue(4): value=%v effort=%v", got.Value, got.Effort)
	}

	got, err = a.SetEffort(tk.ID, ptr(2))
	if err != nil {
		t.Fatal(err)
	}
	if got.Value == nil || *got.Value != 4 || got.Effort == nil || *got.Effort != 2 {
		t.Errorf("after SetEffort(2): value=%v effort=%v", got.Value, got.Effort)
	}
	if roi := got.ROI(); roi != 2 {
		t.Errorf("ROI = %v, want 2", roi)
	}

	// nil clears the estimate (back to unset — intake friction stays zero).
	got, err = a.SetValue(tk.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != nil {
		t.Errorf("SetValue(nil) should clear value, got %v", got.Value)
	}
}

func TestRetitle(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("old title", AddOpts{}) // body seeded "# old title\n"

	// The shard title updates and the body's heading is kept in step; a stray
	// title is trimmed to match SetTitle.
	got, err := a.Retitle(tk.ID, "  new title  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" {
		t.Errorf("shard title = %q, want %q", got.Title, "new title")
	}
	if body, _ := a.Store.LoadBody(tk.ID); body != "# new title\n" {
		t.Errorf("body heading not synced, got %q", body)
	}

	// A body whose first line is not an H1 keeps its prose; only the shard moves.
	if err := a.Store.SaveBody(tk.ID, "no heading here\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Retitle(tk.ID, "second title"); err != nil {
		t.Fatal(err)
	}
	got2, _, err := a.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Title != "second title" {
		t.Errorf("shard title = %q, want %q", got2.Title, "second title")
	}
	if body, _ := a.Store.LoadBody(tk.ID); body != "no heading here\n" {
		t.Errorf("headingless body should be untouched, got %q", body)
	}

	// An empty title is a validation error (delegated to SetTitle).
	if _, err := a.Retitle(tk.ID, "   "); err == nil {
		t.Error("empty title should be a validation error")
	}
}

// A retitle to the title the shard already holds, after the body's heading
// drifted (a hand edit), rewrites the heading — prose — and so advances
// `updated`, like every other body write (t-wdm8: it used to leave the clock
// alone because the shard's bytes never moved). The same call with the heading
// already in step is the no-op it always was.
func TestRetitleStampsUpdatedWhenOnlyTheHeadingMoves(t *testing.T) {
	a := newApp()
	clk := a.Clock.(*fixedClock)
	tk, _ := a.Add("same", AddOpts{})
	if err := a.Store.SaveBody(tk.ID, "# stale heading\n\nprose\n"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Hour)
	got, err := a.Retitle(tk.ID, "same")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := a.Store.LoadBody(tk.ID); body != "# same\n\nprose\n" {
		t.Errorf("heading not synced: %q", body)
	}
	if !got.Updated.Equal(clk.t) {
		t.Errorf("updated = %v, want %v — a heading rewrite is prose and must stamp", got.Updated, clk.t)
	}

	clk.t = clk.t.Add(time.Hour)
	again, err := a.Retitle(tk.ID, "same")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Updated.Equal(got.Updated) {
		t.Errorf("a retitle that changed nothing advanced updated to %v", again.Updated)
	}
}

func TestRetitleHeading(t *testing.T) {
	cases := []struct {
		name, body, title, want string
		changed                 bool
	}{
		{"replace h1", "# old\nbody\n", "new", "# new\nbody\n", true},
		{"noop when identical", "# same\n", "same", "# same\n", false},
		{"seed empty body", "", "seed", "# seed\n", true},
		{"seed whitespace body", "  \n\n", "seed", "# seed\n", true},
		{"headingless prose untouched", "just prose\n# later\n", "x", "just prose\n# later\n", false},
		{"h2 is not an h1", "## sub\n", "x", "## sub\n", false},
		{"hash without space is not a heading", "#foo\n", "x", "#foo\n", false},
		// A generated occurrence opens with its back-link (successorBody); the
		// heading is the first line after it, and a retitle must still reach it.
		{"a successor's back-link sits above the h1", "previous: [[t-abc1]]\n\n# old\ntail\n", "new", "previous: [[t-abc1]]\n\n# new\ntail\n", true},
		{"back-link over headingless prose is untouched", "previous: [[t-abc1]]\n\nprose\n", "x", "previous: [[t-abc1]]\n\nprose\n", false},
		{"an operator's bare link is not a back-link", "[[t-abc1]] came from here\n# old\n", "x", "[[t-abc1]] came from here\n# old\n", false},
		{"leading blank lines then h1", "\n\n# old\ntail\n", "new", "\n\n# new\ntail\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := retitleHeading(c.body, c.title)
			if got != c.want || changed != c.changed {
				t.Errorf("retitleHeading(%q, %q) = (%q, %v), want (%q, %v)", c.body, c.title, got, changed, c.want, c.changed)
			}
		})
	}
}
