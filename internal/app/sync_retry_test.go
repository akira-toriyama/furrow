package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

var testRebaseWait = retryPolicy{
	base:   100 * time.Millisecond,
	factor: 2,
	cap:    1600 * time.Millisecond,
	max:    6,
}

// scriptedErr yields one error per call, repeating the final element once the
// script is exhausted — so "always fails" is a one-element script and
// "fails twice then succeeds" is {race, race, nil}.
func scriptedErr(errs []error) func() error {
	i := 0
	return func() error {
		e := errs[i]
		if i < len(errs)-1 {
			i++
		}
		return e
	}
}

func TestPullWithRetry(t *testing.T) {
	race := func() error {
		return errors.Join(gitrepo.ErrTransientFetchRace, errors.New("cannot lock ref"))
	}
	conflict := &core.Error{Code: core.CodeInternal, ID: "sync-conflict", Msg: "conflict"}

	// the exact bounded-exponential sequence for testRebaseWait — pullWithRetry
	// must advance its OWN backoff via pol.next each iteration, not sit at base.
	fullBackoff := []time.Duration{100, 200, 400, 800, 1600, 1600}
	for i := range fullBackoff {
		fullBackoff[i] *= time.Millisecond
	}

	tests := []struct {
		name         string
		script       []error
		wantSleeps   int
		wantSleepSeq []time.Duration // when set, the exact backoff durations
		wantErrID    string          // "" = nil error; else the *core.Error ID we expect
		wantSame     error           // when set, the exact error value that must pass through
		wantMsgHas   string          // when set, a substring the message must contain
	}{
		{
			name:       "succeeds on the first attempt, no sleep",
			script:     []error{nil},
			wantSleeps: 0, wantErrID: "",
		},
		{
			name:       "a transient race clears after two retries",
			script:     []error{race(), race(), nil},
			wantSleeps: 2, wantErrID: "",
		},
		{
			// A race outliving the whole budget is a stale lock / permanent
			// conflict, NOT a retryable sync-busy: it fails terminally (id "sync")
			// naming the lock to remove, so an agent stops instead of looping.
			name:       "a race that never clears fails terminally, naming the stale lock",
			script:     []error{race()},
			wantSleeps: testRebaseWait.max, wantSleepSeq: fullBackoff,
			wantErrID: "sync", wantMsgHas: "stale lock",
		},
		{
			name:       "a conflict on the FIRST attempt is not transient: returned immediately",
			script:     []error{conflict},
			wantSleeps: 0, wantErrID: "sync-conflict", wantSame: conflict,
		},
		{
			// A race on attempt 1 that becomes a genuine conflict on retry (a
			// co-writer pushed a conflicting shard between attempts) must return
			// sync-conflict, never be swallowed into the retry/terminal path.
			name:       "a conflict surfacing on a retry is returned, not retried",
			script:     []error{race(), conflict},
			wantSleeps: 1, wantErrID: "sync-conflict", wantSame: conflict,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sleeps []time.Duration
			sleep := func(d time.Duration) error { sleeps = append(sleeps, d); return nil }
			err := pullWithRetry(scriptedErr(tc.script), sleep, testRebaseWait, "/board")

			if len(sleeps) != tc.wantSleeps {
				t.Errorf("sleeps = %d %v, want %d", len(sleeps), sleeps, tc.wantSleeps)
			}
			if tc.wantSleepSeq != nil && !slices.Equal(sleeps, tc.wantSleepSeq) {
				t.Errorf("backoff = %v, want %v", sleeps, tc.wantSleepSeq)
			}
			if tc.wantErrID == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if tc.wantSame != nil && err != tc.wantSame {
				t.Errorf("err = %v, want the same value passed through", err)
			}
			fe := core.AsError(err)
			if fe == nil || fe.ID != tc.wantErrID {
				t.Fatalf("err = %v, want *core.Error id %q", err, tc.wantErrID)
			}
			if fe.Code != core.CodeInternal {
				t.Errorf("code = %d, want %d (internal, not validation)", fe.Code, core.CodeInternal)
			}
			if tc.wantMsgHas != "" && !strings.Contains(fe.Msg, tc.wantMsgHas) {
				t.Errorf("message %q must contain %q", fe.Msg, tc.wantMsgHas)
			}
			// The terminal exhaustion error must be a fresh error, no longer
			// matching the transient sentinel — and never the retryable sync-busy,
			// which would loop an agent forever on a stale lock.
			if errors.Is(err, gitrepo.ErrTransientFetchRace) {
				t.Errorf("returned error must not still satisfy errors.Is(ErrTransientFetchRace)")
			}
			if fe.ID == "sync-busy" {
				t.Errorf("a persistent pull race must not classify as retryable sync-busy: %v", err)
			}
		})
	}
}

