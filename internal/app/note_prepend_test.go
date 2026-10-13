package app

import (
	"testing"
	"time"
)

// prependedBody is the one placement rule behind `note --prepend`: after the
// H1 heading when the first real line is one (blank lines and a successor's
// back-link may sit above it), else at the very top; exactly one blank line on
// each side of the new paragraph; the rest of the body byte-for-byte.
func TestPrependedBodyPlacement(t *testing.T) {
	cases := []struct {
		name, body, text, want string
	}{
		{"after heading, before existing prose", "# t\n\nold plan\n", "new", "# t\n\nnew\n\nold plan\n"},
		{"heading only", "# t\n", "new", "# t\n\nnew\n"},
		{"heading, extra blank lines collapse to one", "# t\n\n\n\nold\n", "new", "# t\n\nnew\n\nold\n"},
		{"heading directly followed by prose", "# t\nold\n", "new", "# t\n\nnew\n\nold\n"},
		{"empty body", "", "new", "new\n"},
		{"whitespace-only body", "\n\n", "new", "new\n"},
		{"no heading: top of the body", "old first\n\nmore\n", "new", "new\n\nold first\n\nmore\n"},
		{"H2 is not a title: top of the body", "## section\n\nold\n", "new", "new\n\n## section\n\nold\n"},
		{"#foo is not a heading", "#foo\nold\n", "new", "new\n\n#foo\nold\n"},
		{"back-link above the heading stays above", "previous: [[t-0001]]\n# t\n\nold\n", "new", "previous: [[t-0001]]\n# t\n\nnew\n\nold\n"},
		{"blank lines above the heading are kept", "\n# t\n\nold\n", "new", "\n# t\n\nnew\n\nold\n"},
		{"body without trailing newline", "# t\n\nold", "new", "# t\n\nnew\n\nold\n"},
		{"multi-line paragraph", "# t\n\nold\n", "line A\nline B", "# t\n\nline A\nline B\n\nold\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := prependedBody(c.body, c.text); got != c.want {
				t.Errorf("prependedBody(%q, %q) =\n%q\nwant\n%q", c.body, c.text, got, c.want)
			}
		})
	}
}

// PrependNote is AddNote in every respect but placement: the body is written,
// Updated moves to the clock, a repeat is not deduped (newest first), and an
// empty note is bad usage.
func TestPrependNoteWritesFirstAndBumpsUpdated(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)))
	tk, err := a.Add("task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddNote(tk.ID, "older progress"); err != nil {
		t.Fatal(err)
	}
	created := tk.Updated
	clk.t = clk.t.Add(time.Hour)

	after, err := a.PrependNote(tk.ID, "decision: plan B\n")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Updated.After(created) || !after.Updated.Equal(clk.Now()) {
		t.Errorf("Updated = %s, want the clock %s (after %s)", after.Updated, clk.Now(), created)
	}
	body, _ := a.Store.LoadBody(tk.ID)
	if want := "# task\n\ndecision: plan B\n\nolder progress\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}

	if _, err := a.PrependNote(tk.ID, "decision: plan B"); err != nil {
		t.Fatal(err)
	}
	body, _ = a.Store.LoadBody(tk.ID)
	if want := "# task\n\ndecision: plan B\n\ndecision: plan B\n\nolder progress\n"; body != want {
		t.Errorf("a repeated prepend is a distinct event (never deduped); body = %q", body)
	}

	if _, err := a.PrependNote(tk.ID, " \n"); err == nil {
		t.Error("an empty prepend must be a validation error, not a silent write")
	}
	if _, err := a.PrependNote("t-nope", "x"); err == nil {
		t.Error("an unknown id must fail")
	}
}

// The box twin shares the placement rule and stamps the epic's Updated.
func TestEpicPrependNoteWritesFirst(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)))
	e, err := a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicNote(e.ID, "older"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(time.Hour)
	_, after, err := a.EpicPrependNote(e.ID, "newest")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("epic Updated = %s, want %s", after.Updated, clk.Now())
	}
	body, _ := a.Store.LoadBody(e.ID)
	if want := "# box\n\nnewest\n\nolder\n"; body != want {
		t.Errorf("epic body = %q, want %q", body, want)
	}
}
