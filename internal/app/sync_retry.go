// The bounded retries a sync runs its git steps under: the pre-flight wait
// for a foreign rebase, the transient lock/ref race on the auto-commit and
// the pull, and the push race. Policy and sleep are injected; nothing here
// touches the store.

package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// retryPolicy bounds how long Sync waits out a transient concurrent-writer
// condition before giving up: a foreign rebase caught by the pre-flight, or a
// lock/ref race during the auto-commit or the pull (see retryTransient).
type retryPolicy struct {
	base   time.Duration // first backoff
	factor int           // per-attempt multiplier
	cap    time.Duration // per-sleep ceiling
	max    int           // maximum number of sleeps
}

func (pol retryPolicy) next(backoff time.Duration) time.Duration {
	if backoff *= time.Duration(pol.factor); backoff > pol.cap {
		return pol.cap
	}
	return backoff
}

// defaultConcurrentWait retries for ~4.7s (100+200+400+800+1600+1600ms) — long
// enough to ride out a concurrent writer's sub-second window (a foreign rebase,
// or a fetch racing ours), short enough that a genuinely stuck state surfaces
// promptly.
var defaultConcurrentWait = retryPolicy{
	base:   100 * time.Millisecond,
	factor: 2,
	cap:    1600 * time.Millisecond,
	max:    6,
}

// waitForRebaseToClear polls check (a mid-operation probe returning op+busy),
// sleeping with bounded exponential backoff between polls, until the repo is no
// longer mid-rebase or the policy budget is exhausted. It only waits out a
// "rebase" — the concurrent-writer signature; any other in-progress op (a
// user's own "merge") is never transient, so it returns immediately. Returns
// the last observed op and whether the repo is now clear (no op in progress).
func waitForRebaseToClear(check func() (string, bool), sleep func(time.Duration) error, pol retryPolicy) (string, bool) {
	op, busy := check()
	if !busy {
		return op, true
	}
	if op != "rebase" {
		return op, false
	}
	backoff := pol.base
	for i := 0; i < pol.max; i++ {
		if sleep(backoff) != nil {
			return op, false // cancelled while waiting out a foreign rebase — still in progress
		}
		op, busy = check()
		if !busy {
			return op, true
		}
		if op != "rebase" {
			return op, false
		}
		backoff = pol.next(backoff)
	}
	return op, false
}

// retryTransient runs once and, while it fails with a transient
// concurrent-access race (a co-writer's fetch clobbering FETCH_HEAD, or a
// ref/index lock contended in a shared checkout — gitrepo.ErrTransientRace),
// retries with bounded backoff. Both git stages of a sync go through it: the
// auto-commit (`git add`/`commit` take the index lock) and the pull. A LIVE
// race self-resolves in well under a second, so this rides it out silently in
// the common case. If it outlives the whole budget the lock is almost certainly
// STALE (a crashed git left a .git/*.lock) or the ref conflict permanent, so
// the residual is returned as a TERMINAL error naming the recovery —
// deliberately NOT the retryable "sync-busy", which would loop an agent forever
// on a stale lock (git can't tell a stale lock from a live one, but "outlived
// the retry budget" can). Any other outcome — success, a sync-conflict, a real
// error — is returned immediately and unchanged. top is the work-tree root,
// named in the recovery guidance. The commit stage used to run bare, so the
// index.lock the guidance itself names came back as a raw non-retryable
// git-failed from that half (t-cdx9).
func retryTransient(once func() error, sleep func(time.Duration) error, pol retryPolicy, top string) error {
	err := once()
	backoff := pol.base
	for i := 0; err != nil && errors.Is(err, gitrepo.ErrTransientRace) && i < pol.max; i++ {
		if serr := sleep(backoff); serr != nil {
			return serr // cancelled mid-backoff — stop retrying and propagate
		}
		err = once()
		backoff = pol.next(backoff)
	}
	if err != nil && errors.Is(err, gitrepo.ErrTransientRace) {
		return &core.Error{
			Code: core.CodeInternal,
			Kind: core.KindSyncLockStale,
			Msg: fmt.Sprintf("furrow sync kept losing a git lock/fetch race in %s across "+
				"several seconds of retries; if no other operator is syncing, a crashed git likely left a "+
				"stale lock — remove a stray .git/*.lock (e.g. .git/index.lock) in that repo, then re-run "+
				"(last error: %v)", top, err),
		}
	}
	return err
}

// pushWithRetry pushes and, if the remote moved between our pull and our push
// (gitrepo.ErrNonFastForward), pulls once more and pushes again — the co-writer
// race, which self-resolves. A push still rejected after that retry is reported
// as the RETRYABLE id "sync-push-rejected".
//
// That id exists because the distinction is not decorative. Losing this race
// leaves the board untouched and the local sync commit intact, so the fix is to
// run again — exactly like sync-busy, and unlike every other exit-3 outcome of a
// sync (sync-conflict, sync-stash-stranded, a stale ref lock), all of which are
// terminal and want a human. Folded into the generic "sync" id, as it was, a
// caller could only tell "run me again" from "stop and fix me" by matching this
// message — the one thing furrow's error contract tells callers never to do. So
// sync-task-status.yml's publish step had no honest retry policy available: it
// could retry terminal failures, or abandon a race it should ride out.
//
// A helper with injected push/pull (like retryTransient above) so the exhausted
// case is reachable in a test — as real git it needs the remote to move between
// two specific instants.
func pushWithRetry(push, pull func() error) error {
	err := push()
	if err == nil || !errors.Is(err, gitrepo.ErrNonFastForward) {
		return err
	}
	if err := pull(); err != nil {
		return err
	}
	if err := push(); err != nil {
		if !errors.Is(err, gitrepo.ErrNonFastForward) {
			return err
		}
		return &core.Error{
			Code:      core.CodeInternal,
			Kind:      core.KindSyncPushRejected,
			Retryable: true,
			Msg: fmt.Sprintf("a co-writer kept winning the push race, so the board is unchanged and "+
				"your local sync commit is intact — re-run 'furrow sync' (last error: %v)", err),
		}
	}
	return nil
}
