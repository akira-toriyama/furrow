package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"golang.org/x/term"
)

// JSON goes to stdout ONLY; logs, spinners, and errors go to stderr.
// These helpers are the single funnel for that rule.

// out is stdout; overridable in tests.
var out io.Writer = os.Stdout

// errOut is stderr; overridable in tests. The scope banner and other
// human-facing notices go here so stdout stays pure data (JSON/table).
var errOut io.Writer = os.Stderr

// mustJSON marshals deterministically (SetEscapeHTML(false), 2-space indent) so
// CLI JSON output matches the index's encoding style.
func mustJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// printJSON writes a value as indented JSON to stdout.
func printJSON(v any) {
	fmt.Fprintln(out, string(mustJSON(v)))
}

// printNDJSONValue writes one value as a compact JSON line (Encode adds the \n).
func printNDJSONValue(v any) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	fmt.Fprint(out, b.String())
}

// printNDJSON writes one compact JSON object per line.
func printNDJSON(tasks []core.Task) {
	for _, t := range tasks {
		printNDJSONValue(t)
	}
}

// isTTY reports whether stdout is a terminal — used to pick table vs plain
// output and to gate destructive ops in non-interactive contexts.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// printTaskTable renders tasks as an aligned text table (human output). It is
// deliberately plain (no box drawing) so it greps and copies cleanly.
func printTaskTable(tasks []core.Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return
	}
	// column widths
	wID, wStatus := len("ID"), len("STATUS")
	for _, t := range tasks {
		if len(t.ID) > wID {
			wID = len(t.ID)
		}
		if len(t.Status) > wStatus {
			wStatus = len(t.Status)
		}
	}
	fmt.Fprintf(out, "%-*s  %-*s  %5s  %s\n", wID, "ID", wStatus, "STATUS", "PRIO", "TITLE")
	for _, t := range tasks {
		title := t.Title
		if len(t.Labels) > 0 {
			title += "  [" + strings.Join(t.Labels, ",") + "]"
		}
		fmt.Fprintf(out, "%-*s  %-*s  %5d  %s\n", wID, t.ID, wStatus, t.Status, t.Priority, title)
	}
}

// emitTasks renders a task list per the active output mode (--json | --ndjson |
// human table). emptyIsNotFound makes an empty result exit 1 (the "empty" arm
// of the contract) — used by commands where "nothing matched" is a soft miss.
func emitTasks(tasks []core.Task, emptyIsNotFound bool) error {
	switch {
	case flagNDJSON:
		printNDJSON(tasks)
	case flagJSON:
		if tasks == nil {
			tasks = []core.Task{}
		}
		printJSON(tasks)
	default:
		printTaskTable(tasks)
	}
	if emptyIsNotFound && len(tasks) == 0 {
		return &core.Error{Code: core.CodeNotFound, Msg: "no matching tasks"}
	}
	return nil
}

