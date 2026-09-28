package core

import (
	"strings"
	"testing"
	"time"
)

// The day arithmetic the move and the D-N rendering rest on: a bare day parses
// in UTC (no zone can change which day it is), the delta between two days is
// whole calendar days, and a due's offset is counted on the day it falls on IN
// THE BOARD'S CALENDAR — a bare `--due 2026-11-07` is 23:59:59 JST, which is
// the 7th there and the 7th's UTC noon-ish elsewhere, never the 8th.
func TestAnchorArithmetic(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	if _, err := ParseAnchor(" 2026-11-21 "); err != nil {
		t.Fatalf("a padded day must parse: %v", err)
	}
	for _, bad := range []string{"", "2026-13-45", "2026-11-21T10:00", "next week", "21/11/2026"} {
		if _, err := ParseAnchor(bad); err == nil || !strings.Contains(err.Error(), AnchorSpelling) {
			t.Errorf("ParseAnchor(%q) = %v, want a validation error naming %s", bad, err, AnchorSpelling)
		}
	}
	for _, c := range []struct {
		from, to string
		want     int
	}{
		{"2026-11-21", "2026-11-28", 7},
		{"2026-11-28", "2026-11-21", -7},
		{"2026-11-21", "2026-11-21", 0},
		{"2026-02-27", "2026-03-02", 3}, // across February, no leap day in 2026
		{"2024-02-27", "2024-03-02", 4}, // with one
	} {
		got, err := AnchorDays(c.from, c.to)
		if err != nil || got != c.want {
			t.Errorf("AnchorDays(%s, %s) = %d, %v; want %d", c.from, c.to, got, err, c.want)
		}
	}
	due := time.Date(2026, 11, 7, 23, 59, 59, 0, jst) // the END of the 7th in JST = 14:59:59Z
	if got, _ := AnchorOffsetDays("2026-11-21", due, jst); got != -14 {
		t.Errorf("D-14 read in the board's calendar = %d", got)
	}
	if got, _ := AnchorOffsetDays("2026-11-21", due, time.UTC); got != -14 {
		t.Errorf("the same instant is still the 7th in UTC = %d", got)
	}
	early := time.Date(2026, 11, 8, 3, 0, 0, 0, jst) // 18:00Z on the 7th, the 8th in JST
	if got, _ := AnchorOffsetDays("2026-11-21", early, jst); got != -13 {
		t.Errorf("an instant on the 8th in the board's calendar must count as the 8th = %d", got)
	}
	if got, _ := AnchorOffsetDays("2026-11-21", early, time.UTC); got != -14 {
		t.Errorf("and as the 7th in UTC = %d", got)
	}
	for days, want := range map[int]string{-14: "D-14", 0: "D0", 3: "D+3"} {
		if got := FormatAnchorOffset(days); got != want {
			t.Errorf("FormatAnchorOffset(%d) = %s, want %s", days, got, want)
		}
	}
}

// Every anchor-* finding is a state the write paths refuse, so each is planted
// straight into the structs — a hand-edit — and the done lane is exempt on the
// task side (its due is history; a move never touches it).
func TestAnchorProblems(t *testing.T) {
	due := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	epics := []Epic{
		{ID: "e-day", Anchor: "2026-11-21"},
		{ID: "e-bare"},
		{ID: "e-bad", Anchor: "someday"},
	}
	idx := &Index{Tasks: []Task{
		{ID: "t-ok", Status: "ready", Anchor: "e-day", Due: &due},
		{ID: "t-gone", Status: "ready", Anchor: "e-nope", Due: &due},
		{ID: "t-unset", Status: "ready", Anchor: "e-bare", Due: &due},
		{ID: "t-undated", Status: "ready", Anchor: "e-day"},
		{ID: "t-repeats", Status: "ready", Anchor: "e-day", Due: &due, Repeat: "FREQ=WEEKLY", RepeatAnchor: &due},
		{ID: "t-history", Status: "done", Anchor: "e-nope"},
		{ID: "t-plain", Status: "ready", Due: &due},
	}}
	ps := AnchorProblems(idx, epics, "done")
	got := map[string]string{}
	for _, p := range ps {
		got[p.ID+"/"+p.Code] = p.Severity
	}
	want := map[string]string{
		"e-bad/anchor-invalid":       SevError,
		"t-gone/anchor-missing":      SevError,
		"t-unset/anchor-unset":       SevWarn,
		"t-undated/anchor-undated":   SevWarn,
		"t-repeats/anchor-on-repeat": SevWarn,
	}
	for k, sev := range want {
		if got[k] != sev {
			t.Errorf("missing %s (%s); got %v", k, sev, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra findings: got %v", got)
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Severity > ps[i].Severity {
			t.Errorf("findings not sorted errors-first: %v", ps)
		}
	}
	for _, p := range ps {
		if !strings.Contains(p.Msg, "furrow ") {
			t.Errorf("%s names no remedy: %s", p.Code, p.Msg)
		}
		if strings.Contains(p.Msg, AnchorLayout) {
			t.Errorf("%s leaks the Go layout instead of %s: %s", p.Code, AnchorSpelling, p.Msg)
		}
	}
}

// The pair round-trips through the one marshaller path and stays absent from
// a shard that never carried it — the omitempty half of the flag-day case.
func TestAnchorRoundTrip(t *testing.T) {
	tk := &Task{ID: "t-a", Title: "x", Anchor: "e-day"}
	b, err := MarshalTask(tk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"anchor": "e-day"`) {
		t.Errorf("task anchor not written:\n%s", b)
	}
	back, err := UnmarshalTask(b)
	if err != nil || back.Anchor != "e-day" {
		t.Errorf("task anchor round-trip = %q, %v", back.Anchor, err)
	}
	plain, _ := MarshalTask(&Task{ID: "t-b", Title: "y"})
	if strings.Contains(string(plain), "anchor") {
		t.Errorf("a task with no anchor must not carry the key:\n%s", plain)
	}
	e := &Epic{ID: "e-day", Title: "box", Anchor: "2026-11-21"}
	eb, err := MarshalEpic(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eb), `"anchor": "2026-11-21"`) {
		t.Errorf("epic anchor not written:\n%s", eb)
	}
	eback, err := UnmarshalEpic(eb)
	if err != nil || eback.Anchor != "2026-11-21" {
		t.Errorf("epic anchor round-trip = %q, %v", eback.Anchor, err)
	}
	if plainE, _ := MarshalEpic(&Epic{ID: "e-x", Title: "z"}); strings.Contains(string(plainE), "anchor") {
		t.Errorf("an epic with no anchor must not carry the key:\n%s", plainE)
	}
}
