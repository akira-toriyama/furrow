package app

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

var idRe = regexp.MustCompile(`^t-[0-9a-z]{5}$`)

// fixedClock is a deterministic Clock for tests.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

func newApp() *App {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk)
}

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
	// body file seeded from the title
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
		{Title: "alpha", AddOpts: AddOpts{Value: intptr(3), Effort: intptr(2)}},
		{Title: "beta", AddOpts: AddOpts{Value: intptr(4), Effort: intptr(1)}},
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

// --- t-hgxw: write-path silent divergences ---

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
	// a non-done lane must NOT stamp Closed.
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

// (a) `done` (Move into the done lane) must backfill Closed on a pre-existing
// closed:null zombie, not no-op because it's already in the done lane.
func TestDoneBackfillsClosedOnZombie(t *testing.T) {
	a := newApp()
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-zomb1", Title: "zombie", Status: "done", Priority: 100, Body: core.BodyPath("t-zomb1")})
	a.Store.Save(idx)
	a.Store.SaveBody("t-zomb1", "# z\n")

	got, err := a.Done("t-zomb1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Closed == nil {
		t.Error("done on a closed:null done-lane task must backfill Closed, got nil")
	}
}

// (a) a repeated `done` on an already-closed task must PRESERVE the original
// Closed timestamp — the Move rewrite keys its stamp on Closed==nil, so a no-op
// re-close must not refresh the date even as the clock advances.
func TestDonePreservesOriginalClosed(t *testing.T) {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	a := NewWithStore(st, cfg, clk)

	tk, _ := a.Add("finish me", AddOpts{Status: "ready"})
	first, err := a.Done(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Closed == nil {
		t.Fatal("done should stamp Closed")
	}
	firstClosed := *first.Closed

	clk.t = clk.t.Add(48 * time.Hour) // clock moves on
	again, err := a.Done(tk.ID)       // idempotent re-close
	if err != nil {
		t.Fatal(err)
	}
	if again.Closed == nil || !again.Closed.Equal(firstClosed) {
		t.Errorf("a repeated done must preserve the original Closed %v, got %v", firstClosed, again.Closed)
	}
}

// (a) lint must flag a done-lane task with no Closed timestamp — the backstop for
// zombies created before the fix or by a hand-edit.
func TestLintFlagsDoneWithoutClosed(t *testing.T) {
	a := newApp()
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-zomb2", Title: "zombie", Status: "done", Priority: 100, Body: core.BodyPath("t-zomb2")})
	a.Store.Save(idx)
	a.Store.SaveBody("t-zomb2", "# z\n")

	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range probs {
		if p.ID == "t-zomb2" && p.Severity == core.SevError && strings.Contains(p.Msg, "closed") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should error on a done task with no closed timestamp; got %+v", probs)
	}
}

// (b) add must reject a dangling --dep / --parent up front (exit 2), the same
// existence contract AddDep enforces — instead of silently accepting it.
func TestAddRejectsDanglingDepAndParent(t *testing.T) {
	a := newApp()
	if _, err := a.Add("x", AddOpts{Deps: []string{"t-nope"}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add with a non-existent dep should be a validation error, got %v", err)
	}
	if _, err := a.Add("y", AddOpts{Parent: "t-nosuch"}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("add with a non-existent parent should be a validation error, got %v", err)
	}
	base, _ := a.Add("base", AddOpts{})
	if _, err := a.Add("child", AddOpts{Deps: []string{base.ID}, Parent: base.ID}); err != nil {
		t.Errorf("add with an existing dep/parent should succeed, got %v", err)
	}
}

// (b) the same existence check on the bulk path, for both a dangling dep and a
// dangling parent.
func TestAddManyRejectsDanglingDep(t *testing.T) {
	a := newApp()
	if _, err := a.AddMany([]AddSpec{{Title: "x", AddOpts: AddOpts{Deps: []string{"t-ghost"}}}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("AddMany with a dangling dep should be a validation error, got %v", err)
	}
	if _, err := a.AddMany([]AddSpec{{Title: "y", AddOpts: AddOpts{Parent: "t-nosuch"}}}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("AddMany with a dangling parent should be a validation error, got %v", err)
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

	// b depends on a; c depends on b.
	if _, err := a.AddDep(tb.ID, ta.ID); err != nil {
		t.Fatalf("AddDep b->a: %v", err)
	}
	if _, err := a.AddDep(tc.ID, tb.ID); err != nil {
		t.Fatalf("AddDep c->b: %v", err)
	}

	// Re-adding is idempotent (no duplicate dep).
	t2, err := a.AddDep(tb.ID, ta.ID)
	if err != nil {
		t.Fatalf("idempotent AddDep: %v", err)
	}
	if len(t2.Deps) != 1 || t2.Deps[0] != ta.ID {
		t.Errorf("re-adding a dep must not duplicate it, got %v", t2.Deps)
	}

	// Self-dependency is rejected.
	if _, err := a.AddDep(ta.ID, ta.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("self-dep should be a validation error, got %v", err)
	}
	// Unknown dependency is rejected.
	if _, err := a.AddDep(tb.ID, "t-9999"); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("unknown dep should be a validation error, got %v", err)
	}
	// Cycle is rejected: a->c would close the c->b->a chain.
	if _, err := a.AddDep(ta.ID, tc.ID); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("cycle-creating dep should be a validation error, got %v", err)
	}

	// Remove a real dep.
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
	// Unknown task id is NotFound.
	if _, err := a.AddDep("t-9999", ta.ID); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("AddDep on unknown id should be NotFound, got %v", err)
	}
}

func TestDoneStampsClosedAndMoveClears(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("ship it", AddOpts{Status: "ready"})
	done, err := a.Done(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" || done.Closed == nil {
		t.Errorf("done should set lane=done + Closed: %+v", done)
	}
	// moving back out of done clears Closed.
	back, _ := a.Move(tk.ID, "ready")
	if back.Closed != nil {
		t.Errorf("moving out of done should clear Closed, got %v", back.Closed)
	}
	// moving to icebox (terminal but not done) does NOT stamp Closed.
	ice, _ := a.Move(tk.ID, "icebox")
	if ice.Closed != nil {
		t.Errorf("icebox should not stamp Closed: %v", ice.Closed)
	}
}

func TestNextSkipsBlockedAndTerminal(t *testing.T) {
	a := newApp()
	base, _ := a.Add("base", AddOpts{Status: "ready"})
	a.Add("blocked", AddOpts{Status: "ready", Deps: []string{base.ID}})
	a.Add("parked", AddOpts{Status: "icebox"})

	next, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// only "base" is actionable: blocked waits on an open dep, parked is terminal.
	if len(next) != 1 || next[0].ID != base.ID {
		t.Errorf("next should be [base], got %+v", next)
	}

	// finishing base unblocks the dependent task.
	a.Done(base.ID)
	next, _ = a.Next(QueryOpts{})
	if len(next) != 1 || next[0].Title != "blocked" {
		t.Errorf("after base done, next should be [blocked], got %+v", next)
	}
}

func TestNextOnlyConsidersNextLanes(t *testing.T) {
	a := newApp() // default next-lanes = ready + in-progress
	a.Add("intake", AddOpts{Status: "inbox"})
	a.Add("planned", AddOpts{Status: "backlog"})
	r, _ := a.Add("ready one", AddOpts{Status: "ready"})
	a.Add("doing", AddOpts{Status: "in-progress"})

	next, err := a.Next(QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tk := range next {
		got[tk.Status] = true
	}
	if got["inbox"] || got["backlog"] {
		t.Errorf("next must exclude inbox/backlog by default, got %+v", titlesOf(next))
	}
	if !got["ready"] || !got["in-progress"] {
		t.Errorf("next must include ready + in-progress, got %+v", titlesOf(next))
	}
	if len(next) != 2 {
		t.Errorf("expected 2 actionable tasks, got %d", len(next))
	}
	_ = r
}

func TestNextFiltersByLabel(t *testing.T) {
	a := newApp() // default next-lanes = ready + in-progress
	a.Add("furrow task", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	a.Add("facet task", AddOpts{Status: "ready", Labels: []string{"facet"}})
	a.Add("both", AddOpts{Status: "in-progress", Labels: []string{"furrow", "facet"}})

	if all, _ := a.Next(QueryOpts{}); len(all) != 3 {
		t.Fatalf("no label filter should return all 3 actionable, got %d", len(all))
	}

	furrowOnly, err := a.Next(QueryOpts{Label: "furrow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(furrowOnly) != 2 {
		t.Fatalf("--label furrow should return 2 (furrow task + both), got %+v", titlesOf(furrowOnly))
	}
	for _, tk := range furrowOnly {
		if !contains(tk.Labels, "furrow") {
			t.Errorf("filtered task %q lacks the furrow label", tk.Title)
		}
	}

	if none, _ := a.Next(QueryOpts{Label: "nope"}); len(none) != 0 {
		t.Errorf("unknown label should return no tasks, got %d", len(none))
	}
}

// TestListMultiValueOR covers the comma = OR-within-a-field, flags-AND-across
// semantics for -s and -l: `-s inbox,backlog` matches either lane, `-l a,b`
// matches either tag, and combining them ANDs. Whitespace is trimmed and empty
// tokens dropped. Labels stay lenient (an open vocabulary): an unknown tag just
// matches nothing. Lanes are a closed vocabulary, so an unknown -s token now
// fails fast — pinned separately below. Regression for t-25qt / t-bec7.
func TestListMultiValueOR(t *testing.T) {
	a := newApp()
	a.Add("i-bug", AddOpts{Status: "inbox", Labels: []string{"bug"}})
	a.Add("b-urgent", AddOpts{Status: "backlog", Labels: []string{"urgent"}})
	a.Add("ready-bug", AddOpts{Status: "ready", Labels: []string{"bug"}})
	a.Add("done-chore", AddOpts{Status: "done", Labels: []string{"chore"}})

	cases := []struct {
		name string
		q    QueryOpts
		want []string
	}{
		{"status OR", QueryOpts{Status: "inbox,backlog"}, []string{"i-bug", "b-urgent"}},
		{"label OR", QueryOpts{Label: "bug,urgent"}, []string{"i-bug", "b-urgent", "ready-bug"}},
		{"AND across fields", QueryOpts{Status: "inbox,backlog", Label: "bug"}, []string{"i-bug"}},
		{"single status unchanged", QueryOpts{Status: "inbox"}, []string{"i-bug"}},
		{"single label unchanged", QueryOpts{Label: "chore"}, []string{"done-chore"}},
		{"whitespace + empty tokens", QueryOpts{Status: " inbox , , backlog "}, []string{"i-bug", "b-urgent"}},
		{"unknown label matches nothing (labels stay lenient)", QueryOpts{Label: "nonexistent"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.List(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, tk := range got {
				titles = append(titles, tk.Title)
			}
			if !reflect.DeepEqual(sortedCopy(titles), sortedCopy(tc.want)) {
				t.Errorf("%s: got %v, want %v", tc.name, titles, tc.want)
			}
		})
	}

	// -s is a closed vocabulary: an unknown token fails fast (exit 2) with the
	// configured lanes in Candidates, symmetric with move/add — NOT a silent []
	// (t-bec7 案B). A comma filter fails on the FIRST unknown token even when a
	// known token is also present.
	for _, bad := range []string{"ghost", "inbox,ghost", "ghost,phantom"} {
		_, err := a.List(QueryOpts{Status: bad})
		fe := core.AsError(err)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("-s %q should be a validation error, got %v", bad, err)
		}
		if !reflect.DeepEqual(fe.Candidates, a.Cfg.Lanes) {
			t.Errorf("-s %q error should carry the lanes in candidates, got %v", bad, fe.Candidates)
		}
	}
}

// TestUnknownLaneCandidates pins that every lane gate (add -s, move) returns the
// same "unknown lane" validation error carrying the configured lanes in
// Candidates — so an agent branches on the array, never the prose (t-bec7).
func TestUnknownLaneCandidates(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("t", AddOpts{})

	if _, err := a.Add("x", AddOpts{Status: "ghost"}); !hasLaneCandidates(a, err) {
		t.Errorf("add -s ghost should carry lane candidates, got %v", err)
	}
	if _, err := a.Move(tk.ID, "ghost"); !hasLaneCandidates(a, err) {
		t.Errorf("move to ghost should carry lane candidates, got %v", err)
	}
}

func hasLaneCandidates(a *App, err error) bool {
	fe := core.AsError(err)
	return fe != nil && fe.Code == core.CodeValidation && reflect.DeepEqual(fe.Candidates, a.Cfg.Lanes)
}

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }

// TestSetCombinedEdit pins t-kx76 (e): `set` applies lane+value+effort+labels in
// one write, honors clear/rm, rejects an empty change, and validates the lane
// like Move (unknown → candidates).
func TestSetCombinedEdit(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("triage me", AddOpts{Status: "inbox"})

	got, _, err := a.Set(tk.ID, SetOpts{Status: strp("ready"), Value: intp(4), Effort: intp(2), AddLabels: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.Value == nil || *got.Value != 4 || got.Effort == nil || *got.Effort != 2 || !contains(got.Labels, "bug") {
		t.Fatalf("set should apply every edit at once: %+v", got)
	}

	got, _, err = a.Set(tk.ID, SetOpts{ClearValue: true, RmLabels: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != nil || contains(got.Labels, "bug") {
		t.Errorf("set --clear-value/--rm-label should unset: %+v", got)
	}

	if _, _, err := a.Set(tk.ID, SetOpts{}); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty set should be a validation error, got %v", err)
	}
	if _, _, err := a.Set(tk.ID, SetOpts{Status: strp("ghost")}); !hasLaneCandidates(a, err) {
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

// TestBoardInfo pins the introspection snapshot: it mirrors the effective config
// (lanes/next/default/done) and orders terminal lanes canonically (t-bec7).
func TestBoardInfo(t *testing.T) {
	a := newApp()
	b := a.Board()
	if !reflect.DeepEqual(b.Lanes, a.Cfg.Lanes) {
		t.Errorf("board lanes = %v, want %v", b.Lanes, a.Cfg.Lanes)
	}
	if !reflect.DeepEqual(b.NextLanes, a.Cfg.NextLanes) {
		t.Errorf("board next_lanes = %v, want %v", b.NextLanes, a.Cfg.NextLanes)
	}
	if b.DefaultLane != a.Cfg.DefaultLane || b.DoneLane != a.Cfg.DoneLane {
		t.Errorf("board default/done mismatch: %+v", b)
	}
	want := []string{}
	for _, l := range a.Cfg.Lanes {
		if a.Cfg.IsTerminal(l) {
			want = append(want, l)
		}
	}
	if !reflect.DeepEqual(b.Terminal, want) {
		t.Errorf("board terminal = %v, want %v (canonical lane order)", b.Terminal, want)
	}
	// The snapshot must be a copy — mutating it can't reach the live config.
	b.Lanes[0] = "MUTATED"
	if a.Cfg.Lanes[0] == "MUTATED" {
		t.Error("Board() leaked the live Cfg.Lanes slice")
	}
}

// TestLintArchiveBacklogNudge pins t-0051: `furrow lint` warns (archive-backlog)
// when the archivable-done pile reaches [lint].archive_done, and 0 disables it.
func TestLintArchiveBacklogNudge(t *testing.T) {
	a := newApp()
	a.Cfg.LintArchiveDone = 2
	// Under the fixed clock a task done "now" is closed now; a negative window
	// pushes the cutoff into the future so those done tasks count as archivable
	// (the age mechanics themselves are covered by Archivable's own tests).
	a.Cfg.ArchiveOlderThanDays = -1

	hasNudge := func() bool {
		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.Code == "archive-backlog" {
				return true
			}
		}
		return false
	}

	if hasNudge() {
		t.Fatal("no done tasks -> no archive nudge")
	}
	a.Add("d1", AddOpts{Status: "done"})
	if hasNudge() {
		t.Error("1 archivable done < threshold 2 -> no nudge yet")
	}
	a.Add("d2", AddOpts{Status: "done"})
	if !hasNudge() {
		t.Error("2 archivable done >= threshold 2 -> nudge should fire")
	}
	a.Cfg.LintArchiveDone = 0
	if hasNudge() {
		t.Error("[lint].archive_done=0 disables the nudge")
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func titlesOf(ts []core.Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Status+":"+t.Title)
	}
	return out
}

// TestChecklistEditSurface pins t-abj3 (c-extra): add --check seeds items, and
// RewordCheck/RemoveCheck edit them (out-of-range / empty are validation errors).
func TestChecklistEditSurface(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("has steps", AddOpts{Checklist: []string{"one", "", "two", "three"}})
	if len(tk.Checklist) != 3 {
		t.Fatalf("add --check should seed 3 items (blank dropped): %+v", tk.Checklist)
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
	cfg := config.Default()
	cfg.LabelsRequired = true
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	a := NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})

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
		if p.ID == "t-0099" && p.Severity == core.SevError && contains2(p.Msg, "label") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 as label-less, got %+v", ps)
	}

	// default config (not required) accepts a label-less add.
	cfg2 := config.Default()
	a2 := NewWithStore(memstore.New(cfg2.IDPrefix, cfg2.IDWidth), cfg2, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})
	if _, err := a2.Add("fine", AddOpts{Status: "ready"}); err != nil {
		t.Errorf("label-less add should succeed when not required, got %v", err)
	}
}

func contains2(s, sub string) bool { return strings.Contains(s, sub) }

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
	// in-range still works.
	if _, err := a.Check(tk.ID, 0, true); err != nil {
		t.Errorf("in-range check should succeed, got %v", err)
	}
}

func TestArchiveCommitsBeforeDeletingBodies(t *testing.T) {
	// End-to-end archive against a real fsstore: aged done task moves to
	// .furrow/archive/, hot body is gone, archive body+index present, hot lint clean.
	dir := t.TempDir()
	ia, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	// fix the clock far in the future so the task is "old".
	ia.Clock = &fixedClock{t: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

	// create a done task closed long ago by injecting via the store.
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-0001", Title: "old done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Body: core.BodyPath("t-0001")})
	ia.Store.Save(idx)
	ia.Store.SaveBody("t-0001", "# old done\n")

	moved, err := ia.Archive(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].ID != "t-0001" {
		t.Fatalf("expected to archive t-0001, got %+v", moved)
	}
	// hot store: task gone from index AND body deleted.
	hot, _ := ia.Store.Load()
	if hot.Has("t-0001") {
		t.Error("archived task should be removed from the hot index")
	}
	if ia.Store.BodyExists("t-0001") {
		t.Error("archived task's hot body should be deleted")
	}
	// archive store: the task's shard + meta.json + body present (no index.json).
	if _, err := os.Stat(filepath.Join(ia.Dir, "archive", "tasks", "t-0001.json")); err != nil {
		t.Errorf("archive task shard should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ia.Dir, "archive", "meta.json")); err != nil {
		t.Errorf("archive meta.json should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ia.Dir, "archive", "index.json")); !os.IsNotExist(err) {
		t.Errorf("archive must not contain index.json (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(ia.Dir, "archive", "bodies", "t-0001.md")); err != nil {
		t.Errorf("archive body should exist: %v", err)
	}
	// hot store is consistent.
	ps, _ := ia.Lint()
	if core.HasErrors(ps) {
		t.Errorf("hot store should lint clean after archive, got %+v", ps)
	}
}

func TestArchivableSelection(t *testing.T) {
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	idx := &core.Index{Tasks: []core.Task{
		{ID: "t-1", Status: "done", Closed: &old},
		{ID: "t-2", Status: "done", Closed: &recent},
		{ID: "t-3", Status: "ready"},
		{ID: "t-4", Status: "icebox"}, // terminal but no Closed -> not archivable
	}}
	cutoff := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	ids := Archivable(idx, "done", cutoff)
	if len(ids) != 1 || ids[0] != "t-1" {
		t.Errorf("Archivable = %v, want [t-1]", ids)
	}
}

// eqIDs compares two id slices for exact, in-order equality (Archivable yields
// ids in index order, which is deterministic).
func eqIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestArchivableRepoFilter(t *testing.T) {
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	idx := &core.Index{Tasks: []core.Task{
		{ID: "t-a", Status: "done", Closed: &old, Repos: []string{"owner/a"}},
		{ID: "t-b", Status: "done", Closed: &old, Repos: []string{"owner/b"}},
		{ID: "t-ab", Status: "done", Closed: &old, Repos: []string{"owner/a", "owner/b"}},
		{ID: "t-recent", Status: "done", Closed: &recent, Repos: []string{"owner/a"}}, // too new
		{ID: "t-draft", Status: "done", Closed: &old, Repos: nil},                     // repo-less
	}}
	cutoff := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)

	// No repo filter: age-only, every aged done task (including the repo-less draft).
	if got := Archivable(idx, "done", cutoff); !eqIDs(got, []string{"t-a", "t-b", "t-ab", "t-draft"}) {
		t.Errorf("Archivable(no repo) = %v, want [t-a t-b t-ab t-draft]", got)
	}
	// -r owner/a: only aged done carrying owner/a — the multi-repo task counts,
	// the repo-less draft does not.
	if got := Archivable(idx, "done", cutoff, "owner/a"); !eqIDs(got, []string{"t-a", "t-ab"}) {
		t.Errorf("Archivable(owner/a) = %v, want [t-a t-ab]", got)
	}
	// Multiple repos are a union (OR): a task in ANY listed repo qualifies.
	if got := Archivable(idx, "done", cutoff, "owner/a", "owner/b"); !eqIDs(got, []string{"t-a", "t-b", "t-ab"}) {
		t.Errorf("Archivable(owner/a,owner/b) = %v, want [t-a t-b t-ab]", got)
	}
}

func TestArchiveRepoScope(t *testing.T) {
	// End-to-end repo-scoped archive against a real fsstore: only the named
	// repo's aged done task moves; the out-of-scope repo's task stays hot.
	dir := t.TempDir()
	ia, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	ia.Clock = &fixedClock{t: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-aaa1", Title: "a done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/a"}, Body: core.BodyPath("t-aaa1")})
	idx.Add(core.Task{ID: "t-bbb1", Title: "b done", Status: "done", Priority: 110,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/b"}, Body: core.BodyPath("t-bbb1")})
	ia.Store.Save(idx)
	ia.Store.SaveBody("t-aaa1", "# a\n")
	ia.Store.SaveBody("t-bbb1", "# b\n")

	moved, err := ia.Archive(30, false, "owner/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].ID != "t-aaa1" {
		t.Fatalf("repo-scoped archive should move only t-aaa1, got %+v", moved)
	}
	hot, _ := ia.Store.Load()
	if hot.Has("t-aaa1") {
		t.Error("owner/a task should be archived out of the hot index")
	}
	if !hot.Has("t-bbb1") {
		t.Error("owner/b task must remain in the hot index (out of scope)")
	}
	if ia.Store.BodyExists("t-aaa1") {
		t.Error("owner/a hot body should be deleted")
	}
	if !ia.Store.BodyExists("t-bbb1") {
		t.Error("owner/b hot body must remain")
	}
}

func TestLintFlagsMissingBody(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("has body", AddOpts{})
	// inject a task with no body file directly into the store.
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-0099", Title: "ghost", Status: "ready", Body: core.BodyPath("t-0099")})
	a.Store.Save(idx)

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range ps {
		if p.ID == "t-0099" && p.Severity == core.SevError {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 (no body), got %+v", ps)
	}
	_ = tk
}

// A task shard whose filename disagrees with the id it carries is a hand-edit
// corruption the old monolith couldn't have (the id was a field, not a
// filename). lint must catch it by directory enumeration.
func TestLintFlagsShardFilenameMismatch(t *testing.T) {
	dir := t.TempDir()
	ia, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-write a shard at tasks/t-wrongnm.json whose id field is t-realid0,
	// with a matching body so the ONLY inconsistency is the filename/id mismatch.
	fdir := filepath.Join(dir, ".furrow")
	shard := `{
  "id": "t-realid0",
  "title": "mismatched shard",
  "status": "ready",
  "priority": 100,
  "labels": [],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-06-25T00:00:00Z",
  "updated": "2026-06-25T00:00:00Z",
  "closed": null,
  "body": "bodies/t-realid0.md"
}
`
	if err := os.WriteFile(filepath.Join(fdir, "tasks", "t-wrongnm.json"), []byte(shard), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ia.Store.SaveBody("t-realid0", "# mismatched shard\n"); err != nil {
		t.Fatal(err)
	}

	ps, err := ia.Lint()
	if err != nil {
		t.Fatal(err)
	}
	// The ONLY inconsistency is the filename/id mismatch, so lint must report it
	// exactly once — no phantom "missing body" for the wrong filename and no
	// phantom "orphan body" for the real id whose shard is merely misnamed.
	var errs []core.Problem
	for _, p := range ps {
		if p.Severity == core.SevError {
			errs = append(errs, p)
		}
		if strings.Contains(p.Msg, "no body file") || strings.Contains(p.Msg, "orphan body") {
			t.Errorf("a misnamed shard must not cascade into a body finding: %+v", p)
		}
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "filename") {
		t.Errorf("expected exactly one filename-mismatch error, got %+v", ps)
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

func TestMoveManyIsAllOrNothingInOneWrite(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("one", AddOpts{})
	t2, _ := a.Add("two", AddOpts{})
	t3, _ := a.Add("three", AddOpts{})

	// Happy path: results in input order, duplicates collapse to the first
	// occurrence (the GetBatch convention).
	got, err := a.MoveMany([]string{t1.ID, t2.ID, t1.ID}, "ready")
	if err != nil {
		t.Fatalf("MoveMany: %v", err)
	}
	if len(got) != 2 || got[0].ID != t1.ID || got[1].ID != t2.ID {
		t.Fatalf("results = %+v; want [t1 t2] in input order, dup collapsed", got)
	}
	for _, tk := range got {
		if tk.Status != "ready" {
			t.Errorf("%s status = %q, want ready", tk.ID, tk.Status)
		}
	}

	// DoneMany is MoveMany into the done lane: Closed stamps on every task.
	done, err := a.DoneMany([]string{t2.ID, t3.ID})
	if err != nil {
		t.Fatalf("DoneMany: %v", err)
	}
	for _, tk := range done {
		if tk.Status != "done" || tk.Closed == nil {
			t.Errorf("%s = %q/closed %v; want done lane with Closed stamped", tk.ID, tk.Status, tk.Closed)
		}
	}

	// A missing id fails the WHOLE batch: exit 1, details.missing carries every
	// miss, and the found ids are untouched (all-or-nothing — a write must never
	// half-land the way a batch read may partially succeed).
	_, err = a.MoveMany([]string{t1.ID, "t-nope", "t-nada"}, "backlog")
	if core.ExitCode(err) != int(core.CodeNotFound) {
		t.Fatalf("miss should be NotFound, got %v", err)
	}
	fe := core.AsError(err)
	miss, _ := fe.Details.(map[string]any)["missing"].([]string)
	if strings.Join(miss, ",") != "t-nope,t-nada" {
		t.Errorf("details.missing = %v, want both misses", fe.Details)
	}
	if cur, _, _ := a.Get(t1.ID); cur.Status != "ready" {
		t.Errorf("t1 moved to %q despite the failed batch; all-or-nothing broken", cur.Status)
	}

	// An unknown lane is the usual exit-2 candidates error, nothing written.
	_, err = a.MoveMany([]string{t1.ID}, "reddy")
	if core.ExitCode(err) != int(core.CodeValidation) || len(core.AsError(err).Candidates) == 0 {
		t.Errorf("unknown lane should be validation with candidates, got %v", err)
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
	cfg := config.Default()
	cfg.LabelsRequired = true
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	a := NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)})
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

func intptr(n int) *int { return &n }

func TestSetValueAndEffort(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("estimate me", AddOpts{})
	if tk.Value != nil || tk.Effort != nil {
		t.Fatalf("a fresh task must have unset value/effort: %+v", tk)
	}

	got, err := a.SetValue(tk.ID, intptr(4))
	if err != nil {
		t.Fatal(err)
	}
	if got.Value == nil || *got.Value != 4 || got.Effort != nil {
		t.Errorf("after SetValue(4): value=%v effort=%v", got.Value, got.Effort)
	}

	got, err = a.SetEffort(tk.ID, intptr(2))
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

func TestAddWithEstimate(t *testing.T) {
	a := newApp()
	tk, err := a.Add("scoped", AddOpts{Value: intptr(3), Effort: intptr(2)})
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
	if _, err := a.SetValue(tk.ID, intptr(9)); err != nil { // out of range
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

func TestLintWarnsOutOfRangeEstimate(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("x", AddOpts{})
	if _, err := a.SetValue(tk.ID, intptr(9)); err != nil {
		t.Fatal(err)
	}
	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range probs {
		if p.ID == tk.ID && p.Severity == core.SevWarn && strings.Contains(p.Msg, "value 9") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should warn that value 9 is out of range; got %+v", probs)
	}
}
