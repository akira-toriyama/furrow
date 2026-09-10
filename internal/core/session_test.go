package core

import (
	"testing"
	"time"
)

func TestSessionClashes(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	self := Session{PID: 100, ID: "self", CWD: "/w/glyph", StartedAt: now.Add(-10 * time.Minute)}
	repoOf := func(cwd string) string {
		switch cwd {
		case "/w/glyph":
			return "o/glyph"
		case "/w/furrow":
			return "o/furrow"
		}
		return ""
	}
	earlier := now.Add(-time.Hour)
	later := now.Add(-time.Minute)
	busy := now.Add(-30 * time.Second)
	idle := now.Add(-20 * time.Minute)

	cases := []struct {
		name   string
		self   Session
		others []Session
		repos  []string
		within time.Duration
		want   []SessionClash // only Repo/PID/Busy compared
	}{
		{"earlier busy occupant blocks", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: true}}},
		{"earlier idle occupant warns", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: idle}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"unknown activity is busy", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: true}}},
		{"an occupant whose turn ended is idle however fresh its last write", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy, TurnEnded: true}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"a turn ended with no activity record is still idle (the adapter's positive reading wins)", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, TurnEnded: true}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"a mid-turn occupant silent past the window is idle (the window is the ceiling)", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: idle, TurnEnded: false}},
			[]string{"o/glyph"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"threshold 0 never blocks a known activity", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 0,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"threshold 0 never blocks an unknown activity either (the warn-only switch)", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier}},
			[]string{"o/glyph"}, 0,
			[]SessionClash{{Repo: "o/glyph", PID: 1, Busy: false}}},
		{"later-started session never clashes (first come first served)", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: later, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"same start is unordered, no clash", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: self.StartedAt, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"self by pid is skipped", self,
			[]Session{{PID: 100, ID: "other-id", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"self by id is skipped (a resumed session under a new pid)", self,
			[]Session{{PID: 7, ID: "self", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"a repo the write does not touch is no clash", self,
			[]Session{{PID: 1, ID: "a", CWD: "/w/furrow", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"an underivable cwd occupies nothing", self,
			[]Session{{PID: 1, ID: "a", CWD: "/tmp/nowhere", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"unordered self (zero start) yields nothing", Session{PID: 100, ID: "self"},
			[]Session{{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy}},
			[]string{"o/glyph"}, 5 * time.Minute, nil},
		{"sorted by repo then start", self,
			[]Session{
				{PID: 3, ID: "c", CWD: "/w/glyph", StartedAt: earlier.Add(time.Minute), LastActive: idle},
				{PID: 2, ID: "b", CWD: "/w/furrow", StartedAt: earlier, LastActive: busy},
				{PID: 1, ID: "a", CWD: "/w/glyph", StartedAt: earlier, LastActive: busy},
			},
			[]string{"o/glyph", "o/furrow"}, 5 * time.Minute,
			[]SessionClash{{Repo: "o/furrow", PID: 2, Busy: true}, {Repo: "o/glyph", PID: 1, Busy: true}, {Repo: "o/glyph", PID: 3, Busy: false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SessionClashes(c.self, c.others, repoOf, c.repos, now, c.within)
			if len(got) != len(c.want) {
				t.Fatalf("got %d clashes, want %d: %+v", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i].Repo != c.want[i].Repo || got[i].PID != c.want[i].PID || got[i].Busy != c.want[i].Busy {
					t.Errorf("clash %d = {%s %d busy=%v}, want {%s %d busy=%v}", i, got[i].Repo, got[i].PID, got[i].Busy, c.want[i].Repo, c.want[i].PID, c.want[i].Busy)
				}
			}
		})
	}
}

func TestSessionClashIdleFields(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	self := Session{PID: 1, StartedAt: now}
	repoOf := func(string) string { return "o/r" }
	known := SessionClashes(self, []Session{{PID: 2, CWD: "x", StartedAt: now.Add(-time.Hour), LastActive: now.Add(-90 * time.Second)}}, repoOf, []string{"o/r"}, now, time.Minute)
	if len(known) != 1 || known[0].IdleSeconds == nil || *known[0].IdleSeconds != 90 || known[0].LastActive == nil || known[0].Busy {
		t.Fatalf("known activity: %+v", known)
	}
	unknown := SessionClashes(self, []Session{{PID: 2, CWD: "x", StartedAt: now.Add(-time.Hour)}}, repoOf, []string{"o/r"}, now, time.Minute)
	if len(unknown) != 1 || unknown[0].IdleSeconds != nil || unknown[0].LastActive != nil || !unknown[0].Busy {
		t.Fatalf("unknown activity: %+v", unknown)
	}
	if known[0].TurnEnded {
		t.Fatalf("TurnEnded must mirror the session, not be inferred from idleness: %+v", known)
	}
	ended := SessionClashes(self, []Session{{PID: 2, CWD: "x", StartedAt: now.Add(-time.Hour), LastActive: now.Add(-3 * time.Second), TurnEnded: true}}, repoOf, []string{"o/r"}, now, time.Minute)
	if len(ended) != 1 || !ended[0].TurnEnded || ended[0].Busy || ended[0].IdleSeconds == nil || *ended[0].IdleSeconds != 3 {
		t.Fatalf("ended turn: %+v", ended)
	}
}
