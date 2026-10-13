package app

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

var idRe = regexp.MustCompile(`^t-[0-9a-z]{5}$`)

func TestAddAssignsRandomIDAndSparsePriority(t *testing.T) {
	a := newApp()
	t1, err := a.Add("first", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !idRe.MatchString(t1.ID) || t1.Status != "inbox" || t1.Priority != 100 {
		t.Errorf("first task wrong: %+v", t1)
	}
	t2, _ := a.Add("second", AddOpts{})
	if t2.ID == t1.ID || t2.Priority != 110 { // distinct id, same lane -> +step
		t.Errorf("second task should get a distinct id + priority 110: %+v", t2)
	}
	if t2.Body != "bodies/"+t2.ID+".md" {
		t.Errorf("body path should match the id: %q", t2.Body)
	}
	if body, _ := a.Store.LoadBody(t2.ID); body != "# second\n" {
		t.Errorf("body should seed a heading, got %q", body)
	}
}

func TestAddGeneratesUniqueRandomIDs(t *testing.T) {
	a := newApp() // random (unseeded) memstore
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tk, err := a.Add("task", AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if !idRe.MatchString(tk.ID) {
			t.Fatalf("id %q does not match the random pattern", tk.ID)
		}
		if seen[tk.ID] {
			t.Fatalf("Add produced a duplicate id: %q (uniqueID retry should prevent this)", tk.ID)
		}
		seen[tk.ID] = true
	}
	// the whole index is internally consistent (no duplicate-id lint error).
	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		if strings.Contains(p.Msg, "duplicate id") {
			t.Errorf("unexpected duplicate-id problem: %+v", p)
		}
	}
}

