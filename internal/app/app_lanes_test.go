package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

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
	a, clk := newAppWith()

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
		if !slices.Contains(tk.Labels, "furrow") {
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

func TestArchiveCommitsBeforeDeletingBodies(t *testing.T) {
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

	rep, err := ia.Archive(30, false)
	if err != nil {
		t.Fatal(err)
	}
	if moved := rep.Tasks; len(moved) != 1 || moved[0].ID != "t-0001" {
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
	if got := Archivable(idx, "done", cutoff); !slices.Equal(got, []string{"t-a", "t-b", "t-ab", "t-draft"}) {
		t.Errorf("Archivable(no repo) = %v, want [t-a t-b t-ab t-draft]", got)
	}
	// -r owner/a: only aged done carrying owner/a — the multi-repo task counts,
	// the repo-less draft does not.
	if got := Archivable(idx, "done", cutoff, "owner/a"); !slices.Equal(got, []string{"t-a", "t-ab"}) {
		t.Errorf("Archivable(owner/a) = %v, want [t-a t-ab]", got)
	}
	// Multiple repos are a union (OR): a task in ANY listed repo qualifies.
	if got := Archivable(idx, "done", cutoff, "owner/a", "owner/b"); !slices.Equal(got, []string{"t-a", "t-b", "t-ab"}) {
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

	rep, err := ia.Archive(30, false, "owner/a")
	if err != nil {
		t.Fatal(err)
	}
	if moved := rep.Tasks; len(moved) != 1 || moved[0].ID != "t-aaa1" {
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

func TestMoveManyIsAllOrNothingInOneWrite(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("one", AddOpts{})
	t2, _ := a.Add("two", AddOpts{})
	t3, _ := a.Add("three", AddOpts{})

	// Happy path: results in input order, duplicates collapse to the first
	// occurrence (the GetBatch convention).
	got, _, err := a.MoveMany([]string{t1.ID, t2.ID, t1.ID}, "ready", nil)
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
	done, _, err := a.DoneMany([]string{t2.ID, t3.ID}, nil)
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
	_, _, err = a.MoveMany([]string{t1.ID, "t-nope", "t-nada"}, "backlog", nil)
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
	_, _, err = a.MoveMany([]string{t1.ID}, "reddy", nil)
	if core.ExitCode(err) != int(core.CodeValidation) || len(core.AsError(err).Candidates) == 0 {
		t.Errorf("unknown lane should be validation with candidates, got %v", err)
	}
}
