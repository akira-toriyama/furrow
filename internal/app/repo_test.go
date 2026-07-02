package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// asValidation asserts err is a *core.Error with CodeValidation and returns it.
func asValidation(t *testing.T, err error) *core.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	var fe *core.Error
	if !errors.As(err, &fe) {
		t.Fatalf("expected *core.Error, got %T: %v", err, err)
	}
	if fe.Code != core.CodeValidation {
		t.Fatalf("code = %d, want %d (validation): %v", fe.Code, core.CodeValidation, fe)
	}
	return fe
}

func TestResolveRepo(t *testing.T) {
	a := newApp()
	a.Add("f", AddOpts{Repos: []string{"akira-toriyama/furrow"}})
	a.Add("c", AddOpts{Repos: []string{"akira-toriyama/chord"}})
	a.Add("f2", AddOpts{Repos: []string{"other-org/furrow"}})

	// unique short name resolves to the full owner/repo.
	got, err := a.ResolveRepo("chord")
	if err != nil || got != "akira-toriyama/chord" {
		t.Errorf("ResolveRepo(chord) = %q, %v; want akira-toriyama/chord", got, err)
	}
	// case-insensitive.
	if got, _ := a.ResolveRepo("CHORD"); got != "akira-toriyama/chord" {
		t.Errorf("ResolveRepo(CHORD) = %q, want akira-toriyama/chord", got)
	}
	// ambiguous short name -> validation error carrying the candidates.
	_, err = a.ResolveRepo("furrow")
	fe := asValidation(t, err)
	want := []string{"akira-toriyama/furrow", "other-org/furrow"}
	if !reflect.DeepEqual(fe.Candidates, want) {
		t.Errorf("candidates = %v, want %v", fe.Candidates, want)
	}
	// full owner/repo passes even when unknown to the board (first attach).
	if got, err := a.ResolveRepo("brand/new"); err != nil || got != "brand/new" {
		t.Errorf("ResolveRepo(brand/new) = %q, %v; want pass-through", got, err)
	}
	// full form canonicalizes casing against the universe.
	if got, _ := a.ResolveRepo("Akira-Toriyama/Chord"); got != "akira-toriyama/chord" {
		t.Errorf("full-form casing should canonicalize, got %q", got)
	}
	// unresolvable short name -> validation error.
	asValidation(t, errOf(a.ResolveRepo("ghost")))
}

func errOf(_ string, err error) error { return err }

// The board-derived repos (P3 seam) participate in the resolution universe.
func TestResolveRepoUsesBoardRepos(t *testing.T) {
	a := newApp()
	a.BoardRepos = []string{"akira-toriyama/facet"}
	got, err := a.ResolveRepo("facet")
	if err != nil || got != "akira-toriyama/facet" {
		t.Errorf("ResolveRepo(facet) = %q, %v; want the board-derived repo", got, err)
	}
}

