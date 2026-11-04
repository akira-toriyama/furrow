package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

func mustSetSuccessor(t *testing.T, a *App, box, successor string) {
	t.Helper()
	if _, _, _, err := a.EpicSet(box, EpicSetOpts{Successor: ptr(successor)}); err != nil {
		t.Fatalf("epic set %s --successor %s: %v", box, successor, err)
	}
}

func wantSetRefused(t *testing.T, a *App, box string, o EpicSetOpts, want string) {
	t.Helper()
	_, _, _, err := a.EpicSet(box, o)
	var fe *core.Error
	if !errors.As(err, &fe) || fe.Code != core.CodeValidation {
		t.Fatalf("want a validation refusal mentioning %q, got %v", want, err)
	}
	if !strings.Contains(fe.Msg, want) {
		t.Errorf("refusal = %q, want it to say %q", fe.Msg, want)
	}
}

// The declaration's write path: stored as the resolved id, cleared on request,
// and refused where only this moment can judge — a self-reference, a closed
// target, a loop through the chain, a target the box already waits on, and a
// standing box in either direction.
func TestEpicSetSuccessorDeclaresAndRefuses(t *testing.T) {
	a := newApp()
	ea := mustEpic(t, a, "aws migration", EpicAddOpts{Repos: []string{"o/r"}})
	eb := mustEpic(t, a, "standardization", EpicAddOpts{Repos: []string{"o/r"}})
	ec := mustEpic(t, a, "closed box", EpicAddOpts{Repos: []string{"o/r"}})
	mustEpicDone(t, a, ec)

	before, after, _, err := a.EpicSet(ea, EpicSetOpts{Successor: ptr("standard")}) // a title substring resolves
	if err != nil {
		t.Fatal(err)
	}
	if before.Successor != "" || after.Successor != eb {
		t.Errorf("successor before/after = %q/%q, want \"\"/%s", before.Successor, after.Successor, eb)
	}
	if e, _, _ := a.Store.LoadEpic(ea); e.Successor != eb {
		t.Errorf("stored successor = %q, want %s", e.Successor, eb)
	}

	wantSetRefused(t, a, ea, EpicSetOpts{Successor: ptr(ea)}, "its own successor")
	wantSetRefused(t, a, ea, EpicSetOpts{Successor: ptr(ec)}, "is closed")
	wantSetRefused(t, a, eb, EpicSetOpts{Successor: ptr(ea)}, "close a loop")
	wantSetRefused(t, a, ea, EpicSetOpts{Successor: ptr(eb), ClearSuccessor: true}, "pass one")
	if _, _, err := a.EpicAddDeps(eb, []string{ea}); err != nil { // b waits on a: consistent with a→b
		t.Fatal(err)
	}
	mustSetSuccessor(t, a, ea, eb)
	ed := mustEpic(t, a, "waits on b", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicAddDeps(ed, []string{eb}); err != nil {
		t.Fatal(err)
	}
	wantSetRefused(t, a, ed, EpicSetOpts{Successor: ptr(eb)}, "waits on")

	// Standing in either direction: a box that never closes has no "after".
	wantSetRefused(t, a, ea, EpicSetOpts{Standing: ptr(true)}, "standing")
	mandate := mustEpic(t, a, "mandate", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, _, err := a.EpicSet(mandate, EpicSetOpts{Standing: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	wantSetRefused(t, a, mandate, EpicSetOpts{Successor: ptr(eb)}, "standing")
	// ...but a standing TARGET is a legitimate "open the mandate box next".
	mustSetSuccessor(t, a, ed, mandate)

	// One coherent write: clear the plan and declare standing together.
	if _, after, _, err := a.EpicSet(ea, EpicSetOpts{Standing: ptr(true), ClearSuccessor: true}); err != nil || after.Successor != "" || !after.Standing {
		t.Errorf("--standing --clear-successor must land as one write: %v %+v", err, after)
	}
	if _, _, _, err := a.EpicSet(eb, EpicSetOpts{}); err == nil || !strings.Contains(err.Error(), "--successor") || !strings.Contains(err.Error(), "--clear-successor") {
		t.Errorf("the no-op refusal must list the new flags: %v", err)
	}
}

// What `epic done` proposes from the declaration, per outcome.
func TestSuccessorSuggestResolves(t *testing.T) {
	t.Run("an open, inactive box with a free slot is the plain proposal", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
		ec := mustEpic(t, a, "c", EpicAddOpts{Repos: []string{"o/r"}})
		mustSetSuccessor(t, a, ea, eb)
		if _, _, err := a.EpicAddDeps(eb, []string{ea, ec}); err != nil {
			t.Fatal(err)
		}
		mustActivate(t, a, ea)
		mustEpicDone(t, a, ea)
		s := a.SuccessorSuggest(ea)
		if s == nil || s.ID != eb || s.Declared != eb || s.Reason != "" || s.State != "open" || !s.Proposed() {
			t.Fatalf("suggest = %+v, want the plain proposal of %s", s, eb)
		}
		// The deps are judged AFTER the close: the box just closed is not among them.
		if !slices.Equal(s.OpenDeps, []string{ec}) {
			t.Errorf("open_deps = %v, want [%s] (the closed declarer must not count)", s.OpenDeps, ec)
		}
		if a.SuccessorSuggest(ec) != nil {
			t.Error("a box that declared nothing suggests nothing")
		}
	})
	t.Run("an already active box says so", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/other"}})
		mustSetSuccessor(t, a, ea, eb)
		mustActivate(t, a, eb)
		mustEpicDone(t, a, ea)
		if s := a.SuccessorSuggest(ea); s == nil || s.Reason != SuccessorActive || !s.Proposed() {
			t.Errorf("suggest = %+v, want reason active", s)
		}
	})
	t.Run("a held slot names the holder", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/other"}})
		holder := mustEpic(t, a, "holder", EpicAddOpts{Repos: []string{"o/other"}})
		mustSetSuccessor(t, a, ea, eb)
		mustActivate(t, a, holder)
		mustEpicDone(t, a, ea)
		s := a.SuccessorSuggest(ea)
		if s == nil || s.Reason != SuccessorSlotHeld || s.Held["o/other"] != holder || s.Proposed() {
			t.Errorf("suggest = %+v, want slot-held by %s", s, holder)
		}
	})
	t.Run("a repo-less box cannot be activated", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{})
		mustSetSuccessor(t, a, ea, eb)
		mustEpicDone(t, a, ea)
		if s := a.SuccessorSuggest(ea); s == nil || s.Reason != SuccessorNoRepo {
			t.Errorf("suggest = %+v, want no-repo", s)
		}
	})
	t.Run("a closed successor is walked past along its own chain", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
		ec := mustEpic(t, a, "c", EpicAddOpts{Repos: []string{"o/r"}})
		mustSetSuccessor(t, a, ea, eb)
		mustSetSuccessor(t, a, eb, ec)
		mustEpicDone(t, a, eb)
		mustEpicDone(t, a, ea)
		s := a.SuccessorSuggest(ea)
		if s == nil || s.ID != ec || s.Declared != eb || s.Reason != "" || !slices.Equal(s.Skipped, []string{eb}) {
			t.Errorf("suggest = %+v, want %s reached past closed %s", s, ec, eb)
		}
		// A dead end reports the DECLARED box as closed.
		mustEpicDone(t, a, ec)
		if s := a.SuccessorSuggest(ea); s == nil || s.ID != eb || s.State != "closed" || s.Reason != SuccessorClosed || s.Proposed() {
			t.Errorf("suggest = %+v, want the declared %s reported closed", s, eb)
		}
	})
	t.Run("a dangling declaration is missing", func(t *testing.T) {
		a := newApp()
		ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
		eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
		mustSetSuccessor(t, a, ea, eb)
		if _, err := a.RemoveEpic(eb, RemoveOpts{Apply: true, Force: true}); err != nil {
			t.Fatal(err)
		}
		// rm severed the declaration; plant a dangling one as a merge would.
		e, _, _ := a.Store.LoadEpic(ea)
		e.Successor = "e-gone"
		if err := a.saveEpic(e); err != nil {
			t.Fatal(err)
		}
		mustEpicDone(t, a, ea)
		if s := a.SuccessorSuggest(ea); s == nil || s.Reason != SuccessorMissing || s.State != "missing" || s.ID != "e-gone" {
			t.Errorf("suggest = %+v, want missing", s)
		}
	})
}

