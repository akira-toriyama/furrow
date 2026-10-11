package core

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// mergeFixture is the t-v1fg shape: a task both sides last saw open, then the
// bot closed it while a session ticked its checklist, each stamping `updated`
// in its own second.
func mergeFixture(t *testing.T) (base, bot, session []byte, t0 time.Time) {
	t.Helper()
	t0 = time.Date(2026, 10, 5, 4, 47, 0, 0, time.UTC)
	task := func(mut func(*Task)) []byte {
		tk := &Task{ID: "t-kbjr", Title: "shared", Status: "in-progress", Priority: 10,
			Checklist: []ChecklistItem{{Text: "step", Done: false}},
			Created:   t0, Updated: t0, Body: "bodies/t-kbjr.md"}
		mut(tk)
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	base = task(func(*Task) {})
	bot = task(func(tk *Task) {
		closed := t0.Add(41 * time.Second)
		tk.Status, tk.Closed, tk.Updated = "done", &closed, closed
	})
	session = task(func(tk *Task) {
		tk.Checklist[0].Done = true
		tk.Updated = t0.Add(42 * time.Second)
	})
	return base, bot, session, t0
}

func mustTask(t *testing.T, data []byte) *Task {
	t.Helper()
	tk, err := UnmarshalTask(data)
	if err != nil {
		t.Fatalf("merged shard does not decode: %v\n%s", err, data)
	}
	return tk
}

// Disjoint fields merge: the bot's close and the session's tick both land,
// `updated` is the later stamp, and the bytes are exactly what MarshalTask
// writes (so the next save is zero-churn).
func TestMergeShardDisjointFieldsBothLand(t *testing.T) {
	base, bot, session, t0 := mergeFixture(t)
	for name, sides := range map[string][2][]byte{"ours=bot": {bot, session}, "ours=session": {session, bot}} {
		t.Run(name, func(t *testing.T) {
			merged, conflicts, err := MergeShard(ShardTask, base, sides[0], sides[1])
			if err != nil || len(conflicts) > 0 {
				t.Fatalf("err=%v conflicts=%v", err, conflicts)
			}
			tk := mustTask(t, merged)
			if tk.Status != "done" || tk.Closed == nil || !tk.Closed.Equal(t0.Add(41*time.Second)) {
				t.Errorf("bot's close lost: status=%q closed=%v", tk.Status, tk.Closed)
			}
			if len(tk.Checklist) != 1 || !tk.Checklist[0].Done {
				t.Errorf("session's tick lost: %+v", tk.Checklist)
			}
			if !tk.Updated.Equal(t0.Add(42 * time.Second)) {
				t.Errorf("updated = %v; want the later stamp", tk.Updated)
			}
			again, err := MarshalTask(tk)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again, merged) {
				t.Errorf("merge result is not canonical:\n%s\nvs\n%s", merged, again)
			}
		})
	}
}

// The symmetric case must not depend on which side git calls ours.
func TestMergeShardIsSymmetric(t *testing.T) {
	base, bot, session, _ := mergeFixture(t)
	a, _, err := MergeShard(ShardTask, base, bot, session)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := MergeShard(ShardTask, base, session, bot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("merge depends on side order:\n%s\nvs\n%s", a, b)
	}
}

