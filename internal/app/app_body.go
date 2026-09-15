// Prose: bodies are written FIRST and stamp `updated` unconditionally (the shard
// cannot see a body), and every write goes through saveBody/saveBodies so it
// rides the per-checkout body journal.

package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// EditPath ensures the body file of the entity ref names exists (creating an
// empty one if needed) and returns its absolute path for the CLI to hand to
// $EDITOR.
//
// ref may name a TASK or an EPIC, routed by RefTargetsEpic — store membership,
// never the id's prefix. There is nothing to specialize beyond the lookup: both
// entities' prose lives in the one bodies/ directory (core.Epic.Body), so the
// path this returns is the same kind of file either way. An unresolvable ref
// fails as the entity its prefix suggests: NotFound (exit 1) for a task-shaped
// one, the box resolver's exit 2 + candidates for an `e-` one.
func (a *App) EditPath(ref string) (string, error) {
	id := ref
	epic, err := a.RefTargetsEpic(ref)
	if err != nil {
		return "", err
	}
	// Guarded like every other write: the path handed out is for $EDITOR to
	// write the body through, and the empty body minted below is a write of
	// furrow's own — one that used to land in an occupied repo and ride the
	// next sync (t-8sgn: the one write path on neither the guarded nor the
	// exempt list).
	if epic {
		if id, err = a.ResolveEpic(ref); err != nil {
			return "", err
		}
		e, ok, err := a.Store.LoadEpic(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", core.NotFound(id)
		}
		if err := a.guardRepos(id, e.Repos, ""); err != nil {
			return "", err
		}
	} else {
		idx, lerr := a.load()
		if lerr != nil {
			return "", lerr
		}
		t, i := idx.Find(id)
		if i < 0 {
			return "", a.notFoundTask(id)
		}
		if err := a.guardTask(t); err != nil {
			return "", err
		}
	}
	if !a.Store.BodyExists(id) {
		if err := a.saveBody(id, ""); err != nil {
			return "", err
		}
	}
	p := a.Store.BodyFile(id)
	if p == "" {
		return "", core.Internalf(id, "this store is not file-backed; cannot edit")
	}
	return p, nil
}

// AddNote appends text as a new paragraph to a task's body AND stamps Updated,
// in one call — the body is written first, then the shard. It is the in-band way
// to record progress / stop-points / next steps across sessions. The two writes
// are NOT transactional (this per-file-rename store has no cross-file txn), but
// the ordering is the safe one: if the shard write fails after the body was
// written, the note is saved without Updated advancing — a partial failure costs
// a timestamp, never content, and a re-run re-appends and fixes Updated.
//
// It differs from AppendBody (the `apply` annotation helper) on both counts,
// deliberately: AppendBody dedupes an identical line and leaves Updated alone,
// because a re-run of `apply` for the same PR event must be idempotent. A note
// is the opposite — a progress note repeating an earlier one is still a distinct
// event, so it always appends — and it MUST move Updated, because the body is
// the task's content. Hand-editing the file (via EditPath) does not touch the
// shard, which lets Updated go stale and makes lint's reconcile-gap (a dep's
// Closed time vs. Updated) misfire on a task whose progress was recorded only in
// prose; a note keeps Updated honest, so that check stays trustworthy.
//
// NotFound (exit 1) when id names no task; an empty/whitespace-only note is a
// validation error (exit 2).
func (a *App) AddNote(id, text string) (*core.Task, error) {
	text, err := normalizeNote(id, text)
	if err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	// Prose: the body goes first (a partial failure costs a timestamp, never
	// content), and the stamp is unconditional — the shard cannot see a body.
	return a.mutateInErr(idx, id, func(t *core.Task) error {
		if err := a.appendBody(id, text); err != nil {
			return err
		}
		t.Updated = a.Clock.Now()
		return nil
	})
}

// normalizeNote trims a note's trailing newlines and rejects an
// empty/whitespace note as bad usage (never a silent no-op append). Shared by
// AddNote and the done --note paths so the two can never diverge on what
// counts as a note.
func normalizeNote(id, text string) (string, error) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", core.Validationf(id, "note text is empty")
	}
	return text, nil
}

