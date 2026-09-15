// The single-task mutation skeleton (mutate → mutateInPost) and the mutators
// built on it. Every task write that edits ONE task goes through the skeleton:
// find, guard both sides, snapshot, edit, stamp when the shard moved, save.

package app

import (
	"bytes"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// moveOne moves one task and returns the series report a close produces when
// the task carries a repeat rule (nil for every other move) — `apply`'s
// per-directive close.
func (a *App) moveOne(id, lane string) (*core.Task, *RepeatReport, error) {
	if !a.Cfg.IsLane(lane) {
		return nil, nil, a.unknownLaneErr(id, lane)
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	var rep *RepeatReport
	var succ *pendingSuccessor
	saved, err := a.mutateInPost(idx, id, func(t *core.Task) error {
		r, s, rerr := a.planRepeat(idx, t, t.Status, lane, a.Clock.Now(), nil)
		if rerr != nil {
			return rerr
		}
		if r != nil {
			consumeRepeat(t)
		}
		rep, succ = r, s
		a.applyLane(t, lane)
		return nil
	}, func(ix *core.Index) error {
		return a.flushSuccessors(ix, []*pendingSuccessor{succ})
	})
	if err != nil {
		return nil, nil, err
	}
	return saved, rep, nil
}

// applyLane sets t.Status to lane and keeps Closed consistent: it stamps Closed
// on entering the done lane (when unset — also backfilling a zombie), clears it
// on leaving done, and leaves it alone for other terminal lanes (parked ≠
// closed). Shared by Move and Set so the two can never diverge on the rule.
func (a *App) applyLane(t *core.Task, lane string) {
	was := t.Status
	t.Status = lane
	switch {
	case lane == a.Cfg.DoneLane && t.Closed == nil:
		now := a.Clock.Now()
		t.Closed = &now
	case lane != a.Cfg.DoneLane && was == a.Cfg.DoneLane:
		t.Closed = nil
	}
}

// CheckLane validates a lane name against the configured vocabulary — the
// exit-2 candidates error move/add raise, exposed for a caller that must vet
// the lane BEFORE other work (the CLI's selection preview: previewing a write
// against a lane the apply would refuse would be a lie).
func (a *App) CheckLane(lane string) error {
	if !a.Cfg.IsLane(lane) {
		return a.unknownLaneErr("", lane)
	}
	return nil
}

// Reorder sets a task's absolute priority.
func (a *App) Reorder(id string, priority int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Priority = priority })
}

// ReorderRelative places id immediately before (or after) ref in ref's lane,
// without the caller computing a priority. Both tasks must share a lane. When
// the sparse gap next to ref is exhausted, the whole lane is respaced in the
// SAME write (plan first, then apply, single Save — all-or-nothing like the dep
// commands), and the neighbors' moves are returned so the CLI can report them.
// Only id's Updated advances: a respace is positional bookkeeping on the
// neighbors, not progress, so it must not disturb staleness signals.
func (a *App) ReorderRelative(id, ref string, before bool) (*core.Task, []core.PriorityChange, error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	target, changes, err := idx.PlanRelativePriority(id, ref, before, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
	if err != nil {
		return nil, nil, err
	}
	// The neighbours a respace moves are edited directly, outside the stamp:
	// positional bookkeeping, not progress, so their `updated` stays put.
	saved, err := a.mutateIn(idx, id, func(t *core.Task) {
		t.Priority = target
		for _, c := range changes {
			ct, _ := idx.Find(c.ID)
			ct.Priority = c.To
		}
	})
	if err != nil {
		return nil, nil, err
	}
	return saved, changes, nil
}

// SetValue records a task's value estimate, or clears it when v is nil (back to
// "unset", so triage stays frictionless). An out-of-range score is clamped into
// 1..5 on write by the marshaller. The pointer is copied so a later clamp can't
// reach back into the caller's variable.
func (a *App) SetValue(id string, v *int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Value = cloneIntp(v) })
}

