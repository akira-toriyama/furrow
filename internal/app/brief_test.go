package app

import (
	"strings"
	"testing"
	"time"
)

// briefBoard builds the session-orient fixture: two actionable tasks (ready +
// in-progress), one dep-blocked task in a next lane, one inbox task (neither),
// one repo-less draft, and one backlog task whose dep is done (a revisit
// signal). Repos keep the drafts count honest (a task with no repo IS a draft).
func briefBoard(t *testing.T) (*App, map[string]string) {
	t.Helper()
	a, _ := appWithClock(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	ids := map[string]string{}
	add := func(key, title string, o AddOpts) string {
		t.Helper()
		tk, err := a.Add(title, o)
		if err != nil {
			t.Fatalf("add %s: %v", key, err)
		}
		ids[key] = tk.ID
		return tk.ID
	}
	readyA := add("ready-a", "ready a", AddOpts{Status: "ready", Repos: []string{"o/r"}})
	add("wip", "in progress", AddOpts{Status: "in-progress", Repos: []string{"o/r"}})
	add("blocked", "blocked b", AddOpts{Status: "ready", Repos: []string{"o/r"}, Deps: []string{readyA}})
	add("inbox", "inbox idea", AddOpts{Status: "inbox", Repos: []string{"o/r"}})
	add("draft", "a draft", AddOpts{})
	doneDep := add("done-dep", "finished", AddOpts{Status: "ready", Repos: []string{"o/r"}})
	if _, err := a.Done(doneDep); err != nil {
		t.Fatal(err)
	}
	add("revisit-me", "waiting on finished", AddOpts{Status: "backlog", Repos: []string{"o/r"}, Deps: []string{doneDep}})
	return a, ids
}

func TestBriefComposesTheSessionOrientRead(t *testing.T) {
	a, ids := briefBoard(t)

	b, err := a.Brief(QueryOpts{}, 3, 0)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}

	// next: the actionable tasks (ready-a, wip) in canonical order, WITH bodies.
	if b.NextTotal != 2 || len(b.Next) != 2 {
		t.Fatalf("next = %d shown / %d total, want 2/2", len(b.Next), b.NextTotal)
	}
	if b.Next[0].Task.ID != ids["ready-a"] || b.Next[1].Task.ID != ids["wip"] {
		t.Errorf("next order = %s,%s; want ready-a then wip (canonical)", b.Next[0].Task.ID, b.Next[1].Task.ID)
	}
	if !strings.Contains(b.Next[0].Body, "# ready a") {
		t.Errorf("next[0] must carry its body, got %q", b.Next[0].Body)
	}

	// blocked: the next-lane task with an unsatisfied dep, naming what blocks it.
	if len(b.Blocked) != 1 || b.Blocked[0].Task.ID != ids["blocked"] {
		t.Fatalf("blocked = %+v; want just the dep-blocked ready task", b.Blocked)
	}
	if strings.Join(b.Blocked[0].BlockedBy, ",") != ids["ready-a"] {
		t.Errorf("blocked_by = %v, want [%s]", b.Blocked[0].BlockedBy, ids["ready-a"])
	}

	// revisit: the dep-done signal surfaces through the summary.
	if strings.Join(b.Revisit.DepDone, ",") != ids["revisit-me"] {
		t.Errorf("revisit.dep_done = %v, want [%s]", b.Revisit.DepDone, ids["revisit-me"])
	}

	// drafts: the repo-less task counts, wherever the scope points.
	if b.Drafts != 1 {
		t.Errorf("drafts = %d, want 1", b.Drafts)
	}
}

func TestBriefCapsNextWithoutHidingTheTotal(t *testing.T) {
	a, ids := briefBoard(t)

	b, err := a.Brief(QueryOpts{}, 1, 0)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.Next) != 1 || b.Next[0].Task.ID != ids["ready-a"] {
		t.Fatalf("capped next = %+v, want just ready-a", b.Next)
	}
	if b.NextTotal != 2 {
		t.Errorf("next_total = %d, want 2 (the cap must not hide the count)", b.NextTotal)
	}
}

func TestBriefHonorsTheRepoScope(t *testing.T) {
	a, ids := briefBoard(t)
	other, err := a.Add("other repo ready", AddOpts{Status: "ready", Repos: []string{"x/y"}})
	if err != nil {
		t.Fatal(err)
	}

	b, err := a.Brief(QueryOpts{ScopeRepo: "x/y"}, 3, 0)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.NextTotal != 1 || b.Next[0].Task.ID != other.ID {
		t.Fatalf("scoped next = %+v, want just the x/y task", b.Next)
	}
	if len(b.Blocked) != 0 {
		t.Errorf("scoped blocked = %+v, want none (o/r tasks out of scope)", b.Blocked)
	}
	// A draft has no repo, so the count survives any scope (the ls --drafts rule).
	if b.Drafts != 1 {
		t.Errorf("drafts = %d, want 1 under a scope too", b.Drafts)
	}
	_ = ids
}
