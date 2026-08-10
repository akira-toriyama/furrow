package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func TestSetBodyReplacesAndBumpsUpdated(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	tk, err := a.Add("task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := tk.Updated

	clk.t = clk.t.Add(48 * time.Hour)

	after, err := a.SetBody(tk.ID, "# rewritten\n\nnew plan\n\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Updated.After(created) {
		t.Errorf("SetBody must advance Updated: created=%s updated=%s", created, after.Updated)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("Updated should be the write's clock time: got %s want %s", after.Updated, clk.Now())
	}

	// The old body is GONE (replace, not append), and trailing newlines
	// normalize to exactly one.
	body, _ := a.Store.LoadBody(tk.ID)
	if want := "# rewritten\n\nnew plan\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestSetBodyValidation(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	tk, _ := a.Add("task", AddOpts{})

	// An empty/whitespace replacement is exit 2 — a body is never cleared by
	// accident — and the existing body must survive the refusal.
	if _, err := a.SetBody(tk.ID, "  \n\t\n"); err == nil {
		t.Error("empty replacement should be a validation error")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("empty replacement want CodeValidation, got %v", err)
	}
	if body, _ := a.Store.LoadBody(tk.ID); body == "" {
		t.Error("a refused replacement must not have touched the body")
	}

	if _, err := a.SetBody("t-nope0", "x"); err == nil {
		t.Error("unknown id should be NotFound")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("unknown id want CodeNotFound, got %v", err)
	}
}

func TestEpicSetBodyReplacesAndBumpsUpdated(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Updated

	clk.t = clk.t.Add(24 * time.Hour)

	before, after, err := a.EpicSetBody(e.ID, "# box plan")
	if err != nil {
		t.Fatal(err)
	}
	if !before.Updated.Equal(created) {
		t.Errorf("before snapshot should carry the pre-write Updated, got %s", before.Updated)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("epic Updated should advance to the write's clock time: got %s want %s", after.Updated, clk.Now())
	}
	body, _ := a.Store.LoadBody(e.ID)
	if want := "# box plan\n"; body != want {
		t.Errorf("epic body = %q, want %q", body, want)
	}

	// The empty check runs BEFORE resolution (EpicNote's order): an empty
	// body on an unknown ref is still exit 2 for emptiness.
	if _, _, err := a.EpicSetBody("e-nope0", " "); err == nil {
		t.Error("empty replacement should fail before resolution")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("want CodeValidation, got %v", err)
	}
}