// SetEffort records a task's effort estimate, or clears it when v is nil. Same
// clamp/copy semantics as SetValue.
func (a *App) SetEffort(id string, v *int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Effort = cloneIntp(v) })
}

// SetTitle renames a task's one-line summary. It touches only the shard; use
// Retitle from the CLI so the body's heading is kept in step.
func (a *App) SetTitle(id, title string) (*core.Task, error) {
	title = core.NormalizeTitle(title)
	if title == "" {
		return nil, core.Validationf(id, "title must not be empty")
	}
	if why := core.TitleTooLong(title); why != "" {
		return nil, core.Validationf(id, "%s", why)
	}
	return a.mutate(id, func(t *core.Task) { t.Title = title })
}

// Retitle renames a task and keeps the two homes of a title in step: the shard's
// title field (the source of truth) and the body's leading `# ` heading. Before
// this, a title lived in both places with no command to change it, so a rename
// meant hand-editing the shard AND the body — and once shards became
// furrow-owned, hand-editing them was off-limits entirely. Retitle syncs the
// body heading, then writes the shard — the prose paths' order (AddNote), so a
// failed body write leaves the title where the heading still is. A body whose
// first line is not an H1 is left untouched (there is no second home to drift);
// an empty body is seeded a heading, mirroring add.
//
// A heading rewrite is PROSE and stamps `updated` unconditionally, like every
// other body write: the shard's own bytes cannot see it, so a retitle to the
// title the shard already holds — the case after a hand-edited heading drifted
// — used to move the body and leave the clock alone, invisible to is:stale,
// revisit and reconcile-gap (t-wdm8).
func (a *App) Retitle(id, title string) (*core.Task, error) {
	title = core.NormalizeTitle(title)
	if title == "" {
		return nil, core.Validationf(id, "title must not be empty")
	}
	if why := core.TitleTooLong(title); why != "" {
		return nil, core.Validationf(id, "%s", why)
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.mutateInErr(idx, id, func(t *core.Task) error {
		body, err := a.Store.LoadBody(id)
		if err != nil {
			return err
		}
		if next, changed := retitleHeading(body, title); changed {
			if err := a.saveBody(id, next); err != nil {
				return err
			}
			t.Updated = a.Clock.Now() // prose moved: stamp unconditionally
		}
		t.Title = title
		return nil
	})
}

// retitleHeading rewrites body's leading ATX H1 heading to `# <title>`, returning
// the new body and whether it changed. The heading is the first non-blank line
// when that line is an H1 — a single `#` then a space, so `##` and `#foo` are not
// treated as one — and only its text is replaced; everything after is preserved
// byte-for-byte. An empty (or whitespace-only) body is seeded `# <title>`,
// matching add. A non-empty body whose first non-blank line is not an H1 is
// returned unchanged: the title then lives only in the shard, with nothing to
// keep in sync. The one line allowed ABOVE the heading is a generated
// occurrence's `previous: [[id]]` back-link (successorBody puts it on line 1),
// otherwise every successor of a repeating task would keep its stale heading
// through a retitle. Operates on LF-delimited markdown (furrow's on-disk form).
func retitleHeading(body, title string) (string, bool) {
	want := "# " + title
	if strings.TrimSpace(body) == "" {
		return want + "\n", true
	}
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" || bodyLinkLine.MatchString(ln) {
			continue // skip leading blank lines and a successor's back-link before the heading
		}
		if !strings.HasPrefix(ln, "# ") {
			return body, false // first real line isn't an H1 — leave the body alone
		}
		if ln == want {
			return body, false
		}
		lines[i] = want
		return strings.Join(lines, "\n"), true
	}
	return body, false
}