func TestAddManyGeneratesUniqueIDs(t *testing.T) {
	a := newApp() // random (unseeded) memstore
	specs := make([]AddSpec, 300)
	for i := range specs {
		specs[i] = AddSpec{Title: "batch", AddOpts: AddOpts{Status: "ready"}}
	}
	created, err := a.AddMany(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != len(specs) {
		t.Fatalf("AddMany created %d tasks, want %d", len(created), len(specs))
	}
	// every id in one batch must be distinct (uniqueID checks the accumulating index).
	seen := map[string]bool{}
	for _, tk := range created {
		if !idRe.MatchString(tk.ID) {
			t.Errorf("id %q does not match the random pattern", tk.ID)
		}
		if seen[tk.ID] {
			t.Fatalf("AddMany produced a duplicate id within one batch: %q", tk.ID)
		}
		seen[tk.ID] = true
	}
}

// TestAddManyMatchesSingleAdd guards the bulk path against silently diverging
// from a single `add`: the shared --value/--effort flags must land on every
// task (help promises "the shared flags apply to every task"), and each returned
// task must be canonicalized so bulk-add output deep-equals a subsequent read
// (no `null` slices where single-add emits `[]`). Regression for t-adx9.
func TestAddManyMatchesSingleAdd(t *testing.T) {
	a := newApp()
	specs := []AddSpec{
		{Title: "alpha", AddOpts: AddOpts{Value: ptr(3), Effort: ptr(2)}},
		{Title: "beta", AddOpts: AddOpts{Value: ptr(4), Effort: ptr(1)}},
	}
	created, err := a.AddMany(specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != len(specs) {
		t.Fatalf("AddMany created %d tasks, want %d", len(created), len(specs))
	}
	for i, tk := range created {
		// (1) value/effort must be carried, exactly as single Add does.
		wantV, wantE := *specs[i].Value, *specs[i].Effort
		if tk.Value == nil || *tk.Value != wantV || tk.Effort == nil || *tk.Effort != wantE {
			t.Errorf("task %d dropped estimate: value=%v effort=%v, want %d/%d",
				i, tk.Value, tk.Effort, wantV, wantE)
		}
		// (2) []-not-null: a returned nil slice would marshal to `null`, breaking
		// the documented invariant and diverging from a subsequent ls.
		if tk.Labels == nil || tk.Repos == nil || tk.Deps == nil || tk.Refs == nil || tk.Checklist == nil {
			t.Errorf("task %d returned a nil slice (must be []): %+v", i, tk)
		}
		// (3) the strong parity check: the returned task equals what a fresh read
		// returns, field for field — so `add --stdin --json` deep-equals `ls`.
		got, _, err := a.Get(tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tk, *got) {
			t.Errorf("task %d: AddMany return diverges from a read:\n add=%+v\n get=%+v", i, tk, *got)
		}
	}
}

// TestAddManyCarriesAndResolvesEpic guards the `epic` field across the same
// bulk-vs-single parity the --type field needed: `add --stdin -e <ref>` must
// RESOLVE the reference exactly as single Add does (storing the raw ref would
// file the task under a box that does not exist, visible only to lint), an
// epic-less spec must stay epic-less, and an unknown epic must fail the whole
// batch up front rather than being silently created.
func TestAddManyCarriesAndResolvesEpic(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "the travel box", EpicAddOpts{})

	created, err := a.AddMany([]AddSpec{
		// Deliberately a TITLE SUBSTRING, not the id: that is what makes this a
		// resolution test rather than an assignment test.
		{Title: "filed", AddOpts: AddOpts{Epic: "travel"}},
		{Title: "unfiled", AddOpts: AddOpts{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created[0].Epic != box {
		t.Errorf("AddMany stored the raw reference instead of resolving it: got %q, want %q", created[0].Epic, box)
	}
	if created[1].Epic != "" {
		t.Errorf("an epic-less spec must stay epic-less, got %q", created[1].Epic)
	}
	// The membership survives a fresh read (bulk output == a later ls).
	got, _, err := a.Get(created[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Epic != box {
		t.Errorf("persisted epic diverges from a read: got %q, want %q", got.Epic, box)
	}
	// An unknown epic fails the whole batch up front (exit 2), like single Add.
	if _, err := a.AddMany([]AddSpec{{Title: "bad", AddOpts: AddOpts{Epic: "no-such-box"}}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("AddMany with an unknown epic should be a validation error, got %v", err)
	}
}

func TestAddRejectsUnknownLaneAndEmptyTitle(t *testing.T) {
	a := newApp()
	if _, err := a.Add("x", AddOpts{Status: "ghost"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown lane should be a validation error, got %v", err)
	}
	if _, err := a.Add("   ", AddOpts{}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty title should be a validation error, got %v", err)
	}
}

// TestAddAndRetitleFlattenTitle pins that a title's interior newline/control
// characters are flattened before they reach the body's "# " heading —
// otherwise a title like "Fix bug\n## Injected" fabricates a second heading in
// the body markdown. Regression for the title-injection audit (glyph #61
// Flatten class).
func TestAddAndRetitleFlattenTitle(t *testing.T) {
	a := newApp()
	tk, err := a.Add("Fix bug\n## Injected", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "Fix bug ## Injected" {
		t.Errorf("Add must flatten the title to one line, got %q", tk.Title)
	}
	if body, _ := a.Store.LoadBody(tk.ID); body != "# Fix bug ## Injected\n" {
		t.Errorf("the body heading must not gain a fabricated line, got %q", body)
	}

	// retitle re-splices the title into the H1 heading, so it must flatten too.
	rt, err := a.Retitle(tk.ID, "New\ntitle")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Title != "New title" {
		t.Errorf("Retitle must flatten the title, got %q", rt.Title)
	}
	if body, _ := a.Store.LoadBody(tk.ID); body != "# New title\n" {
		t.Errorf("retitle must keep a single H1 heading, got %q", body)
	}
}

// (a) A task born directly in the done lane must stamp Closed, so it isn't a
// zombie that `done` no-ops on and `archive` skips forever.
func TestAddStampsClosedWhenBornInDoneLane(t *testing.T) {
	a := newApp()
	tk, err := a.Add("born done", AddOpts{Status: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Closed == nil {
		t.Error("a task added directly into the done lane must stamp Closed, got nil")
	}
	open, _ := a.Add("open", AddOpts{Status: "ready"})
	if open.Closed != nil {
		t.Errorf("a task in a non-done lane must not stamp Closed, got %v", open.Closed)
	}
}

// (a) the bulk path stamps Closed too, so `add --stdin -s done` doesn't leak the
// same zombie the single path was fixed for.
func TestAddManyStampsClosedInDoneLane(t *testing.T) {
	a := newApp()
	created, err := a.AddMany([]AddSpec{{Title: "bulk done", AddOpts: AddOpts{Status: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Closed == nil {
		t.Errorf("bulk add into the done lane must stamp Closed, got %+v", created)
	}
}

// (b) add must reject a dangling --dep / -e up front (exit 2), the same
// existence contract AddDep enforces — instead of silently accepting it.
func TestAddRejectsDanglingDepAndEpic(t *testing.T) {
	a := newApp()
	if _, err := a.Add("x", AddOpts{Deps: []string{"t-nope"}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add with a non-existent dep should be a validation error, got %v", err)
	}
	if _, err := a.Add("y", AddOpts{Epic: "e-nosuch"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add with a non-existent epic should be a validation error, got %v", err)
	}
	base, _ := a.Add("base", AddOpts{})
	box := mustEpic(t, a, "a box", EpicAddOpts{})
	if _, err := a.Add("member", AddOpts{Deps: []string{base.ID}, Epic: box}); err != nil {
		t.Errorf("add with an existing dep/epic should succeed, got %v", err)
	}
}

// (b) the same existence check on the bulk path, for both a dangling dep and a
// dangling epic.
func TestAddManyRejectsDanglingDep(t *testing.T) {
	a := newApp()
	if _, err := a.AddMany([]AddSpec{{Title: "x", AddOpts: AddOpts{Deps: []string{"t-ghost"}}}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("AddMany with a dangling dep should be a validation error, got %v", err)
	}
	if _, err := a.AddMany([]AddSpec{{Title: "y", AddOpts: AddOpts{Epic: "e-nosuch"}}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("AddMany with a dangling epic should be a validation error, got %v", err)
	}
}

// (c) check --add is repeatable: every item is appended, not just the last.
func TestAddChecksAppendsAll(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("steps", AddOpts{})
	got, err := a.AddChecks(tk.ID, []string{"first", "second", "third"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Checklist) != 3 {
		t.Fatalf("AddChecks should append all 3 items, got %d: %+v", len(got.Checklist), got.Checklist)
	}
	for i, want := range []string{"first", "second", "third"} {
		if got.Checklist[i].Text != want {
			t.Errorf("item %d = %q, want %q", i, got.Checklist[i].Text, want)
		}
	}
}

func TestAddDepAndRemoveDep(t *testing.T) {
	a := newApp()
	ta, _ := a.Add("a", AddOpts{})
	tb, _ := a.Add("b", AddOpts{})
	tc, _ := a.Add("c", AddOpts{})

	if _, err := a.AddDep(tb.ID, ta.ID); err != nil {
		t.Fatalf("AddDep b->a: %v", err)
	}
	if _, err := a.AddDep(tc.ID, tb.ID); err != nil {
		t.Fatalf("AddDep c->b: %v", err)
	}

	t2, err := a.AddDep(tb.ID, ta.ID)
	if err != nil {
		t.Fatalf("idempotent AddDep: %v", err)
	}
	if len(t2.Deps) != 1 || t2.Deps[0] != ta.ID {
		t.Errorf("re-adding a dep must not duplicate it, got %v", t2.Deps)
	}

	if _, err := a.AddDep(ta.ID, ta.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("self-dep should be a validation error, got %v", err)
	}
	if _, err := a.AddDep(tb.ID, "t-9999"); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown dep should be a validation error, got %v", err)
	}
	// Cycle is rejected: a->c would close the c->b->a chain.
	if _, err := a.AddDep(ta.ID, tc.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("cycle-creating dep should be a validation error, got %v", err)
	}

	rt, err := a.RemoveDep(tb.ID, ta.ID)
	if err != nil {
		t.Fatalf("RemoveDep: %v", err)
	}
	if len(rt.Deps) != 0 {
		t.Errorf("dep should be removed, got %v", rt.Deps)
	}
	// Removing a non-existent dep is a validation error (not a silent no-op).
	if _, err := a.RemoveDep(tb.ID, ta.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("removing a non-dependency should be a validation error, got %v", err)
	}
	if _, err := a.AddDep("t-9999", ta.ID); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("AddDep on unknown id should be NotFound, got %v", err)
	}
}

func hasLaneCandidates(a *App, err error) bool {
	fe := core.AsError(err)
	return fe != nil && fe.Code == core.CodeValidation && reflect.DeepEqual(fe.Candidates, a.Cfg.Lanes)
}

func TestAddWithEstimate(t *testing.T) {
	a := newApp()
	tk, err := a.Add("scoped", AddOpts{Value: ptr(3), Effort: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Value == nil || *tk.Value != 3 || tk.Effort == nil || *tk.Effort != 2 {
		t.Errorf("Add did not carry value/effort: %+v", tk)
	}
}

func TestEstimateClampedOnRead(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("x", AddOpts{})
	if _, err := a.SetValue(tk.ID, ptr(9)); err != nil { // out of range
		t.Fatal(err)
	}
	got, _, err := a.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value == nil || *got.Value != 5 {
		t.Errorf("an out-of-range value must clamp to 5 on read, got %v", got.Value)
	}
}

// A title past core.MaxTitleLen is the writer's refusal, on add and retitle
// alike: a 10,000-character title wrecked every human view's last column
// (t-awr6). Counted in characters.
func TestTitleLengthIsRefusedAtTheWriter(t *testing.T) {
	a := newApp()
	long := strings.Repeat("字", core.MaxTitleLen+1)
	if _, err := a.Add(long, AddOpts{}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add with an over-long title = %v, want exit 2", err)
	}
	tk, err := a.Add("short", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Retitle(tk.ID, long); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("retitle to an over-long title = %v, want exit 2", err)
	}
	if _, err := a.Retitle(tk.ID, strings.Repeat("字", core.MaxTitleLen)); err != nil {
		t.Errorf("a title at the cap fits: %v", err)
	}
}