// retryPolicy.next doubles the backoff by factor and clamps it at cap.
func TestRetryPolicyNext(t *testing.T) {
	got := []time.Duration{}
	backoff := testRebaseWait.base
	for i := 0; i < testRebaseWait.max; i++ {
		got = append(got, backoff)
		backoff = testRebaseWait.next(backoff)
	}
	want := []time.Duration{100, 200, 400, 800, 1600, 1600}
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Errorf("backoff[%d] = %v, want %v", i, got[i], want[i]*time.Millisecond)
		}
	}
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
			sleep := func(d time.Duration) error { sleeps = append(sleeps, d); return nil }
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
	waitForRebaseToClear(neverClears, func(d time.Duration) error { sleeps = append(sleeps, d); return nil }, testRebaseWait)

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

// A backoff cancelled mid-wait (a Ctrl-C during the transient-race retry) must
// stop the loop and propagate the cancellation, NOT ride out the remaining budget
// and then mislabel it a stale-lock failure.
func TestPullWithRetryBailsWhenSleepCancelled(t *testing.T) {
	race := func() error { return errors.Join(gitrepo.ErrTransientFetchRace, errors.New("cannot lock ref")) }
	var sleeps int
	sleep := func(time.Duration) error { // cancelled on the 2nd backoff
		sleeps++
		if sleeps >= 2 {
			return context.Canceled
		}
		return nil
	}
	err := pullWithRetry(race, sleep, testRebaseWait, "/board")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (bail on the cancelled backoff)", err)
	}
	if sleeps != 2 {
		t.Errorf("sleeps = %d, want 2 (must bail on the cancelled backoff, not exhaust the budget)", sleeps)
	}
}

// waitForRebaseToClear likewise stops waiting out a foreign rebase the moment the
// backoff is cancelled, reporting the rebase still in progress (not cleared).
func TestWaitForRebaseBailsWhenSleepCancelled(t *testing.T) {
	neverClears := func() (string, bool) { return "rebase", true }
	var sleeps int
	sleep := func(time.Duration) error { sleeps++; return context.Canceled } // cancelled on the first backoff
	op, cleared := waitForRebaseToClear(neverClears, sleep, testRebaseWait)
	if cleared {
		t.Error("cleared = true; a cancelled wait must not report the rebase cleared")
	}
	if op != "rebase" {
		t.Errorf("op = %q, want rebase", op)
	}
	if sleeps != 1 {
		t.Errorf("sleeps = %d, want 1 (must bail on the cancelled backoff, not exhaust the budget)", sleeps)
	}
}

// interruptError collapses cancellation ARTIFACTS (a killed subprocess, a
// cancelled rev-parse mis-said as "not a git repository") into sync-interrupted,
// but must NOT mask a genuine sync-conflict: that error is a definitive outcome
// (rebase aborted, board restored via the detached AbortRebase) carrying the
// contract-promised Details.paths, so a signal racing the conflict handling must
// leave it intact.
func TestInterruptError(t *testing.T) {
	cancelled := context.Canceled
	conflict := &core.Error{Code: core.CodeInternal, ID: "sync-conflict", Msg: "conflict",
		Details: map[string]any{"paths": []string{".furrow/tasks/t-1.json"}}}
	killed := core.Internalf("sync", "git fetch: (no output)")
	notARepo := core.Validationf("sync", "x is not inside a git repository")

	tests := []struct {
		name   string
		err    error
		ctxErr error
		wantID string // expected *core.Error ID of the result ("" skips the id check)
		want   error  // when non-nil, the exact value that must pass through unchanged
	}{
		{"nil error is never reclassified", nil, cancelled, "", nil},
		{"no cancellation passes the error through", killed, nil, "", killed},
		{"a killed subprocess becomes sync-interrupted", killed, cancelled, "sync-interrupted", nil},
		{"a cancelled Open (mis-said 'not a git repo') becomes sync-interrupted", notARepo, cancelled, "sync-interrupted", nil},
		{"a real sync-conflict is preserved with its paths", conflict, cancelled, "sync-conflict", conflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := interruptError(tc.err, tc.ctxErr)
			if tc.want != nil && got != tc.want {
				t.Fatalf("got %v, want the exact error value passed through unchanged", got)
			}
			if tc.err == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if tc.wantID != "" {
				fe := core.AsError(got)
				if fe == nil || fe.ID != tc.wantID {
					t.Fatalf("got %v, want *core.Error id %q", got, tc.wantID)
				}
			}
		})
	}
}
