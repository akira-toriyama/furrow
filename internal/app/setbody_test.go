package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

func TestSetBodyReplacesAndBumpsUpdated(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)))
	tk, err := a.Add("task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := tk.Updated

	clk.t = clk.t.Add(48 * time.Hour)

	after, _, err := a.SetBody(tk.ID, "# rewritten\n\nnew plan\n\n\n")
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
	a, _ := newAppWith(at(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)))
	tk, _ := a.Add("task", AddOpts{})

	// An empty/whitespace replacement is exit 2 — a body is never cleared by
	// accident — and the existing body must survive the refusal.
	if _, _, err := a.SetBody(tk.ID, "  \n\t\n"); err == nil {
		t.Error("empty replacement should be a validation error")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("empty replacement want CodeValidation, got %v", err)
	}
	if body, _ := a.Store.LoadBody(tk.ID); body == "" {
		t.Error("a refused replacement must not have touched the body")
	}

	if _, _, err := a.SetBody("t-nope0", "x"); err == nil {
		t.Error("unknown id should be NotFound")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("unknown id want CodeNotFound, got %v", err)
	}
}

func TestEpicSetBodyReplacesAndBumpsUpdated(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)))
	e, err := a.EpicAdd("box", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Updated

	clk.t = clk.t.Add(24 * time.Hour)

	before, after, _, err := a.EpicSetBody(e.ID, "# box plan")
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
	if _, _, _, err := a.EpicSetBody("e-nope0", " "); err == nil {
		t.Error("empty replacement should fail before resolution")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("want CodeValidation, got %v", err)
	}
}

// identicalBodies are replacements equal to the seeded "# same\n" once
// normalizeBody collapses trailing newlines — what a front-end writing back an
// unchanged $EDITOR buffer sends (t-3zyn).
var identicalBodies = map[string]string{
	"byte-identical":         "# same\n",
	"extra trailing newline": "# same\n\n\n",
	"no trailing newline":    "# same",
}

// An unchanged replacement writes nothing: no body write, no journal entry, no
// `updated` stamp — the staleness clock survives. A real change afterwards
// still writes, journals and stamps.
func TestSetBodyIdenticalWritesNothing(t *testing.T) {
	for name, text := range identicalBodies {
		t.Run(name, func(t *testing.T) {
			a, st := newProseFailApp()
			tk, err := a.Add("same", AddOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if body, _ := a.Store.LoadBody(tk.ID); body != "# same\n" {
				t.Fatalf("seed body = %q, want %q", body, "# same\n")
			}
			a.bodiesTouched, st.bodyCalls = nil, 0
			was := updatedOf(t, a, tk.ID)
			advance(t, a, 48*time.Hour)

			after, replaced, err := a.SetBody(tk.ID, text)
			if err != nil {
				t.Fatal(err)
			}
			if replaced {
				t.Error("an identical replacement must report replaced=false")
			}
			if !after.Updated.Equal(was) {
				t.Errorf("returned updated moved on an identical body: %s -> %s", was, after.Updated)
			}
			if got := updatedOf(t, a, tk.ID); !got.Equal(was) {
				t.Errorf("stored updated moved on an identical body: %s -> %s", was, got)
			}
			if st.bodyCalls != 0 {
				t.Errorf("an identical body must not be written; SaveBody ran %d time(s)", st.bodyCalls)
			}
			if a.bodiesTouched[tk.ID] {
				t.Error("an identical body must not enter the body journal")
			}
			if body, _ := a.Store.LoadBody(tk.ID); body != "# same\n" {
				t.Errorf("body bytes changed: %q", body)
			}

			after, replaced, err = a.SetBody(tk.ID, "# same\n\nnew plan")
			if err != nil {
				t.Fatal(err)
			}
			if !replaced || st.bodyCalls != 1 || !a.bodiesTouched[tk.ID] {
				t.Errorf("a real change must write and journal: replaced=%t writes=%d journaled=%t", replaced, st.bodyCalls, a.bodiesTouched[tk.ID])
			}
			if !after.Updated.Equal(a.Clock.Now()) {
				t.Errorf("a real change must stamp updated: got %s want %s", after.Updated, a.Clock.Now())
			}
		})
	}
}

// The box twin: EpicSetBody leaves an unchanged body, the journal and the
// box's clock alone, and still stamps a real change.
func TestEpicSetBodyIdenticalWritesNothing(t *testing.T) {
	for name, text := range identicalBodies {
		t.Run(name, func(t *testing.T) {
			a, st := newProseFailApp()
			e, err := a.EpicAdd("same", EpicAddOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if body, _ := a.Store.LoadBody(e.ID); body != "# same\n" {
				t.Fatalf("seed body = %q, want %q", body, "# same\n")
			}
			a.bodiesTouched, st.bodyCalls = nil, 0
			advance(t, a, 48*time.Hour)

			before, after, replaced, err := a.EpicSetBody(e.ID, text)
			if err != nil {
				t.Fatal(err)
			}
			if replaced {
				t.Error("an identical replacement must report replaced=false")
			}
			if !before.Updated.Equal(e.Updated) || !after.Updated.Equal(e.Updated) {
				t.Errorf("box updated moved on an identical body: %s -> %s", e.Updated, after.Updated)
			}
			if stored, _, _ := a.Store.LoadEpic(e.ID); !stored.Updated.Equal(e.Updated) {
				t.Errorf("stored box updated moved on an identical body: %s -> %s", e.Updated, stored.Updated)
			}
			if st.bodyCalls != 0 {
				t.Errorf("an identical body must not be written; SaveBody ran %d time(s)", st.bodyCalls)
			}
			if a.bodiesTouched[e.ID] {
				t.Error("an identical body must not enter the body journal")
			}
			if body, _ := a.Store.LoadBody(e.ID); body != "# same\n" {
				t.Errorf("body bytes changed: %q", body)
			}

			_, after, replaced, err = a.EpicSetBody(e.ID, "# same\n\nnew plan")
			if err != nil {
				t.Fatal(err)
			}
			if !replaced || st.bodyCalls != 1 || !a.bodiesTouched[e.ID] {
				t.Errorf("a real change must write and journal: replaced=%t writes=%d journaled=%t", replaced, st.bodyCalls, a.bodiesTouched[e.ID])
			}
			if !after.Updated.Equal(a.Clock.Now()) {
				t.Errorf("a real change must stamp the box: got %s want %s", after.Updated, a.Clock.Now())
			}
		})
	}
}