// Relabel adds and/or removes labels on a task. Adding a label already present,
// and removing one already absent, are both no-ops (idempotent) so re-runs don't
// churn the diff. A call with neither --add nor --rm is a bad-usage error
// rather than a silent no-op. When [labels].required is set, a relabel that would
// leave the task with zero labels is rejected. The marshaller keeps the stored
// label set sorted and de-duplicated, so the in-memory order here doesn't matter.
func (a *App) Relabel(id string, add, remove []string) (*core.Task, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, core.Validationf(id, "provide at least one --add or --rm label")
	}
	if err := requireNonBlank(id, "--add", add); err != nil {
		return nil, err
	}
	if err := requireNonBlank(id, "--rm", remove); err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	next := labelDelta(t.Labels, add, remove)
	if a.Cfg.LabelsRequired && len(next) == 0 {
		return nil, core.Validationf(id, "a label is required ([labels].required); this relabel would remove the last one")
	}
	return a.mutateIn(idx, id, func(t *core.Task) { t.Labels = next })
}

// Reref adds and/or removes refs (file:line or URL pointers) on a task, the
// after-the-fact edit for what `add --ref` sets at creation. Adding a ref
// already present, and removing one already absent, are both no-ops
// (idempotent) so re-runs don't churn the diff; a call with neither --add nor
// --rm is a bad-usage error rather than a silent no-op. Unlike labels, refs
// are a user-ordered SEQUENCE (the marshaller deliberately does not sort
// them), so survivors keep their order and adds append at the end.
func (a *App) Reref(id string, add, remove []string) (*core.Task, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, core.Validationf(id, "provide at least one --add or --rm ref")
	}
	if err := requireNonBlank(id, "--add", add); err != nil {
		return nil, err
	}
	if err := requireNonBlank(id, "--rm", remove); err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	next := labelDelta(t.Refs, add, remove)
	return a.mutateIn(idx, id, func(t *core.Task) { t.Refs = next })
}

// labelDelta returns cur with every entry in remove dropped and every entry in
// add unioned on (idempotent — an add already present, or a remove already
// absent, is a no-op). Survivors keep their order, then the adds; the marshaller
// sorts+dedupes on write, so in-memory order is immaterial. Shared by Relabel
// and Set.
func labelDelta(cur, add, remove []string) []string {
	rm := make(map[string]bool, len(remove))
	for _, l := range remove {
		rm[l] = true
	}
	next := make([]string, 0, len(cur)+len(add))
	for _, l := range cur {
		if !rm[l] {
			next = append(next, l)
		}
	}
	for _, l := range add {
		if !contains(next, l) {
			next = append(next, l)
		}
	}
	return next
}

// mutate loads, finds, applies fn, stamps Updated when fn actually changed
// something, and saves — the common shape of every single-task edit. Returns the
// updated task.
func (a *App) mutate(id string, fn func(*core.Task)) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.mutateIn(idx, id, fn)
}

// mutateIn is mutate minus the load: it applies fn to a task in an index the
// caller ALREADY holds, then stamps and saves on the same terms.
//
// It exists because a verb that must VALIDATE against the task before editing it
// (a checklist index in range, a relabel that must not empty a required label
// set, a repo arg resolved against the board's repo universe) used to check
// against one snapshot and then call mutate, which loaded a SECOND one. Two
// loads, two snapshots, no lock between them — so `check`/`check --rm` wrote
// `t.Checklist[item]` and sliced `t.Checklist[:item]` against a list that a
// concurrent writer may have shortened in between, which is an out-of-range
// panic rather than an error. Validating and applying against one snapshot is
// what closes that, and halving the read is the free part.
func (a *App) mutateIn(idx *core.Index, id string, fn func(*core.Task)) (*core.Task, error) {
	return a.mutateInErr(idx, id, func(t *core.Task) error { fn(t); return nil })
}

// mutateInErr is mutateIn for an edit that can REFUSE. The distinction matters
// for the one edit that reads the store while mutating — a close that has to
// mint the next occurrence of a repeating task — since its failure must leave
// the index untouched, and a func() with no error could only panic or lie.
func (a *App) mutateInErr(idx *core.Index, id string, fn func(*core.Task) error) (*core.Task, error) {
	return a.mutateInPost(idx, id, fn, nil)
}

