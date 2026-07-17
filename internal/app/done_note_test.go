package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestDoneNoteClosesAndAppendsInOneCall(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	tk, err := a.Add("task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := tk.Updated

	clk.t = clk.t.Add(48 * time.Hour)

	after, err := a.DoneNote(tk.ID, "→ continued in t-xxxxx")
	if err != nil {
		t.Fatalf("DoneNote: %v", err)
	}
	if after.Status != a.Cfg.DoneLane || after.Closed == nil {
		t.Errorf("task = %q/closed %v; want done lane with Closed stamped", after.Status, after.Closed)
	}
	if !after.Updated.After(created) || !after.Updated.Equal(clk.Now()) {
		t.Errorf("Updated should be the close's clock time: got %s want %s", after.Updated, clk.Now())
	}

	body, _ := a.Store.LoadBody(tk.ID)
	if want := "# task\n\n→ continued in t-xxxxx\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestDoneNoteValidation(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	tk, _ := a.Add("task", AddOpts{})

	// An empty/whitespace note is bad usage — done without a note is a
	// different command line, never a silent no-note close.
	if _, err := a.DoneNote(tk.ID, "  \n\t "); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty note want CodeValidation, got %v", err)
	}
	if cur, _, _ := a.Get(tk.ID); cur.Status == a.Cfg.DoneLane {
		t.Errorf("a rejected note must not close the task")
	}

	// Unknown id is the classic single-id NotFound (no batch envelope).
	if _, err := a.DoneNote("t-nope0", "x"); core.ExitCode(err) != int(core.CodeNotFound) {
		t.Errorf("unknown id want CodeNotFound, got %v", err)
	}
}

func TestDoneManyNoteAppendsToEveryTask(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	t1, _ := a.Add("one", AddOpts{})
	t2, _ := a.Add("two", AddOpts{})

	got, err := a.DoneManyNote([]string{t1.ID, t2.ID}, "shipped in furrow#152")
	if err != nil {
		t.Fatalf("DoneManyNote: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("results = %+v; want both tasks", got)
	}
	for _, tk := range got {
		if tk.Status != a.Cfg.DoneLane || tk.Closed == nil {
			t.Errorf("%s = %q/closed %v; want done lane with Closed stamped", tk.ID, tk.Status, tk.Closed)
		}
		body, _ := a.Store.LoadBody(tk.ID)
		if !strings.Contains(body, "\n\nshipped in furrow#152\n") {
			t.Errorf("%s body missing the note paragraph:\n%q", tk.ID, body)
		}
	}
}

func TestDoneManyNoteIsAllOrNothing(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC))
	t1, _ := a.Add("one", AddOpts{})

	_, err := a.DoneManyNote([]string{t1.ID, "t-nope"}, "note")
	if core.ExitCode(err) != int(core.CodeNotFound) {
		t.Fatalf("miss should be NotFound, got %v", err)
	}
	fe := core.AsError(err)
	miss, _ := fe.Details.(map[string]any)["missing"].([]string)
	if strings.Join(miss, ",") != "t-nope" {
		t.Errorf("details.missing = %v, want the miss", fe.Details)
	}
	// Nothing half-lands: the found task keeps its lane AND its body.
	if cur, _, _ := a.Get(t1.ID); cur.Status == a.Cfg.DoneLane {
		t.Errorf("t1 closed despite the failed batch; all-or-nothing broken")
	}
	if body, _ := a.Store.LoadBody(t1.ID); strings.Contains(body, "note") {
		t.Errorf("t1 body gained the note despite the failed batch:\n%q", body)
	}

	// The empty-note guard covers the batch path too.
	if _, err := a.DoneManyNote([]string{t1.ID}, " "); core.ExitCode(err) != int(core.CodeValidation) {
		t.Errorf("empty note want CodeValidation, got %v", err)
	}
}
