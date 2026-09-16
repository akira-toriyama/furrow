// The epic views: `epic ls`, `epic show`, and the epic mutation envelope with
// its own changed-field comparison (an Epic is not a Task; the two lists of
// fields are kept separate on purpose).

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// epicView is one epic row/record in --json. core.Epic is embedded for the same
// reason core.Task is elsewhere: the derived facts sit BESIDE the stored ones, so
// a reader gets the shard's keys unchanged plus progress/stuck.
type epicView struct {
	core.Epic
	Progress app.Progress `json:"progress"`
	Stuck    bool         `json:"stuck"`
	// OpenDeps are the deps still open (the unsatisfied "open after" edges) —
	// derived, so it sits beside the stored deps set rather than replacing it.
	// omitempty: a box with no waits keeps the pre-v7 row shape.
	OpenDeps []string `json:"open_deps,omitempty"`
	// Waiting is the box's "parked until" state — every non-terminal member
	// done, a parked member's due still ahead — as `{until, task}`; omitted
	// otherwise. The human rows print it as `⏳ waiting until <due> (<task>)`.
	Waiting *app.EpicWait `json:"waiting,omitempty"`
}

func toEpicView(it app.EpicItem) epicView {
	return epicView{Epic: it.Epic, Progress: it.Progress, Stuck: it.Stuck, OpenDeps: it.OpenDeps, Waiting: it.Waiting}
}

func emitEpicList(a *app.App, items []app.EpicItem) error {
	views := make([]epicView, 0, len(items))
	for _, it := range items {
		views = append(views, toEpicView(it))
	}
	emitList(views, func() {
		if len(views) == 0 {
			fmt.Fprintln(out, "(no epics)")
			return
		}
		for _, v := range views {
			mark := " "
			if v.Active {
				mark = "▶"
			}
			line := fmt.Sprintf("%s %s  %d/%d  %s", mark, v.ID, v.Progress.Done, v.Progress.Total, v.Title)
			if v.Pinned {
				line += "  📌 pinned"
			}
			if v.Standing {
				line += "  [standing]"
			}
			if !v.IsOpen() {
				line += "  [closed]"
			}
			if v.Stuck {
				line += "  ⚠ stuck"
			}
			if len(v.OpenDeps) > 0 {
				line += "  ⏳ waits: " + strings.Join(v.OpenDeps, ", ")
			}
			if v.Waiting != nil {
				line += "  " + waitingUntil(a, v.Waiting)
			}
			if v.Goal != "" {
				line += "\n    goal: " + v.Goal
			}
			fmt.Fprintln(out, line)
		}
	})
	return nil
}

// epicDetailView is `epic show --json`: the box plus its members, in the same
// row shape `ls --json` uses so a consumer parses one task type, not two.
type epicDetailView struct {
	core.Epic
	Progress app.Progress   `json:"progress"`
	Stuck    bool           `json:"stuck"`
	Waiting  *app.EpicWait  `json:"waiting,omitempty"`
	Tasks    []listItemView `json:"tasks"`
	BodyText string         `json:"body_text"`
}

// epicMetaView is epicDetailView minus the prose — `show <epic-id> --no-body`.
// A separate struct rather than `omitempty` on BodyText, so `epic show` keeps
// emitting the key for a box whose body is genuinely empty: the flag decides
// whether the key exists, never the content.
type epicMetaView struct {
	core.Epic
	Progress app.Progress   `json:"progress"`
	Stuck    bool           `json:"stuck"`
	Waiting  *app.EpicWait  `json:"waiting,omitempty"`
	Tasks    []listItemView `json:"tasks"`
}

func emitEpicDetail(a *app.App, d *app.EpicDetail, noBody bool) error {
	if jsonMode() {
		if noBody {
			v := toEpicDetailView(d)
			emitObject(epicMetaView{Epic: v.Epic, Progress: v.Progress, Stuck: v.Stuck, Waiting: v.Waiting, Tasks: v.Tasks})
			return nil
		}
		emitObject(toEpicDetailView(d))
		return nil
	}
	if noBody {
		lean := *d
		lean.Body = ""
		d = &lean
	}
	printEpicDetail(a, d)
	return nil
}

// toEpicDetailView is the epic detail's JSON value, split out so `furrow show
// <epic-id>` emits the SAME object `epic show` does — one shape per entity, no
// second box view to keep in step.
func toEpicDetailView(d *app.EpicDetail) epicDetailView {
	v := epicDetailView{Epic: d.Epic, Progress: d.Progress, Stuck: d.Stuck, Waiting: d.Waiting, BodyText: d.Body}
	v.Tasks = make([]listItemView, 0, len(d.Tasks))
	for _, it := range d.Tasks {
		v.Tasks = append(v.Tasks, toListItemView(it))
	}
	return v
}