func TestAddResolvesReposAndDraftConflicts(t *testing.T) {
	a := newApp()
	a.Add("seed", AddOpts{Repos: []string{"akira-toriyama/furrow"}})

	// short name on add resolves against the universe.
	tk, err := a.Add("more", AddOpts{Repos: []string{"furrow"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tk.Repos) != 1 || tk.Repos[0] != "akira-toriyama/furrow" {
		t.Errorf("add should resolve the short name, got %v", tk.Repos)
	}
	// a bare unresolvable name is rejected (strict).
	if _, err := a.Add("bad", AddOpts{Repos: []string{"ghost"}}); err == nil {
		t.Error("add with an unresolvable short name should fail")
	}
	// --draft conflicts with an explicit repo.
	_, err = a.Add("conflicted", AddOpts{Draft: true, Repos: []string{"akira-toriyama/furrow"}})
	asValidation(t, err)
	// --draft alone creates a repo-less task.
	d, err := a.Add("a draft", AddOpts{Draft: true})
	if err != nil || len(d.Repos) != 0 {
		t.Errorf("draft add should create with no repos, got %v, %v", d, err)
	}
}

func TestRerepo(t *testing.T) {
	a := newApp()
	seed, _ := a.Add("seed", AddOpts{Repos: []string{"akira-toriyama/furrow"}})
	tk, _ := a.Add("edit me", AddOpts{})

	// --add with a full owner/repo (even brand new).
	after, err := a.Rerepo(tk.ID, []string{"akira-toriyama/chord"}, nil)
	if err != nil || !reflect.DeepEqual(after.Repos, []string{"akira-toriyama/chord"}) {
		t.Fatalf("Rerepo add full = %v, %v", after, err)
	}
	// --add with a unique short name resolves.
	after, err = a.Rerepo(tk.ID, []string{"furrow"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Repos, []string{"akira-toriyama/chord", "akira-toriyama/furrow"}) {
		t.Errorf("after add short: %v", after.Repos)
	}
	// idempotent re-add.
	again, err := a.Rerepo(tk.ID, []string{"furrow"}, nil)
	if err != nil || len(again.Repos) != 2 {
		t.Errorf("re-add should be a no-op, got %v, %v", again.Repos, err)
	}
	// --rm accepts the same forms (short name here).
	after, err = a.Rerepo(tk.ID, nil, []string{"chord"})
	if err != nil || !reflect.DeepEqual(after.Repos, []string{"akira-toriyama/furrow"}) {
		t.Errorf("rm short = %v, %v", after.Repos, err)
	}
	// strict: a bare unresolvable name is a validation error, not a new repo.
	_, err = a.Rerepo(tk.ID, []string{"not-a-repo"}, nil)
	fe := asValidation(t, err)
	if !strings.Contains(fe.Msg, "owner/repo") {
		t.Errorf("strict-add error should point at the owner/repo form: %q", fe.Msg)
	}
	// neither flag is bad usage.
	_, err = a.Rerepo(tk.ID, nil, nil)
	asValidation(t, err)
	// unknown id is not-found.
	if _, err := a.Rerepo("t-zzzzz", []string{"a/b"}, nil); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("unknown id should be not-found, got %v", err)
	}
	_ = seed
}

func TestListNextFilterByRepoAndDrafts(t *testing.T) {
	a := newApp()
	f, _ := a.Add("furrow work", AddOpts{Status: "ready", Repos: []string{"akira-toriyama/furrow"}})
	c, _ := a.Add("chord work", AddOpts{Status: "ready", Repos: []string{"akira-toriyama/chord"}})
	d, _ := a.Add("a draft", AddOpts{Status: "ready"})

	// List by repo.
	got, err := a.List(QueryOpts{Repo: "akira-toriyama/furrow"})
	if err != nil || len(got) != 1 || got[0].ID != f.ID {
		t.Errorf("List by repo = %+v, %v", got, err)
	}
	// Drafts only.
	drafts, _ := a.List(QueryOpts{Drafts: true})
	if len(drafts) != 1 || drafts[0].ID != d.ID {
		t.Errorf("List drafts = %+v", drafts)
	}
	// Drafts bypass the board scope (ScopeLabel would exclude everything here).
	drafts, _ = a.List(QueryOpts{Drafts: true, ScopeLabel: "nothing-has-this"})
	if len(drafts) != 1 || drafts[0].ID != d.ID {
		t.Errorf("drafts must bypass the scope, got %+v", drafts)
	}
	// Next by repo.
	next, _ := a.Next(QueryOpts{Repo: "akira-toriyama/chord"})
	if len(next) != 1 || next[0].ID != c.ID {
		t.Errorf("Next by repo = %+v", next)
	}
	// Scope + tag AND at the app layer.
	a.Add("scoped tagged", AddOpts{Status: "ready", Labels: []string{"scope", "tag"}})
	a.Add("scoped plain", AddOpts{Status: "ready", Labels: []string{"scope"}})
	a.Add("unscoped tagged", AddOpts{Status: "ready", Labels: []string{"tag"}})
	both, _ := a.List(QueryOpts{ScopeLabel: "scope", Label: "tag"})
	if len(both) != 1 || both[0].Title != "scoped tagged" {
		t.Errorf("scope AND tag should keep exactly the scoped+tagged task, got %+v", both)
	}
}

// Revisit surfaces open drafts (no_repo) regardless of the board scope or a
// repo filter; repo-attached tasks still honor the scope.
func TestRevisitSurfacesDraftsRegardlessOfScope(t *testing.T) {
	a := newApp()
	d, _ := a.Add("a draft", AddOpts{Status: "ready"})
	a.Add("other repo task", AddOpts{Status: "ready", Repos: []string{"other/repo"}})

	items, err := a.Revisit(QueryOpts{ScopeLabel: "some-scope"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Task.ID != d.ID {
		t.Fatalf("scoped revisit should still surface the draft (and only it), got %+v", items)
	}
	found := false
	for _, r := range items[0].Reasons {
		if r.Code == core.RevisitNoRepo {
			found = true
		}
	}
	if !found {
		t.Errorf("draft should carry the no_repo reason, got %+v", items[0].Reasons)
	}

	// A repo filter keeps drafts visible too (the -r hidden-drafts hint is a
	// CLI concern for ls/next; revisit itself never hides a draft).
	items, _ = a.Revisit(QueryOpts{Repo: "other/repo"}, 0)
	ids := map[string]bool{}
	for _, it := range items {
		ids[it.Task.ID] = true
	}
	if !ids[d.ID] {
		t.Errorf("revisit with a repo filter must still surface drafts, got %+v", items)
	}
}

func TestDidYouMeanRepo(t *testing.T) {
	a := newApp()
	a.Add("one", AddOpts{Repos: []string{"akira-toriyama/furrow"}})
	a.Add("two", AddOpts{Repos: []string{"akira-toriyama/furrow"}})

	// fires: the label matches nothing, but uniquely names a repo with tasks.
	err := a.DidYouMeanRepo("furrow")
	fe := asValidation(t, err)
	if !reflect.DeepEqual(fe.Candidates, []string{"akira-toriyama/furrow"}) {
		t.Errorf("candidates = %v", fe.Candidates)
	}
	for _, want := range []string{`label "furrow" matches no tasks`, "has 2 task(s)", "use -r furrow"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("message %q should contain %q", fe.Msg, want)
		}
	}

	// does not fire: the label exists as a tag somewhere.
	a.Add("tagged", AddOpts{Labels: []string{"furrow"}})
	if err := a.DidYouMeanRepo("furrow"); err != nil {
		t.Errorf("guard must stay quiet when the label exists as a tag: %v", err)
	}
	// does not fire: nothing resolves.
	if err := a.DidYouMeanRepo("ghost"); err != nil {
		t.Errorf("guard must stay quiet on an unknown name: %v", err)
	}
	// does not fire on ambiguity (only a UNIQUE short-name match guards).
	a.Add("three", AddOpts{Repos: []string{"other-org/chord"}})
	a.Add("four", AddOpts{Repos: []string{"akira-toriyama/chord"}})
	if err := a.DidYouMeanRepo("chord"); err != nil {
		t.Errorf("guard must stay quiet on an ambiguous short name: %v", err)
	}
}

// AddMany (add --stdin) honors Repos on the shared opts and resolves them.
func TestAddManyAttachesRepos(t *testing.T) {
	a := newApp()
	a.Add("seed", AddOpts{Repos: []string{"akira-toriyama/furrow"}})
	created, err := a.AddMany([]AddSpec{
		{Title: "alpha", AddOpts: AddOpts{Repos: []string{"furrow"}}},
		{Title: "beta", AddOpts: AddOpts{Repos: []string{"furrow"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range created {
		if !reflect.DeepEqual(tk.Repos, []string{"akira-toriyama/furrow"}) {
			t.Errorf("%s repos = %v", tk.Title, tk.Repos)
		}
	}
	// a draft-with-repo spec fails before anything is written.
	if _, err := a.AddMany([]AddSpec{{Title: "x", AddOpts: AddOpts{Draft: true, Repos: []string{"a/b"}}}}); err == nil {
		t.Error("AddMany should reject Draft+Repos")
	}
}
