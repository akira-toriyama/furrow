// The listing row with its derived facts (actionable, blocked_by): what `ls`
// emits per task, and what the tree, brief and epic views reuse for member
// rows. Keep it a projection of app.ListItem — no lookups of its own.

package cli

import (
	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// listItemView is one row of `ls`'s --json: the whole task (embedded, so it stays
// a superset of the bare-task shape older readers expect) plus the DERIVED facts
// the flat list exposes beside the stored ones. Same discipline as treeView:
// core.Task is EMBEDDED, so it must never grow a MarshalJSON or these sibling
// fields would silently vanish.
type listItemView struct {
	core.Task
	Actionable bool     `json:"actionable"`
	BlockedBy  []string `json:"blocked_by"`
}

func toListItemView(it app.ListItem) listItemView {
	return listItemView{Task: it.Task, Actionable: it.Actionable, BlockedBy: it.BlockedBy}
}

// emitListItems renders the flat `ls`: --json an array (empty -> [], never null),
// --ndjson one row per line, human an aligned table with a leading state glyph.
// An empty listing is a healthy result (exit 0), never a miss.
func emitListItems(a *app.App, items []app.ListItem) error {
	views := make([]listItemView, 0, len(items))
	for _, it := range items {
		views = append(views, toListItemView(it))
	}
	emitList(views, func() { printListItemTable(a, items) })
	return nil
}

// printListItemTable is printTaskTable plus a leading one-character state column
// (the same glyph the tree uses) — so the everyday `ls` shows, at a glance, which
// rows are ready to pick up (★) and which are merely open (·). Plain, greppable
// output.
func printListItemTable(a *app.App, items []app.ListItem) {
	tasks := make([]core.Task, 0, len(items))
	glyphs := make([]string, 0, len(items))
	for _, it := range items {
		tasks = append(tasks, it.Task)
		glyphs = append(glyphs, stateGlyph(a, it.Actionable, it.Task.Status))
	}
	printRows(a, tasks, glyphs)
}
