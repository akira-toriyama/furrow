package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// Restoring a task whose dependency stays archived would put a dependent on
// the hot board that reads as blocked on an id the index lacks (lint
// dep-missing, the board's pre-push lint refusing the push): the mirror of
// ArchiveIDs' hold, so it is refused the same way (t-kw28).
func TestUnarchiveRefusesWhenADependencyStaysArchived(t *testing.T) {
	a := newFSApp(t)
	dep, err := a.Add("dependency", AddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := a.Add("dependent", AddOpts{Repos: []string{"o/r"}, Deps: []string{dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{dep.ID, dependent.ID} {
		if _, err := a.Done(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ArchiveIDs([]string{dependent.ID, dep.ID}, false); err != nil {
		t.Fatal(err)
	}
	_, err = a.Unarchive([]string{dependent.ID})
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindReferenced {
		t.Fatalf("want kind referenced, got %v", err)
	}
	if !strings.Contains(fe.Msg, "furrow unarchive "+dependent.ID+" "+dep.ID) {
		t.Fatalf("the refusal must name the call that restores both: %s", fe.Msg)
	}
	idx, _ := a.Store.Load()
	if idx.Has(dependent.ID) {
		t.Fatal("a refused unarchive restored the dependent anyway")
	}
	if _, err := a.Unarchive([]string{dependent.ID, dep.ID}); err != nil {
		t.Fatalf("restoring both together must pass: %v", err)
	}
	problems, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		if p.Code == "dep-missing" {
			t.Fatalf("dep-missing after restoring both: %+v", p)
		}
	}
}

// Unarchive is a task write like every other: a session holding one of the
// restored tasks' repos refuses it before anything moves.
func TestUnarchiveIsGuardedBySession(t *testing.T) {
	a := newFSApp(t) // archive needs a file-backed store; the guard is armed below
	tk, err := a.Add("retired", AddOpts{Repos: []string{"o/glyph"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Done(tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ArchiveIDs([]string{tk.ID}, false); err != nil {
		t.Fatal(err)
	}
	now := a.Clock.Now()
	glyph := fakeCheckout(t, t.TempDir(), "glyph", "o/glyph")
	a.Sessions = fakeRegistry{sessions: []core.Session{
		{PID: 100, ID: "self", CWD: t.TempDir(), StartedAt: now.Add(-10 * time.Minute), LastActive: now},
		{PID: 1, ID: "occ", Name: "glyph-ab", CWD: glyph, StartedAt: now.Add(-time.Hour), LastActive: now.Add(-time.Minute)},
	}}
	a.Self = SessionRef{PID: 100, ID: "self"}
	_, err = a.Unarchive([]string{tk.ID})
	if err == nil {
		t.Fatal("restoring into a busy repo must be refused")
	}
	wantSessionBusy(t, err, tk.ID)
	idx, _ := a.Store.Load()
	if idx.Has(tk.ID) {
		t.Fatal("a refused unarchive restored the task anyway")
	}
}