// The previous suggestion restricted to the slots the proposal leaves
// uncovered: a cross-repo declarer whose successor spans one of its repos.
func TestSuccessorUncoveredReposAndPreviousAmong(t *testing.T) {
	a := newApp()
	ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r", "o/other"}})
	eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
	old := mustEpic(t, a, "old focus elsewhere", EpicAddOpts{Repos: []string{"o/other"}})
	mustActivate(t, a, old)
	if _, _, err := a.EpicDeactivate(old); err != nil {
		t.Fatal(err)
	}
	mustSetSuccessor(t, a, ea, eb)
	mustActivate(t, a, ea)
	mustEpicDone(t, a, ea)
	s := a.SuccessorSuggest(ea)
	if got := a.UncoveredRepos(s, []string{"o/r", "o/other"}); !slices.Equal(got, []string{"o/other"}) {
		t.Fatalf("uncovered = %v, want [o/other]", got)
	}
	prev, _ := a.PreviousActiveAmong(ea, []string{"o/other"})
	if prev == nil || prev.ID != old {
		t.Errorf("previous among o/other = %+v, want %s", prev, old)
	}
	if prev, _ := a.PreviousActiveAmong(ea, []string{"o/r"}); prev != nil {
		t.Errorf("no box in o/r has an activation record, want nil, got %+v", prev)
	}
}

