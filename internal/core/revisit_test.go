package core

import (
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

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRevisitReasons(t *testing.T) {
	p := func(n int) *int { return &n }
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -47) // 47 days ago
	fresh := now.AddDate(0, 0, -1)
	done := map[string]bool{"t-d1": true, "t-d2": true}

	cases := []struct {
		name      string
		task      Task
		staleDays int
		want      []string // expected reason codes in order
	}{
		{
			name:      "fully set, fresh, no deps -> none",
			task:      Task{Value: p(3), Effort: p(2), Updated: fresh},
			staleDays: 30,
			want:      []string{},
		},
		{
			name:      "value+effort unset",
			task:      Task{Updated: fresh},
			staleDays: 30,
			want:      []string{RevisitValueUnset, RevisitEffortUnset},
		},
		{
			name:      "only effort unset",
			task:      Task{Value: p(4), Updated: fresh},
			staleDays: 30,
			want:      []string{RevisitEffortUnset},
		},
		{
			name:      "stale fires when older than threshold",
			task:      Task{Value: p(3), Effort: p(2), Updated: old},
			staleDays: 30,
			want:      []string{RevisitStale},
		},
		{
			name:      "stale boundary: exactly threshold fires",
			task:      Task{Value: p(3), Effort: p(2), Updated: now.AddDate(0, 0, -30)},
			staleDays: 30,
			want:      []string{RevisitStale},
		},
		{
			name:      "staleDays<=0 disables stale",
			task:      Task{Value: p(3), Effort: p(2), Updated: old},
			staleDays: 0,
			want:      []string{},
		},
		{
			name:      "one dep_done per done dep, in dep order",
			task:      Task{Value: p(3), Effort: p(2), Updated: fresh, Deps: []string{"t-open", "t-d2", "t-d1"}},
			staleDays: 30,
			want:      []string{RevisitDepDone, RevisitDepDone},
		},
		{
			name:      "all signals combine in canonical order",
			task:      Task{Updated: old, Deps: []string{"t-d1"}},
			staleDays: 30,
			want:      []string{RevisitValueUnset, RevisitEffortUnset, RevisitStale, RevisitDepDone},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RevisitReasons(c.task, now, c.staleDays, done)
			if !eqStrs(codesOf(got), c.want) {
				t.Errorf("codes = %v, want %v", codesOf(got), c.want)
			}
		})
	}
}

func TestRevisitReasonsDepDoneNamesDep(t *testing.T) {
	now := time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC)
	done := map[string]bool{"t-d1": true}
	rs := RevisitReasons(Task{Value: intptr(1), Effort: intptr(1), Updated: now, Deps: []string{"t-d1"}}, now, 30, done)
	if len(rs) != 1 || rs[0].Code != RevisitDepDone {
		t.Fatalf("want one dep_done, got %v", rs)
	}
	if want := "dep t-d1 is done"; rs[0].Detail != want {
		t.Errorf("detail = %q, want %q", rs[0].Detail, want)
	}
}

func intptr(n int) *int { return &n }
