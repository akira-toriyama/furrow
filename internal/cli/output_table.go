// The task table: the rows `ls`, `next` and `revisit` print, and the JSON
// views that ride beside them (actionable reasons, revisit signals). Row
// tags (due/repeat glyphs) live in output.go — every view that renders a task
// as a task shares them, so a new tag is added there, not here.

package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// printTaskTable renders tasks as an aligned text table (human output). It is
// deliberately plain (no box drawing) so it greps and copies cleanly.
func printTaskTable(a *app.App, tasks []core.Task) {
	printRows(a, tasks, nil)
}

// printRows is the one human task table: id, lane, priority and the title cell
// — labels in [], repos in () so `ls | grep owner/repo` works, then the due
// and repeat tags, which ride in the cell rather than as columns (a column
// would widen every row on every board for a field only a few tasks carry).
// glyphs, when given, is one leading state column per row (the flat `ls`);
// nil is the glyph-less table every other list prints. Two copies differed by
// that column alone (t-3tq4).
func printRows(a *app.App, tasks []core.Task, glyphs []string) {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return
	}
	wID, wStatus := len("ID"), len("STATUS")
	for _, t := range tasks {
		if len(t.ID) > wID {
			wID = len(t.ID)
		}
		if len(t.Status) > wStatus {
			wStatus = len(t.Status)
		}
	}
	lead := func(g string) string {
		if glyphs == nil {
			return ""
		}
		return g + "  "
	}
	fmt.Fprintf(out, "%s%-*s  %-*s  %5s  %s\n", lead(" "), wID, "ID", wStatus, "STATUS", "PRIO", "TITLE")
	for i, t := range tasks {
		title := t.Title
		if len(t.Labels) > 0 {
			title += "  [" + strings.Join(t.Labels, ",") + "]"
		}
		if len(t.Repos) > 0 {
			title += "  (" + strings.Join(t.Repos, ",") + ")"
		}
		title = withTags(title, dueTag(a, &t), repeatTag(&t))
		g := ""
		if glyphs != nil {
			g = glyphs[i]
		}
		fmt.Fprintf(out, "%s%-*s  %-*s  %5d  %s\n", lead(g), wID, t.ID, wStatus, t.Status, t.Priority, title)
	}
}

// emitTasks renders a task list per the active output mode (--json | --ndjson |
// human table). An empty list is a healthy result (exit 0), never a miss — a
// query that matched nothing still succeeded. exit 1 is reserved for a
// specifically requested id that does not exist (e.g. `show <id>`).
func emitTasks(a *app.App, tasks []core.Task) error {
	emitList(tasks, func() { printTaskTable(a, tasks) })
	return nil
}

// actionReason explains, for an agent, WHY a task is in `next`: the lane it sits
// in (whichever lane qualified it — a configured next-lane, or one included by a
// one-shot `--lanes` override, so a backlog task surfaced by `next --lanes
// backlog` is distinguishable from a ready one), and the dependencies it satisfied
// (all already done — that is what made it actionable). deps_satisfied is [] when
// the task had no dependencies.
type actionReason struct {
	InNextLane    string   `json:"in_next_lane"`
	DepsSatisfied []string `json:"deps_satisfied"`
}

// actionableView is a `next` task plus its reason (JSON/NDJSON output only).
type actionableView struct {
	core.Task
	Reason actionReason `json:"reason"`
}

func reasonFor(t core.Task) actionReason {
	deps := t.Deps
	if deps == nil {
		deps = []string{}
	}
	// A task is in `next` only when its status is a next-lane and every dep is
	// done, so the lane qualifies it and its deps are exactly what it satisfied.
	return actionReason{InNextLane: t.Status, DepsSatisfied: deps}
}

// emitActionable renders `next` results. In --json / --ndjson it attaches a
// reason to each task so an agent sees why it is actionable; the human table is
// unchanged. An empty result is a healthy "nothing actionable right now" state
// and exits 0 — the same contract as `ls`/`revisit` (exit 1 is reserved for a
// specifically requested id that is missing, e.g. `show`). An agent pipeline
// under `set -e` must not treat "no work to pick up" as a failure.
func emitActionable(a *app.App, tasks []core.Task) error {
	views := make([]actionableView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, actionableView{Task: t, Reason: reasonFor(t)})
	}
	emitList(views, func() {
		if len(tasks) == 0 {
			fmt.Fprintln(out, "(nothing actionable)")
			return
		}
		printTaskTable(a, tasks)
	})
	return nil
}

// revisitView is a task plus the reasons it needs re-evaluation (JSON/NDJSON
// output only) — the agent's worklist of metadata to fix.
type revisitView struct {
	core.Task
	Revisit []core.RevisitReason `json:"revisit"`
}

// emitRevisit renders `revisit` results. In --json / --ndjson it attaches the
// reasons to each task so an agent sees exactly what to fix; the human table is
// the shared one. Unlike `next`, an empty result is the healthy "nothing to
// revisit" state and exits 0 — an agent pipeline must not treat it as an error.
func emitRevisit(a *app.App, items []app.RevisitItem) error {
	views := make([]revisitView, 0, len(items))
	for _, it := range items {
		views = append(views, revisitView{Task: it.Task, Revisit: it.Reasons})
	}
	emitList(views, func() {
		tasks := make([]core.Task, 0, len(items))
		for _, it := range items {
			tasks = append(tasks, it.Task)
		}
		printTaskTable(a, tasks)
	})
	return nil
}
