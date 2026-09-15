// `ls --tree`: groups by epic over what MATCHED, and the drawing of one
// group. groupView embeds core.Task rows through listItemView, so the same
// no-MarshalJSON rule as every embedded view applies.

package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// groupView is one epic's group in `ls --tree --json`. Epic is a pointer so the
// trailing unfiled group serializes as `"epic": null` rather than a fabricated
// empty box — a reader must be able to tell "no epic" from "an epic with blank
// fields".
type groupView struct {
	Epic     *core.Epic     `json:"epic"`
	Active   bool           `json:"active"`
	Progress *app.Progress  `json:"progress,omitempty"`
	Stuck    bool           `json:"stuck"`
	Tasks    []listItemView `json:"tasks"`
}

func toTreeViews(nodes []app.TreeNode) []listItemView {
	out := make([]listItemView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, listItemView{Task: n.Task, Actionable: n.Actionable, BlockedBy: n.BlockedBy})
	}
	return out
}

func toGroupViews(groups []app.TreeGroup) []groupView {
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupView{
			Epic:     g.Epic,
			Active:   g.Active,
			Progress: g.Progress,
			Stuck:    g.Stuck,
			Tasks:    toTreeViews(g.Tasks),
		})
	}
	return out
}

// emitTree renders the board grouped by epic. --json nests tasks inside their
// group; --ndjson streams one GROUP per line (a group is the value here, and the
// line-oriented contract is one value per line — flattening it would destroy the
// grouping that was asked for).
func emitTree(a *app.App, groups []app.TreeGroup) error {
	views := toGroupViews(groups)
	emitList(views, func() {
		if len(views) == 0 {
			fmt.Fprintln(out, "(no tasks)")
			return
		}
		for i, g := range groups {
			if i > 0 {
				fmt.Fprintln(out)
			}
			printTreeGroup(a, g)
		}
	})
	return nil
}

func printTreeGroup(a *app.App, g app.TreeGroup) {
	if g.Epic == nil {
		fmt.Fprintln(out, "(no epic)")
	} else {
		head := g.Epic.ID + "  " + g.Epic.Title
		if g.Active {
			head = "▶ " + head
		} else {
			head = "  " + head
		}
		if g.Progress != nil {
			head += fmt.Sprintf("  (%d/%d)", g.Progress.Done, g.Progress.Total)
		}
		if !g.Epic.IsOpen() {
			head += "  [closed]"
		}
		if g.Stuck {
			head += "  ⚠ stuck"
		}
		fmt.Fprintln(out, head)
		if g.Epic.Goal != "" {
			fmt.Fprintln(out, "    goal: "+g.Epic.Goal)
		}
	}
	for _, n := range g.Tasks {
		printTreeNode(a, n)
	}
}

// printTreeNode draws one member task, marked with the shared state glyph.
// The lane is printed too: a glyph is a summary, not a substitute, and `[ready]`
// is what greps.
func printTreeNode(a *app.App, n app.TreeNode) {
	line := "    " + stateGlyph(a, n.Actionable, n.Task.Status) + " " + n.Task.ID + "  [" + n.Task.Status + "]  " + n.Task.Title
	// The same tags the flat row carries: --tree is the same matched rows
	// regrouped, so it must not be the one view where a promise — or a series —
	// disappears.
	line = withTags(line, dueTag(a, &n.Task), repeatTag(&n.Task))
	if len(n.BlockedBy) > 0 {
		line += "  ← blocked by: " + strings.Join(n.BlockedBy, ", ")
	}
	fmt.Fprintln(out, line)
}
