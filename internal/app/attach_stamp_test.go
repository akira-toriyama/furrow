package app

import (
	"testing"
	"time"
)

// A body line is prose: attach stamps `updated` like a note does (the shard
// cannot see a body, so without the stamp the change was invisible to the
// staleness clock — t-jg8x), and a line already present stamps nothing.
func TestAttachAndBodyAppendStampUpdatedOnceAndIdempotently(t *testing.T) {
	a, clk := newAppWith()
	tk, err := a.Add("bug", AddOpts{Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	was := tk.Updated
	clk.t = clk.t.Add(48 * time.Hour)
	if _, err := a.Attach(tk.ID, "shot.png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	after, _, err := a.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Updated.After(was) {
		t.Fatalf("attach left updated at %s", after.Updated)
	}
	clk.t = clk.t.Add(48 * time.Hour)
	changed, err := a.AppendBody(tk.ID, "- merged in #1")
	if err != nil || !changed {
		t.Fatalf("first append: changed=%v err=%v", changed, err)
	}
	stamped, _, _ := a.Get(tk.ID)
	if !stamped.Updated.After(after.Updated) {
		t.Fatalf("an appended line left updated at %s", stamped.Updated)
	}
	clk.t = clk.t.Add(48 * time.Hour)
	changed, err = a.AppendBody(tk.ID, "- merged in #1")
	if err != nil || changed {
		t.Fatalf("second append of the same line: changed=%v err=%v", changed, err)
	}
	again, _, _ := a.Get(tk.ID)
	if !again.Updated.Equal(stamped.Updated) {
		t.Fatalf("a no-op append moved updated to %s", again.Updated)
	}
}