// brief names the plan a closed box left when nothing is active, and drops it
// the moment a box is active again.
func TestBriefNextUpNamesTheDeclaredSuccessor(t *testing.T) {
	a := newApp()
	ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
	eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
	mustSetSuccessor(t, a, ea, eb)
	mustActivate(t, a, ea)
	mustEpicDone(t, a, ea)
	b, err := a.Brief(QueryOpts{}, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.NextUp == nil || b.NextUp.ID != eb || b.NextUp.After != ea {
		t.Fatalf("next_up = %+v, want %s after %s", b.NextUp, eb, ea)
	}
	mustActivate(t, a, eb)
	if b, _ := a.Brief(QueryOpts{}, 5, 0); b.NextUp != nil {
		t.Errorf("a box is active: next_up must be nil, got %+v", b.NextUp)
	}
}

// A declaration naming the box is a reference `epic rm` refuses over, and
// severs under --force.
func TestRemoveEpicCountsSuccessorDeclarations(t *testing.T) {
	a := newApp()
	ea := mustEpic(t, a, "a", EpicAddOpts{Repos: []string{"o/r"}})
	eb := mustEpic(t, a, "b", EpicAddOpts{Repos: []string{"o/r"}})
	mustSetSuccessor(t, a, ea, eb)
	_, err := a.RemoveEpic(eb, RemoveOpts{Apply: true})
	refs := wantReferenced(t, err)
	if len(refs.Successors) != 1 || refs.Successors[0] != (DepEdge{From: ea, To: eb}) {
		t.Errorf("successors = %+v, want the declaration from %s", refs.Successors, ea)
	}
	if !strings.Contains(refs.Summary(), "successor declaration") {
		t.Errorf("summary = %q, want it to name the declaration", refs.Summary())
	}
	if _, err := a.RemoveEpic(eb, RemoveOpts{Apply: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := a.Store.LoadEpic(ea); e.Successor != "" {
		t.Errorf("--force must drop the declaration, got %q", e.Successor)
	}
}