// SetBody replaces a task's ENTIRE body AND stamps Updated, in one call — the
// non-interactive replacement path (`furrow edit <id> --body`). AddNote covers
// the append case; this is the substitute for hand-editing the file EditPath
// hands out, which leaves `updated` stale and makes the staleness signals
// (revisit/lint reconcile-gap) blind to real progress. The write order matches
// AddNote's for the same reason: body first, shard second, so a partial
// failure costs a timestamp, never content, and a re-run replaces again and
// fixes Updated.
//
// An empty/whitespace-only body is a validation error (exit 2) — a caller
// piping an unset variable or a closed stream means a bug, and a body is
// never cleared by accident. NotFound (exit 1) when id names no task.
func (a *App) SetBody(id, text string) (*core.Task, error) {
	text, err := normalizeBody(id, text)
	if err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return a.mutateInErr(idx, id, func(t *core.Task) error { // AddNote's order and stamp
		if err := a.saveBody(id, text); err != nil {
			return err
		}
		t.Updated = a.Clock.Now()
		return nil
	})
}

// normalizeBody normalizes a REPLACEMENT body: trailing newlines collapse to
// exactly one (the marshaller's trailing-newline discipline, applied to
// prose), and an empty/whitespace-only body is bad usage — replacing is never
// clearing. Shared by SetBody and EpicSetBody so the two entities can never
// diverge on what counts as a body.
func normalizeBody(id, text string) (string, error) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", core.Validationf(id, "replacement body is empty; a body is never cleared this way (append progress with `furrow note` instead)")
	}
	return text + "\n", nil
}

// appendBody appends text to a task's body as a new paragraph, separated from
// existing content by exactly one blank line, whatever the body's current
// trailing whitespace.
func (a *App) appendBody(id, text string) error {
	next, err := a.appendedBody(id, text)
	if err != nil {
		return err
	}
	return a.saveBody(id, next)
}

// appendedBody composes what appendBody would write — the body with text as a
// new paragraph — without writing it, so a batch can compose every body first
// and land them all in one SaveBodies.
func (a *App) appendedBody(id, text string) (string, error) {
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(body)
	if body != "" {
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		if !strings.HasSuffix(body, "\n\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(text)
	b.WriteString("\n")
	return b.String(), nil
}

// markBodyTouched records that this process created, modified, or deleted the
// body of task id — the set AutoCommitFlush passes as SyncOpts.Bodies so
// autocommit commits the command's OWN body edits even when the file is already
// tracked. Cheap by design so every body write can call it unconditionally,
// whether or not autocommit is on.
func (a *App) markBodyTouched(id string) {
	if a.bodiesTouched == nil {
		a.bodiesTouched = map[string]bool{}
	}
	a.bodiesTouched[id] = true
}

// saveBody persists a task's body through the store AND records the id as
// touched. It is the app-layer funnel for every HOT-store body write; the
// archive substore's bodies land as machine-written archive/ paths (outside
// bodies/) and so need no tracking.
func (a *App) saveBody(id, body string) error {
	if err := a.Store.SaveBody(id, body); err != nil {
		return err
	}
	a.markBodyTouched(id)
	return nil
}

// saveBodies is saveBody over a batch, through the store's one-step write: a
// failure leaves every body as it was (see core.Store.SaveBodies), which is
// what lets a batch close carry a note without breaking all-or-nothing.
func (a *App) saveBodies(bodies map[string]string) error {
	if err := a.Store.SaveBodies(bodies); err != nil {
		return err
	}
	for id := range bodies {
		a.markBodyTouched(id)
	}
	return nil
}

// deleteBody removes a task's body through the store AND records the id as
// touched, so an archive's hot-side body deletion rides in the SAME autocommit
// as the archive/ copy it was moved to — rather than being classified as a
// tracked-dirty pending body and left out of the commit.
func (a *App) deleteBody(id string) error {
	if err := a.Store.DeleteBody(id); err != nil {
		return err
	}
	a.markBodyTouched(id)
	return nil
}
