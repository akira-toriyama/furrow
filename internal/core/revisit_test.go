package core

import (
	"slices"
	"testing"
	"time"
)

func codesOf(rs []RevisitReason) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Code
	}
	return out
}

func TestRevisitReasons(t *testing.T) {
	p := func(n int) *int { return &n }
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -47)
	fresh := now.AddDate(0, 0, -1)
	done := map[string]bool{"t-d1": true, "t-d2": true}
	rr := []string{"o/r"} // attached to a repo, so no_repo stays quiet in repo-agnostic cases

	cases := []struct {
		name      string
		task      Task
		staleDays int
		want      []string // expected reason codes in order
	}{
		{
			name:      "fully set, fresh, no deps -> none",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: fresh},
			staleDays: 30,
			want:      []string{},
		},
		{
			name:      "value+effort unset",
			task:      Task{Repos: rr, Updated: fresh},
			staleDays: 30,
			want:      []string{RevisitValueUnset, RevisitEffortUnset},
		},
		{
			name:      "only effort unset",
			task:      Task{Repos: rr, Value: p(4), Updated: fresh},
			staleDays: 30,
			want:      []string{RevisitEffortUnset},
		},
		{
			name:      "stale fires when older than threshold",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: old},
			staleDays: 30,
			want:      []string{RevisitStale},
		},
		{
			name:      "stale boundary: exactly threshold fires",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: now.AddDate(0, 0, -30)},
			staleDays: 30,
			want:      []string{RevisitStale},
		},
		{
			name:      "stale boundary: just under threshold stays quiet",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: now.AddDate(0, 0, -29)},
			staleDays: 30,
			want:      []string{},
		},
		{
			name:      "staleDays<=0 disables stale",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: old},
			staleDays: 0,
			want:      []string{},
		},
		{
			name:      "one dep_done per done dep, in dep order",
			task:      Task{Repos: rr, Value: p(3), Effort: p(2), Updated: fresh, Deps: []string{"t-open", "t-d2", "t-d1"}},
			staleDays: 30,
			want:      []string{RevisitDepDone, RevisitDepDone},
		},
		{
			name:      "all signals combine in canonical order",
			task:      Task{Repos: rr, Updated: old, Deps: []string{"t-d1"}},
			staleDays: 30,
			want:      []string{RevisitValueUnset, RevisitEffortUnset, RevisitStale, RevisitDepDone},
		},
		{
			// A draft (repos == []) surfaces on that alone: fully estimated,
			// fresh, no deps — no_repo is still reported.
			name:      "draft -> no_repo fires alone",
			task:      Task{Value: p(3), Effort: p(2), Updated: fresh},
			staleDays: 30,
			want:      []string{RevisitNoRepo},
		},
		{
			name:      "no_repo leads the canonical order when combined",
			task:      Task{Updated: old, Deps: []string{"t-d1"}},
			staleDays: 30,
			want:      []string{RevisitNoRepo, RevisitValueUnset, RevisitEffortUnset, RevisitStale, RevisitDepDone},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RevisitReasons(c.task, now, c.staleDays, done)
			if !slices.Equal(codesOf(got), c.want) {
				t.Errorf("codes = %v, want %v", codesOf(got), c.want)
			}
		})
	}
}

func TestRevisitReasonsDepDoneNamesDep(t *testing.T) {
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	done := map[string]bool{"t-d1": true}
	rs := RevisitReasons(Task{Repos: []string{"o/r"}, Value: ptr(1), Effort: ptr(1), Updated: now, Deps: []string{"t-d1"}}, now, 30, done)
	if len(rs) != 1 || rs[0].Code != RevisitDepDone {
		t.Fatalf("want one dep_done, got %v", rs)
	}
	if want := "dep t-d1 is done"; rs[0].Detail != want {
		t.Errorf("detail = %q, want %q", rs[0].Detail, want)
	}
}

func TestRevisitReasonsDepDoneOrder(t *testing.T) {
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	done := map[string]bool{"t-d1": true, "t-d2": true}
	// The emitted order follows Deps, not the done map's iteration order — that is
	// what makes this assertion deterministic at all.
	rs := RevisitReasons(Task{Repos: []string{"o/r"}, Value: ptr(1), Effort: ptr(1), Updated: now, Deps: []string{"t-open", "t-d2", "t-d1"}}, now, 30, done)
	want := []RevisitReason{
		{Code: RevisitDepDone, Detail: "dep t-d2 is done"},
		{Code: RevisitDepDone, Detail: "dep t-d1 is done"},
	}
	if len(rs) != len(want) {
		t.Fatalf("got %d reasons, want %d: %+v", len(rs), len(want), rs)
	}
	for i, w := range want {
		if rs[i] != w {
			t.Errorf("reason[%d] = %+v, want %+v", i, rs[i], w)
		}
	}
}

// A task in a parked lane revisit reads raises only the two signals that say
// its wait may be over, and stale is withheld while the wait has a clock of
// its own (a due still ahead in a lane that tracks dues, or a dep not yet
// done) — the reads that already bring such a task back.
func TestParkedRevisitReasons(t *testing.T) {
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -47)
	fresh := now.AddDate(0, 0, -1)
	ahead := now.AddDate(0, 0, 10)
	arrived := now.AddDate(0, 0, -2)
	done := map[string]bool{"t-d1": true, "t-d2": true}

	cases := []struct {
		name      string
		task      Task
		staleDays int
		dueCounts bool
		want      []string
	}{
		{"untouched, no due, no dep -> stale", Task{Updated: old}, 30, true, []string{RevisitStale}},
		{"touched inside the window -> none", Task{Updated: fresh}, 30, true, []string{}},
		{"no repo and no estimates are an open task's signals", Task{Updated: fresh}, 30, true, []string{}},
		{"a due still ahead is the wait's own clock", Task{Updated: old, Due: &ahead}, 30, true, []string{}},
		{"a due ahead in a lane [due] ignores never speaks, so stale does", Task{Updated: old, Due: &ahead}, 30, false, []string{RevisitStale}},
		{"an arrived due no longer holds stale back", Task{Updated: old, Due: &arrived}, 30, true, []string{RevisitStale}},
		{"an unfinished dep is the wait's own clock", Task{Updated: old, Deps: []string{"t-open"}}, 30, true, []string{}},
		{"a dep that closed -> stale and dep_done", Task{Updated: old, Deps: []string{"t-d1"}}, 30, true, []string{RevisitStale, RevisitDepDone}},
		{"one dep closed, one not -> dep_done only", Task{Updated: old, Deps: []string{"t-d1", "t-open"}}, 30, true, []string{RevisitDepDone}},
		{"dep_done does not wait for the stale window", Task{Updated: fresh, Deps: []string{"t-d1", "t-d2"}}, 30, true, []string{RevisitDepDone, RevisitDepDone}},
		{"stale_days 0 disables stale, not dep_done", Task{Updated: old, Deps: []string{"t-d1"}}, 0, true, []string{RevisitDepDone}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := ParkedRevisitReasons(c.task, now, c.staleDays, done, c.dueCounts)
			if rs == nil {
				t.Fatal("reasons must be [] not nil, like RevisitReasons")
			}
			if got := codesOf(rs); !slices.Equal(got, c.want) {
				t.Errorf("codes = %v, want %v", got, c.want)
			}
		})
	}
}