// actionReason explains, for an agent, WHY a task is in `next`: the next-lane it
// sits in, and the dependencies it satisfied (all already done — that is what
// made it actionable). deps_satisfied is [] when the task had no dependencies.
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
// unchanged. An empty result is the "nothing actionable" miss (exit 1).
func emitActionable(tasks []core.Task) error {
	switch {
	case flagNDJSON:
		for _, t := range tasks {
			printNDJSONValue(actionableView{Task: t, Reason: reasonFor(t)})
		}
	case flagJSON:
		views := make([]actionableView, 0, len(tasks))
		for _, t := range tasks {
			views = append(views, actionableView{Task: t, Reason: reasonFor(t)})
		}
		printJSON(views)
	default:
		printTaskTable(tasks)
	}
	if len(tasks) == 0 {
		return &core.Error{Code: core.CodeNotFound, Msg: "no actionable tasks"}
	}
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
func emitRevisit(items []app.RevisitItem) error {
	switch {
	case flagNDJSON:
		for _, it := range items {
			printNDJSONValue(revisitView{Task: it.Task, Revisit: it.Reasons})
		}
	case flagJSON:
		views := make([]revisitView, 0, len(items))
		for _, it := range items {
			views = append(views, revisitView{Task: it.Task, Revisit: it.Reasons})
		}
		printJSON(views)
	default:
		tasks := make([]core.Task, 0, len(items))
		for _, it := range items {
			tasks = append(tasks, it.Task)
		}
		printTaskTable(tasks)
	}
	return nil
}

// taskView is the JSON shape for `show`: the task plus its resolved body text.
type taskView struct {
	core.Task
	BodyText string `json:"body_text"`
}

// printTaskDetail renders a single task for `show` / after a mutation.
func printTaskDetail(t *core.Task, body string) {
	if flagJSON {
		printJSON(taskView{Task: *t, BodyText: body})
		return
	}
	fmt.Fprintf(out, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(out, "status:   %s\n", t.Status)
	fmt.Fprintf(out, "priority: %d\n", t.Priority)
	if t.Value != nil {
		fmt.Fprintf(out, "value:    %d\n", *t.Value)
	}
	if t.Effort != nil {
		fmt.Fprintf(out, "effort:   %d\n", *t.Effort)
	}
	if t.Value != nil && t.Effort != nil && *t.Effort > 0 {
		fmt.Fprintf(out, "roi:      %.2f\n", t.ROI())
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(out, "labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Parent != "" {
		fmt.Fprintf(out, "parent:   %s\n", t.Parent)
	}
	if len(t.Deps) > 0 {
		fmt.Fprintf(out, "deps:     %s\n", strings.Join(t.Deps, ", "))
	}
	if len(t.Refs) > 0 {
		fmt.Fprintf(out, "refs:     %s\n", strings.Join(t.Refs, ", "))
	}
	for _, c := range t.Checklist {
		box := "[ ]"
		if c.Done {
			box = "[x]"
		}
		fmt.Fprintf(out, "  %s %s\n", box, c.Text)
	}
	fmt.Fprintf(out, "created:  %s\n", t.Created.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "updated:  %s\n", t.Updated.Format("2006-01-02 15:04"))
	if t.Closed != nil {
		fmt.Fprintf(out, "closed:   %s\n", t.Closed.Format("2006-01-02 15:04"))
	}
	if strings.TrimSpace(body) != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(body, "\n"))
	}
}

// printOK prints a short confirmation line for a mutation (human mode) or the
// task JSON (--json mode).
func printOK(verb string, t *core.Task) {
	if flagJSON {
		printJSON(t)
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, t.ID, t.Title)
}

// printMutation reports a single-task edit. In --json mode it emits
// {before, after, changed} so an agent sees the effect of a mutation inline,
// without a follow-up `show`. In human mode it prints the short verb line.
func printMutation(verb string, before, after *core.Task) {
	if flagJSON {
		printJSON(map[string]any{
			"before":  before,
			"after":   after,
			"changed": changedFields(before, after),
		})
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, after.ID, after.Title)
}

// changedFields lists the task fields that differ between before and after
// (json field names), so an agent need not diff the two objects itself. The
// always-bumped `updated` stamp and the immutable `created`/`body` are omitted.
// An empty result is [] (never null), and a nil before yields [].
func changedFields(before, after *core.Task) []string {
	ch := []string{}
	if before == nil {
		return ch
	}
	if before.Status != after.Status {
		ch = append(ch, "status")
	}
	if before.Priority != after.Priority {
		ch = append(ch, "priority")
	}
	if !intpEq(before.Value, after.Value) {
		ch = append(ch, "value")
	}
	if !intpEq(before.Effort, after.Effort) {
		ch = append(ch, "effort")
	}
	if before.Title != after.Title {
		ch = append(ch, "title")
	}
	if before.Parent != after.Parent {
		ch = append(ch, "parent")
	}
	if !strsEq(before.Labels, after.Labels) {
		ch = append(ch, "labels")
	}
	if !strsEq(before.Deps, after.Deps) {
		ch = append(ch, "deps")
	}
	if !strsEq(before.Refs, after.Refs) {
		ch = append(ch, "refs")
	}
	if !checklistEq(before.Checklist, after.Checklist) {
		ch = append(ch, "checklist")
	}
	if !timeEq(before.Closed, after.Closed) {
		ch = append(ch, "closed")
	}
	return ch
}

// intpEq compares two optional ints: both nil is equal; one nil is not.
func intpEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// strsEq compares two string slices; nil and empty compare equal.
func strsEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checklistEq compares two checklists (ChecklistItem is comparable).
func checklistEq(a, b []core.ChecklistItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// timeEq compares optional timestamps: both nil is equal; one nil is not.
func timeEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// atoiArg parses a CLI integer argument into a validation error on failure.
func atoiArg(name, s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, core.Validationf("", "%s must be an integer, got %q", name, s)
	}
	return n, nil
}