// mutateInPost is mutateInErr with a hook that runs after the edit is stamped
// and BEFORE the write. It exists for the one edit that must also ADD a task —
// a close that mints the next occurrence — because core.Index holds tasks by
// value: appending can move the backing array, so the insert has to happen once
// every *core.Task pointer this function holds is done being used.
func (a *App) mutateInPost(idx *core.Index, id string, fn func(*core.Task) error, post func(*core.Index) error) (*core.Task, error) {
	t, i := idx.Find(id)
	if i < 0 {
		return nil, a.notFoundTask(id)
	}
	// Guarded BEFORE fn as well as after it (mutateEpicStamping's shape): the
	// prose paths' fn writes the body — a note appended, a body replaced, a
	// heading synced — and a refusal after that would have already landed the
	// prose. The registry is read once, so the second call costs nothing.
	if err := a.guardTask(t); err != nil {
		return nil, err
	}
	before, err := core.MarshalTask(t)
	if err != nil {
		return nil, err
	}
	reposBefore := append([]string(nil), t.Repos...)
	if err := fn(t); err != nil {
		return nil, err
	}
	// The guard sees the edit's both sides (a repo attached or detached by fn),
	// and a refusal here leaves the store untouched: nothing has been saved.
	if err := a.guardRepos(id, unionRepos(reposBefore, t.Repos), ""); err != nil {
		return nil, err
	}
	if err := a.stampIfChanged(t, before); err != nil {
		return nil, err
	}
	if post != nil {
		if err := post(idx); err != nil {
			return nil, err
		}
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// stampIfChanged advances t.Updated only when the edit actually changed what
// gets PERSISTED. before is the shard bytes as they stood before the edit — the
// very bytes Store.Save would have written — so the question asked is exactly
// "will this task's shard differ?", canonicalization included: adding a label
// the task already carries, re-setting a value to its current score, moving to
// the lane it is already in, or renaming to the same title all compare equal,
// and any unknown key the passthrough carries is compared along with the rest.
//
// Why the STAMP and not the Save: Store.Save already writes only the shards
// whose bytes changed, so a no-op mutation churned git for exactly one reason —
// the unconditional Updated stamp made the bytes differ. Skip the stamp and the
// Save becomes a genuine no-op by itself, which is why every caller keeps
// calling it unconditionally: a path that also moved a NEIGHBOR (reorder's
// respace) must still persist that even when the target itself did not move.
//
// This is what the idempotence the list-editing write paths already promise in
// prose ("re-runs don't churn the diff") actually costs.
// Updated is the clock `is:stale`, revisit's stale signal, lint's reconcile-gap
// and `ls --since` all read, so an agent's idempotent retry must not reset it —
// and on a shared board each retry was one more commit for everyone to sync.
//
// The paths that write PROSE (note, done --note, a body replacement) stamp
// unconditionally and deliberately: the body is the task's content but lives
// outside the shard, so its bytes can never show up in this comparison.
func (a *App) stampIfChanged(t *core.Task, before []byte) error {
	changed, err := shardChanged(t, before)
	if err != nil {
		return err
	}
	if changed {
		t.Updated = a.Clock.Now()
	}
	return nil
}

// shardChanged is the comparison itself, for the batch paths: they stamp every
// task they touched with ONE instant, so they decide the stamp themselves rather
// than letting each task read the clock a second apart.
func shardChanged(t *core.Task, before []byte) (bool, error) {
	after, err := core.MarshalTask(t)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(before, after), nil
}

// stampEpicIfChanged is stampIfChanged's box twin, over an epic's shard bytes.
func (a *App) stampEpicIfChanged(e *core.Epic, before []byte) error {
	after, err := core.MarshalEpic(e)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	e.Updated = a.Clock.Now()
	return nil
}