// The same key set to different values on both sides is a real conflict: no
// merged bytes, the key named, no error.
func TestMergeShardSameFieldDifferentValuesConflicts(t *testing.T) {
	base, _, _, t0 := mergeFixture(t)
	retitle := func(title string, at time.Time) []byte {
		tk := mustTask(t, base)
		tk.Title, tk.Updated = title, at
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	merged, conflicts, err := MergeShard(ShardTask, base, retitle("A wins", t0.Add(time.Second)), retitle("B wins", t0.Add(2*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if merged != nil {
		t.Errorf("a conflicted merge must return no bytes, got:\n%s", merged)
	}
	if len(conflicts) != 1 || conflicts[0] != "title" {
		t.Errorf("conflicts = %v; want [title] (updated resolves by clock, title does not)", conflicts)
	}
}

// Both sides closed the task (a PR footer's apply and a session's `done`):
// status agrees, the first close is the close, and the later `updated` wins.
func TestMergeShardDoubleCloseKeepsFirstClose(t *testing.T) {
	base, _, _, t0 := mergeFixture(t)
	closeAt := func(at time.Time) []byte {
		tk := mustTask(t, base)
		tk.Status, tk.Closed, tk.Updated = "done", &at, at
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := t0.Add(41*time.Second), t0.Add(42*time.Second)
	merged, conflicts, err := MergeShard(ShardTask, base, closeAt(second), closeAt(first))
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
	tk := mustTask(t, merged)
	if tk.Closed == nil || !tk.Closed.Equal(first) {
		t.Errorf("closed = %v; want the first close %v", tk.Closed, first)
	}
	if !tk.Updated.Equal(second) {
		t.Errorf("updated = %v; want the later stamp %v", tk.Updated, second)
	}
}

// A reopen on one side against a re-close on the other is a disagreement about
// the task, not about clocks: `closed` null vs a stamp conflicts.
func TestMergeShardReopenVersusCloseConflicts(t *testing.T) {
	base, bot, _, t0 := mergeFixture(t)
	reopened := func() []byte {
		tk := mustTask(t, bot)
		tk.Status, tk.Closed, tk.Updated = "ready", nil, t0.Add(50*time.Second)
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}()
	reclosed := func() []byte {
		tk := mustTask(t, bot)
		at := t0.Add(60 * time.Second)
		tk.Closed, tk.Updated = &at, at
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}()
	_, conflicts, err := MergeShard(ShardTask, bot, reopened, reclosed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(conflicts, ",") != "closed" {
		t.Errorf("conflicts = %v; want [closed] (status changed on one side only, so it merges)", conflicts)
	}
	_ = base
}

// A key one side REMOVED (omitempty: `due` cleared) and the other left alone
// stays removed; a key one side ADDED lands — presence is a value like any other.
func TestMergeShardAddedAndRemovedKeys(t *testing.T) {
	base, _, _, t0 := mergeFixture(t)
	due := t0.Add(24 * time.Hour)
	withDue := func() []byte {
		tk := mustTask(t, base)
		tk.Due = &due
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}()
	cleared := func() []byte { // from withDue: due removed, updated advanced
		tk := mustTask(t, withDue)
		tk.Due, tk.Updated = nil, t0.Add(time.Second)
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}()
	labelled := func() []byte { // from withDue: a label added, updated advanced
		tk := mustTask(t, withDue)
		tk.Labels, tk.Updated = []string{"bug"}, t0.Add(2*time.Second)
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}()
	merged, conflicts, err := MergeShard(ShardTask, withDue, cleared, labelled)
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
	tk := mustTask(t, merged)
	if tk.Due != nil {
		t.Errorf("the cleared due came back: %v", tk.Due)
	}
	if len(tk.Labels) != 1 || tk.Labels[0] != "bug" {
		t.Errorf("the added label was lost: %v", tk.Labels)
	}
}

// Unknown keys (a newer binary's field) survive the merge exactly as the
// passthrough would carry them, and a side that adds one is honoured as a
// change like any other.
func TestMergeShardPreservesUnknownKeys(t *testing.T) {
	base, bot, session, _ := mergeFixture(t)
	withFuture := []byte(strings.TrimSuffix(string(session), "}\n") + `,"blocked":true}` + "\n")
	merged, conflicts, err := MergeShard(ShardTask, base, bot, withFuture)
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
	tk := mustTask(t, merged)
	if keys := tk.ExtraKeys(); len(keys) != 1 || keys[0] != "blocked" {
		t.Errorf("unknown key dropped by the merge: extras=%v\n%s", keys, merged)
	}
	if tk.Status != "done" {
		t.Errorf("the known-field merge must still happen beside the unknown key: status=%q", tk.Status)
	}
}

// A hand-edit's whitespace is not a change: the same value pretty-printed
// differently compares equal.
func TestMergeShardIgnoresWhitespaceDifferences(t *testing.T) {
	base, bot, session, _ := mergeFixture(t)
	var loose bytes.Buffer
	if err := json.Indent(&loose, session, "", "\t"); err != nil {
		t.Fatal(err)
	}
	_, conflicts, err := MergeShard(ShardTask, base, bot, loose.Bytes())
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
}

// No common ancestor (both sides created the shard): equal content merges,
// divergent content conflicts on the differing keys.
func TestMergeShardWithoutBase(t *testing.T) {
	_, bot, session, _ := mergeFixture(t)
	if _, conflicts, err := MergeShard(ShardTask, nil, bot, bot); err != nil || len(conflicts) > 0 {
		t.Errorf("identical both-added shards must merge: err=%v conflicts=%v", err, conflicts)
	}
	_, conflicts, err := MergeShard(ShardTask, nil, bot, session)
	if err != nil {
		t.Fatal(err)
	}
	// updated resolves by clock; every other differing key is a conflict.
	if strings.Join(conflicts, ",") != "checklist,closed,status" {
		t.Errorf("conflicts = %v; want [checklist closed status]", conflicts)
	}
}

// A side that is not a shard at all is a validation error, never a merge.
func TestMergeShardRejectsNonObjects(t *testing.T) {
	base, bot, _, _ := mergeFixture(t)
	for _, bad := range [][]byte{[]byte(`[]`), []byte(`null`), []byte(`{"id":`), []byte(`"x"`)} {
		if _, _, err := MergeShard(ShardTask, base, bot, bad); err == nil {
			t.Errorf("MergeShard(..., %s) must fail", bad)
		}
	}
	// A valid object the task decoder refuses (a type mismatch) is an error too.
	broken := []byte(strings.TrimSuffix(string(bot), "}\n") + `,"nothing":1}` + "\n")
	broken = bytes.Replace(broken, []byte(`"priority": 10`), []byte(`"priority": "ten"`), 1)
	if _, _, err := MergeShard(ShardTask, base, bot, broken); err == nil {
		t.Error("a merge the task decoder refuses must surface as an error")
	}
}

// The epic and repo kinds canonicalise through their own marshallers.
func TestMergeShardEpicAndRepoKinds(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	epic := func(mut func(*Epic)) []byte {
		e := &Epic{ID: "e-k3m9", Title: "box", Created: t0, Updated: t0}
		mut(e)
		data, err := MarshalEpic(e)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	base := epic(func(*Epic) {})
	retitled := epic(func(e *Epic) { e.Title, e.Updated = "box!", t0.Add(time.Second) })
	closedAt := t0.Add(2 * time.Second)
	closed := epic(func(e *Epic) { e.Closed, e.Updated = &closedAt, closedAt })
	merged, conflicts, err := MergeShard(ShardEpic, base, retitled, closed)
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
	e, err := UnmarshalEpic(merged)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "box!" || e.Closed == nil || !e.Updated.Equal(closedAt) {
		t.Errorf("epic merge lost a side: %+v", e)
	}

	repo := func(mut func(*RepoRecord)) []byte {
		r := &RepoRecord{Repo: "akira-toriyama/furrow"}
		mut(r)
		data, err := MarshalRepo(r)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	rbase := repo(func(*RepoRecord) {})
	human := repo(func(r *RepoRecord) { at := t0; r.LastReviewed = &at })
	agent := repo(func(r *RepoRecord) { at := t0.Add(time.Second); r.LastAgentReviewed = &at })
	rmerged, conflicts, err := MergeShard(ShardRepo, rbase, human, agent)
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("err=%v conflicts=%v", err, conflicts)
	}
	rr, err := UnmarshalRepo(rmerged)
	if err != nil {
		t.Fatal(err)
	}
	if rr.LastReviewed == nil || rr.LastAgentReviewed == nil {
		t.Errorf("repo merge lost a side: %+v", rr)
	}
}

// A close of a REPEATING task is never merged: closing mints the successor
// from the closer's base, so a double close is two series and an edit racing a
// close is lost on the predecessor. Both must stay a conflict (review of #451).
func TestMergeShardNeverMergesACloseOfARepeatingTask(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	task := func(mut func(*Task)) []byte {
		due := t0.Add(24 * time.Hour)
		tk := &Task{ID: "t-rep", Title: "weekly", Status: "inbox", Priority: 10, Created: t0, Updated: t0,
			Body: "bodies/t-rep.md", Due: &due, Repeat: "FREQ=WEEKLY", RepeatAnchor: &due}
		mut(tk)
		data, err := MarshalTask(tk)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	base := task(func(*Task) {})
	closeAt := func(at time.Time) []byte {
		return task(func(tk *Task) {
			tk.Status, tk.Closed, tk.Updated, tk.Repeat, tk.RepeatAnchor = "done", &at, at, "", nil
		})
	}
	snoozed := task(func(tk *Task) { d := t0.Add(48 * time.Hour); tk.Due, tk.Updated = &d, t0.Add(time.Second) })

	for name, sides := range map[string][2][]byte{
		"double close":    {closeAt(t0.Add(41 * time.Second)), closeAt(t0.Add(42 * time.Second))},
		"close vs snooze": {closeAt(t0.Add(41 * time.Second)), snoozed},
		"snooze vs close": {snoozed, closeAt(t0.Add(41 * time.Second))},
	} {
		merged, conflicts, err := MergeShard(ShardTask, base, sides[0], sides[1])
		if err != nil {
			t.Fatal(err)
		}
		if merged != nil || !slices.Contains(conflicts, "closed") {
			t.Errorf("%s: merged=%v conflicts=%v; want no bytes and a closed conflict", name, merged != nil, conflicts)
		}
	}
	// Two snoozes of the same series do not involve the close and still conflict
	// on due, as any same-field disagreement does; a snooze against a label is fine.
	labelled := task(func(tk *Task) { tk.Labels, tk.Updated = []string{"chore"}, t0.Add(2*time.Second) })
	if _, conflicts, err := MergeShard(ShardTask, base, snoozed, labelled); err != nil || len(conflicts) > 0 {
		t.Errorf("a snooze beside a label on a repeating task must merge: err=%v conflicts=%v", err, conflicts)
	}
}

// An empty or identity-less side cannot merge into a nameless shard.
func TestMergeShardRejectsEmptyAndIdentitylessSides(t *testing.T) {
	base, bot, _, _ := mergeFixture(t)
	// Against a changed side, an emptied side is refused as conflicts (every
	// changed key disagrees); against an unchanged side it would merge into a
	// nameless shard, which the canonicaliser refuses as an error.
	for _, bad := range [][]byte{nil, []byte(``), []byte(`{}`), []byte(`{"title":"x"}`)} {
		if merged, conflicts, err := MergeShard(ShardTask, base, bot, bad); err == nil && len(conflicts) == 0 {
			t.Errorf("MergeShard(bot, %q) must refuse, got:\n%s", bad, merged)
		}
		if merged, _, err := MergeShard(ShardTask, base, base, bad); err == nil {
			t.Errorf("MergeShard(base, %q) must fail, got:\n%s", bad, merged)
		}
	}
	if _, _, err := MergeShard(ShardRepo, nil, []byte(`{"repo":""}`), []byte(`{"repo":""}`)); err == nil {
		t.Error("a repo merge naming no repo must fail")
	}
}