// printEpicDetail is the human half, likewise shared with `show`.
func printEpicDetail(a *app.App, d *app.EpicDetail) {
	fmt.Fprintf(out, "%s  %s\n", d.Epic.ID, d.Epic.Title)
	if d.Epic.Goal != "" {
		fmt.Fprintf(out, "goal:     %s\n", d.Epic.Goal)
	}
	state := "open"
	if !d.Epic.IsOpen() {
		state = "closed"
	}
	if d.Epic.Active {
		state += ", active"
	}
	if d.Epic.Pinned {
		state += ", pinned"
	}
	if d.Epic.Standing {
		state += ", standing"
	}
	fmt.Fprintf(out, "state:    %s\n", state)
	fmt.Fprintf(out, "progress: %d/%d\n", d.Progress.Done, d.Progress.Total)
	if d.Stuck {
		fmt.Fprintln(out, "          ⚠ stuck — open members but none actionable")
	}
	if d.Waiting != nil {
		fmt.Fprintf(out, "          %s — open work done, a parked member's due still ahead\n", waitingUntil(a, d.Waiting))
	}
	if len(d.Epic.Labels) > 0 {
		fmt.Fprintf(out, "labels:   %s\n", strings.Join(d.Epic.Labels, ", "))
	}
	if len(d.Epic.Repos) > 0 {
		fmt.Fprintf(out, "repos:    %s\n", strings.Join(d.Epic.Repos, ", "))
	}
	if len(d.Deps) > 0 {
		parts := make([]string, 0, len(d.Deps))
		for _, r := range d.Deps {
			state := r.State
			if state == "" {
				state = "missing" // a dangling edge — lint's epic-dep-missing
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", r.ID, state))
		}
		fmt.Fprintf(out, "deps:     %s\n", strings.Join(parts, ", "))
	}
	for _, k := range sortedKeys(d.Epic.Meta) {
		fmt.Fprintf(out, "meta:     %s = %s\n", k, d.Epic.Meta[k])
	}
	fmt.Fprintf(out, "tasks (%d):\n", len(d.Tasks))
	for _, it := range d.Tasks {
		row := fmt.Sprintf("  %s %s  [%s]  %s", stateGlyph(a, it.Actionable, it.Task.Status), it.Task.ID, it.Task.Status, it.Task.Title)
		fmt.Fprintln(out, withTags(row, repeatTag(&it.Task)))
	}
	// The body last, exactly as `show` prints a task's: the compact dashboard
	// first, the prose (goal context, the activation log) after it.
	if strings.TrimSpace(d.Body) != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(d.Body, "\n"))
	}
}

// emitEpicMutation is emitMutation's epic twin: the same {before,after,changed}
// envelope, so a front-end reads one shape whichever entity it edited.
func emitEpicMutation(mutate func() (*core.Epic, *core.Epic, error)) error {
	before, after, err := mutate()
	if err != nil {
		return err
	}
	return emitEpicMutationResult(before, after, nil)
}

// emitEpicMutationResult renders the epic {before,after,changed} envelope, with
// any extra top-level keys merged in — activate's `open_deps` (its
// crossed-ordering warning made machine-readable), note's `appended`. Same
// escape hatch, and same reason, as the task side's mutationEnvelope: an
// envelope key for the fact a stderr note narrates.
func emitEpicMutationResult(before, after *core.Epic, extra map[string]any) error {
	if jsonMode() {
		emitObject(epicMutationEnvelope(before, after, extra))
		return nil
	}
	fmt.Fprintf(out, "%s  %s\n", after.ID, after.Title)
	return nil
}

// printEpicMutation is printMutation's epic twin: the same verb-prefixed human
// line, so a command that takes EITHER entity's id (`furrow note`) reads
// identically whichever one it addressed. The epic subtree's own verbs keep
// emitEpicMutationResult's bare `<id>  <title>` line — there the verb is
// already in the command.
func printEpicMutation(verb string, before, after *core.Epic, extra map[string]any) {
	if jsonMode() {
		emitObject(epicMutationEnvelope(before, after, extra))
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, after.ID, after.Title)
}

// epicMutationEnvelope builds the epic {before, after, changed} object — the
// single assembly point for the shape, for the reason mutationEnvelope
// documents on the task side: assembled twice, a new key gets added once.
func epicMutationEnvelope(before, after *core.Epic, extra map[string]any) map[string]any {
	return envelope(before, after, changedEpicFields(before, after), extra)
}

// changedEpicFields names the epic fields a mutation actually altered — the
// `changed` array of the envelope, so a caller can tell a real edit from a no-op
// without diffing two objects.
func changedEpicFields(before, after *core.Epic) []string {
	ch := []string{}
	if before == nil || after == nil {
		return ch
	}
	if before.Title != after.Title {
		ch = append(ch, "title")
	}
	if before.Goal != after.Goal {
		ch = append(ch, "goal")
	}
	if before.Active != after.Active {
		ch = append(ch, "active")
	}
	if !strsEq(before.Labels, after.Labels) {
		ch = append(ch, "labels")
	}
	if !strsEq(before.Repos, after.Repos) {
		ch = append(ch, "repos")
	}
	if !metaEq(before.Meta, after.Meta) {
		ch = append(ch, "meta")
	}
	if !strsEq(before.Deps, after.Deps) {
		ch = append(ch, "deps")
	}
	if before.Standing != after.Standing {
		ch = append(ch, "standing")
	}
	if before.Pinned != after.Pinned {
		ch = append(ch, "pinned")
	}
	if (before.Closed == nil) != (after.Closed == nil) {
		ch = append(ch, "closed")
	}
	if !timeEq(before.Reviewed, after.Reviewed) {
		ch = append(ch, "reviewed")
	}
	return ch
}

func metaEq(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
