package app

import (
	"testing"
	"time"
)

var testRebaseWait = retryPolicy{
	base:   100 * time.Millisecond,
	factor: 2,
	cap:    1600 * time.Millisecond,
	max:    6,
}

// scriptedCheck yields one (op,busy) step per call, repeating the final step
// once the script is exhausted — so "never clears" is a one-element script.
func scriptedCheck(script [][2]any) func() (string, bool) {
	i := 0
	return func() (string, bool) {
		op, _ := script[i][0].(string)
		busy, _ := script[i][1].(bool)
		if i < len(script)-1 {
			i++
		}
		return op, busy
	}
}

func TestWaitForRebaseToClear(t *testing.T) {
	tests := []struct {
		name        string
		script      [][2]any
		wantOp      string
		wantCleared bool
		wantSleeps  int
	}{
		{
			name:   "clear on first check needs no sleep",
			script: [][2]any{{"", false}},
			wantOp: "", wantCleared: true, wantSleeps: 0,
		},
		{
			name:   "transient rebase clears after two polls",
			script: [][2]any{{"rebase", true}, {"rebase", true}, {"", false}},
			wantOp: "", wantCleared: true, wantSleeps: 2,
		},
		{
			name:   "rebase that never clears exhausts the budget",
			script: [][2]any{{"rebase", true}},
			wantOp: "rebase", wantCleared: false, wantSleeps: 6,
		},
		{
			name:   "a merge is not retried",
			script: [][2]any{{"merge", true}},
			wantOp: "merge", wantCleared: false, wantSleeps: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sleeps []time.Duration
			sleep := func(d time.Duration) { sleeps = append(sleeps, d) }
			op, cleared := waitForRebaseToClear(scriptedCheck(tc.script), sleep, testRebaseWait)
			if op != tc.wantOp || cleared != tc.wantCleared {
				t.Errorf("got (op=%q cleared=%v), want (op=%q cleared=%v)", op, cleared, tc.wantOp, tc.wantCleared)
			}
			if len(sleeps) != tc.wantSleeps {
				t.Errorf("sleeps = %d %v, want %d", len(sleeps), sleeps, tc.wantSleeps)
			}
		})
	}
}

// The backoff is exponential from base, doubling, clamped at cap, for max sleeps.
func TestWaitForRebaseBackoffSequence(t *testing.T) {
	neverClears := func() (string, bool) { return "rebase", true }
	var sleeps []time.Duration
	waitForRebaseToClear(neverClears, func(d time.Duration) { sleeps = append(sleeps, d) }, testRebaseWait)

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		1600 * time.Millisecond, // clamped at cap
	}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Errorf("sleep[%d] = %v, want %v", i, sleeps[i], want[i])
		}
	}
}
