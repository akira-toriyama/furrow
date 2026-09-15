// What a pull brought IN: the task changes other machines and CI wrote,
// classified from the pre-pull..post-pull shard tree-diff. Read-only display
// data — any failure drops an entry, never the sync.

package app

import (
	"context"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// The incoming kinds, one closed vocabulary (`furrow vocab incoming-kinds`):
// a shard that appeared is created; a modification is classified by the FIRST
// match of closed | reopened | moved | refiled | updated (a `done` both closes
// and moves; "closed" is the half the operator cares about); a shard that
// vanished is archived when the pulled tree holds its archive/ copy and
// removed when it holds none — `furrow archive` and `furrow rm` delete a hot
// shard alike, and calling an rm "archived" pointed the reader at an
// `unarchive` that has nothing to restore (t-k0g9).
const (
	incomingCreated  = "created"
	incomingClosed   = "closed"
	incomingReopened = "reopened"
	incomingMoved    = "moved"
	incomingRefiled  = "refiled"
	incomingArchived = "archived"
	incomingRemoved  = "removed"
	incomingUpdated  = "updated"
)

// incomingKinds lists the vocabulary in the order the docs enumerate it.
var incomingKinds = []string{
	incomingCreated, incomingClosed, incomingReopened, incomingMoved,
	incomingRefiled, incomingArchived, incomingRemoved, incomingUpdated,
}

// IncomingChange is one pulled-in task change: the task, its title (resolved
// from the shard so the summary is readable without a second command), and a
// kind (the vocabulary above). From/To carry the old and new lane for moved
// and the old and new epic for refiled ("" = unfiled).
type IncomingChange struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

// classifyIncomingEdit is the modification arm of the incoming classifier,
// pure so the kind priority is table-testable without git.
func classifyIncomingEdit(before, after *core.Task) (kind, from, to string) {
	switch {
	case before.Closed == nil && after.Closed != nil:
		return incomingClosed, "", ""
	case before.Closed != nil && after.Closed == nil:
		return incomingReopened, "", ""
	case before.Status != after.Status:
		return incomingMoved, before.Status, after.Status
	case before.Epic != after.Epic:
		return incomingRefiled, before.Epic, after.Epic
	}
	return incomingUpdated, "", ""
}

// incomingChanges classifies what the pull brought in. base is HEAD as captured
// AFTER the auto-commit and before the pull: the rebase replays our own commits
// on top of what it pulls, so base's tree already holds everything local and
// the TREE diff base..HEAD is exactly the others' changes — never our own
// auto-commit. Best-effort display data by the publishedSwitches contract: any
// git or parse failure drops the entry (or the whole list) rather than failing
// the sync. Task shards only: an epic's inbound activation already surfaces as
// brief's epic header, and the body prose has no machine-classifiable diff.
func (a *App) incomingChanges(ctx context.Context, r *gitrepo.Repo, spec, base string) []IncomingChange {
	if base == "" {
		return nil
	}
	head := r.Head(ctx)
	if head == "" || head == base {
		return nil
	}
	prefix := spec + "/tasks/"
	taskAt := func(rev, path string) *core.Task {
		data := r.FileAt(ctx, rev, path)
		if data == nil {
			return nil
		}
		t, err := core.UnmarshalTask(data)
		if err != nil {
			return nil
		}
		return t
	}
	var out []IncomingChange
	for _, fc := range r.ChangedFiles(ctx, base, head, prefix) {
		name, ok := strings.CutPrefix(fc.Path, prefix)
		if !ok || !strings.HasSuffix(name, ".json") || strings.Contains(name, "/") {
			continue
		}
		ch := IncomingChange{ID: strings.TrimSuffix(name, ".json")}
		switch fc.Status {
		case 'A':
			t := taskAt(head, fc.Path)
			if t == nil {
				continue
			}
			ch.Kind, ch.Title = incomingCreated, t.Title
		case 'D':
			t := taskAt(base, fc.Path)
			if t == nil {
				continue
			}
			ch.Kind, ch.Title = incomingRemoved, t.Title
			if r.FileAt(ctx, head, spec+"/archive/tasks/"+name) != nil {
				ch.Kind = incomingArchived
			}
		case 'M':
			before, after := taskAt(base, fc.Path), taskAt(head, fc.Path)
			if before == nil || after == nil {
				continue
			}
			ch.Title = after.Title
			ch.Kind, ch.From, ch.To = classifyIncomingEdit(before, after)
		default:
			continue
		}
		out = append(out, ch)
	}
	return out
}
