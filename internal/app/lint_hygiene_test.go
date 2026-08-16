package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// title-scope-marker is opt-in and title-only: with the board vocabulary set,
// an OPEN task whose title carries a marker warns (case-insensitively, one
// finding per task even when several markers hit), while terminal-lane tasks
// and marker-free titles stay quiet. Off by default: furrow ships no wording.
func TestLintTitleScopeMarker(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC))
	hit, _ := a.Add("fix X (残り: docs のみ)", AddOpts{Status: "ready"})
	a.Add("plain title", AddOpts{Status: "ready"}) //nolint:errcheck // asserted via lint
	parked, _ := a.Add("park (残り: y)", AddOpts{Status: "ready"})
	a.Move(parked.ID, "icebox") //nolint:errcheck // terminal tasks are exempt

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := problemsWithCode(ps, "title-scope-marker"); len(got) != 0 {
		t.Fatalf("check must be OFF by default, got %+v", got)
	}

	a.Cfg.LintTitleScopeMarkers = []string{"残り:", "ONLY:"}
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := problemsWithCode(ps, "title-scope-marker")
	if len(got) != 1 || got[0].ID != hit.ID || got[0].Severity != core.SevWarn {
		t.Errorf("title-scope-marker = %+v, want one warn on %s (open task only)", got, hit.ID)
	}
}

// stale-inbox fires only in the DEFAULT lane, only past the configured days,
// and only when the knob is on — the inbox-zero nudge, not a general staleness
// sweep (that is [revisit].stale_days' job).
func TestLintStaleInbox(t *testing.T) {
	now := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
	a := newDueApp(now)
	old, _ := a.Add("untriaged", AddOpts{})           // default lane = inbox
	backlog, _ := a.Add("waiting is fine", AddOpts{}) // will move out of intake
	a.Move(backlog.ID, "backlog")                     //nolint:errcheck // lane change only

	// Age both: the clock moves forward 8 days past the writes above.
	a.Clock = &fixedClock{t: now.AddDate(0, 0, 8)}
	fresh, _ := a.Add("just captured", AddOpts{})

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := problemsWithCode(ps, "stale-inbox"); len(got) != 0 {
		t.Fatalf("check must be OFF by default, got %+v", got)
	}

	a.Cfg.LintStaleInboxDays = 7
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := problemsWithCode(ps, "stale-inbox")
	if len(got) != 1 || got[0].ID != old.ID || got[0].Severity != core.SevWarn {
		t.Errorf("stale-inbox = %+v, want one warn on %s (not %s in backlog, not fresh %s)",
			got, old.ID, backlog.ID, fresh.ID)
	}
}

// done-draft names a done-lane task with no repo — but only once the board
// uses the repo dimension at all, and never for icebox/waiting drafts (a
// parked idea-draft is a normal state; only finishing makes the missing
// attribution a record gap).
func TestLintDoneDraft(t *testing.T) {
	a := newDueApp(time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC))
	doneDraft, _ := a.Add("shipped, unattributed", AddOpts{})
	a.Done(doneDraft.ID) //nolint:errcheck // asserted via lint
	iceDraft, _ := a.Add("parked idea", AddOpts{})
	a.Move(iceDraft.ID, "icebox") //nolint:errcheck // must stay exempt

	// A board with NO repo anywhere does not participate: every task being a
	// draft is its normal shape, so the check must stay quiet.
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := problemsWithCode(ps, "done-draft"); len(got) != 0 {
		t.Fatalf("non-participating board must stay quiet, got %+v", got)
	}

	// One task carrying a repo flips the board into participation.
	attributed, _ := a.Add("normal", AddOpts{Repos: []string{"o/r"}})
	a.Done(attributed.ID) //nolint:errcheck // a done task WITH a repo is clean
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := problemsWithCode(ps, "done-draft")
	if len(got) != 1 || got[0].ID != doneDraft.ID || got[0].Severity != core.SevWarn {
		t.Errorf("done-draft = %+v, want one warn on %s only", got, doneDraft.ID)
	}
}
