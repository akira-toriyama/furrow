package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// appWithClock builds an App on a memstore whose clock the test can advance, so
// a note's effect on Updated is observable (newApp's clock is fixed).
func appWithClock(start time.Time) (*App, *fixedClock) {
	cfg := config.Default()
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: start}
	return NewWithStore(st, cfg, clk), clk
}

func TestAddNoteAppendsParagraphAndBumpsUpdated(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC))
	tk, err := a.Add("task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := tk.Updated

	// Time passes before the note is written.
	clk.t = clk.t.Add(48 * time.Hour)

	after, err := a.AddNote(tk.ID, "検証完了。次: アダプタ選定。")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Updated.After(created) {
		t.Errorf("note must advance Updated: created=%s updated=%s", created, after.Updated)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("Updated should be the note's clock time: got %s want %s", after.Updated, clk.Now())
	}

	body, _ := a.Store.LoadBody(tk.ID)
	if want := "# task\n\n検証完了。次: アダプタ選定。\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}

	// A second note appends another paragraph separated by exactly one blank
	// line — and is NOT deduped even if it repeats an earlier line.
	if _, err := a.AddNote(tk.ID, "検証完了。次: アダプタ選定。"); err != nil {
		t.Fatal(err)
	}
	body, _ = a.Store.LoadBody(tk.ID)
	if n := strings.Count(body, "検証完了"); n != 2 {
		t.Errorf("a repeated note must still append (want 2 copies), got %d in:\n%s", n, body)
	}
	if strings.Contains(body, "選定。\n選定。") || strings.Contains(body, "\n\n\n") {
		t.Errorf("paragraphs must be separated by exactly one blank line:\n%q", body)
	}
}

func TestAddNoteValidation(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC))
	tk, _ := a.Add("task", AddOpts{})

	if _, err := a.AddNote(tk.ID, "   \n\t "); err == nil {
		t.Error("empty/whitespace note should be a validation error")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("empty note want CodeValidation, got %v", err)
	}

	if _, err := a.AddNote("t-nope0", "x"); err == nil {
		t.Error("unknown id should be NotFound")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("unknown id want CodeNotFound, got %v", err)
	}
}

// TestAddNoteReconcilesStaleDep is the regression for the bug that motivated the
// command: recording progress only by hand-editing the body left Updated stale,
// so lint's reconcile-gap fired on a task already reconciled in prose. A note
// advances Updated, clearing the false positive.
func TestAddNoteReconcilesStaleDep(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC))

	epic, _ := a.Add("epic", AddOpts{Status: "backlog"})
	slice, _ := a.Add("slice", AddOpts{Status: "backlog"})
	if _, err := a.AddDeps(epic.ID, []string{slice.ID}); err != nil {
		t.Fatal(err)
	}

	// The dep closes AFTER the epic's last update -> reconcile-gap fires.
	clk.t = clk.t.Add(24 * time.Hour)
	if _, err := a.Done(slice.ID); err != nil {
		t.Fatal(err)
	}
	if !hasProblem(t, a, "reconcile-gap", epic.ID) {
		t.Fatal("precondition: dep closed after epic.Updated should warn reconcile-gap")
	}

	// Recording progress with a note advances the epic's Updated past the dep's
	// Closed time -> the warning clears.
	clk.t = clk.t.Add(1 * time.Hour)
	if _, err := a.AddNote(epic.ID, "slice done; folded its result into the epic."); err != nil {
		t.Fatal(err)
	}
	if hasProblem(t, a, "reconcile-gap", epic.ID) {
		t.Error("after a note, reconcile-gap must clear for the epic")
	}
}

func hasProblem(t *testing.T, a *App, code, id string) bool {
	t.Helper()
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == code && p.ID == id {
			return true
		}
	}
	return false
}
