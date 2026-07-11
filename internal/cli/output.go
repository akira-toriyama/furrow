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

// jsonMode reports whether machine output was requested in either form. It is
// the single predicate a command gates on so --ndjson is honored everywhere
// --json is — not just the list commands. (--ndjson wins when both are set;
// emitObject picks the exact shape.)
func jsonMode() bool { return flagJSON || flagNDJSON }

// emitObject writes a single value as the active machine format: indented under
// --json, compact one-line under --ndjson. It is the single-object twin of
// emitTasks — for commands whose machine payload is one object (a mutation's
// {before,after,changed}, an attach/init/edit result, the apply report, the
// version block). Callers gate on jsonMode() first; a list-shaped command uses
// emitTasks / a per-line loop instead.
func emitObject(v any) {
	if flagNDJSON {
		printNDJSONValue(v)
		return
	}
	printJSON(v)
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
		// repos ride alongside the labels in (), so `ls | grep owner/repo` works.
		if len(t.Repos) > 0 {
			title += "  (" + strings.Join(t.Repos, ",") + ")"
		}
		fmt.Fprintf(out, "%-*s  %-*s  %5d  %s\n", wID, t.ID, wStatus, t.Status, t.Priority, title)
	}
}

// emitTasks renders a task list per the active output mode (--json | --ndjson |
// human table). An empty list is a healthy result (exit 0), never a miss — a
// query that matched nothing still succeeded. exit 1 is reserved for a
// specifically requested id that does not exist (e.g. `show <id>`).
func emitTasks(tasks []core.Task) error {
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
// unchanged. An empty result is a healthy "nothing actionable right now" state
// and exits 0 — the same contract as `ls`/`revisit` (exit 1 is reserved for a
// specifically requested id that is missing, e.g. `show`). An agent pipeline
// under `set -e` must not treat "no work to pick up" as a failure.
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
		if len(tasks) == 0 {
			fmt.Fprintln(out, "(nothing actionable)")
			return nil
		}
		printTaskTable(tasks)
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

// searchHitView is one `search` result: the task plus which field carried the
// match and a one-line snippet with the term in context (JSON/NDJSON only).
type searchHitView struct {
	core.Task
	MatchedField string `json:"matched_field"`
	Snippet      string `json:"snippet"`
}

// emitSearch renders `search` results in canonical order. --json is an array
// (empty -> [], never null), --ndjson one hit per line, human a plain table
// with the snippet. A zero-match result is a healthy empty result (exit 0),
// never a miss — the same contract as ls/next/revisit.
func emitSearch(hits []app.SearchHit) error {
	switch {
	case flagNDJSON:
		for _, h := range hits {
			printNDJSONValue(searchHitView{Task: h.Task, MatchedField: h.MatchedField, Snippet: h.Snippet})
		}
	case flagJSON:
		views := make([]searchHitView, 0, len(hits))
		for _, h := range hits {
			views = append(views, searchHitView{Task: h.Task, MatchedField: h.MatchedField, Snippet: h.Snippet})
		}
		printJSON(views)
	default:
		printSearchTable(hits)
	}
	return nil
}

// printSearchTable renders search hits as a plain aligned table: id, matched
// field, and the match text. For a body hit the title is shown before the
// snippet (so the id is never the only clue to which task matched); a title hit
// shows the title alone. Deliberately plain (no box drawing) so it greps and
// copies cleanly, like printTaskTable.
func printSearchTable(hits []app.SearchHit) {
	if len(hits) == 0 {
		fmt.Fprintln(out, "(no matches)")
		return
	}
	wID, wField := len("ID"), len("FIELD")
	for _, h := range hits {
		if len(h.Task.ID) > wID {
			wID = len(h.Task.ID)
		}
		if len(h.MatchedField) > wField {
			wField = len(h.MatchedField)
		}
	}
	fmt.Fprintf(out, "%-*s  %-*s  %s\n", wID, "ID", wField, "FIELD", "MATCH")
	for _, h := range hits {
		match := h.Snippet
		if h.MatchedField == "body" {
			match = h.Task.Title + "  ·  " + h.Snippet
		}
		fmt.Fprintf(out, "%-*s  %-*s  %s\n", wID, h.Task.ID, wField, h.MatchedField, match)
	}
}

// taskView is the JSON shape for `show`: the task plus its resolved body text.
type taskView struct {
	core.Task
	BodyText string `json:"body_text"`
}

// metaBacklinkView is `show --no-body --backlinks`: metadata plus mentioned_by,
// with no body_text key at all (absent, not an empty placeholder).
type metaBacklinkView struct {
	core.Task
	MentionedBy []mentionRef `json:"mentioned_by"`
}

// showView picks the JSON shape for one `show` result. Body and backlinks are
// each opt-in/out; an omitted facet means its key is absent, and with both
// off the shape is a bare task — identical to a `ls` element.
func showView(it app.ShowItem, mentions []core.Task, noBody, backlinks bool) any {
	switch {
	case noBody && backlinks:
		return metaBacklinkView{Task: it.Task, MentionedBy: toMentionRefs(mentions)}
	case noBody:
		return it.Task
	case backlinks:
		return backlinkView{Task: it.Task, BodyText: it.Body, MentionedBy: toMentionRefs(mentions)}
	default:
		return taskView{Task: it.Task, BodyText: it.Body}
	}
}

// emitShow renders `show` results in input order. --ndjson is one task per
// line at any arity; --json keeps the historical single object for one id and
// emits an array for a batch (so a batch of misses still prints []); the human
// output separates detail blocks with a --- line. mentions is non-nil only
// when --backlinks ran, aligned index-for-index with items.
func emitShow(items []app.ShowItem, mentions [][]core.Task, single, noBody, backlinks bool) {
	mentionsAt := func(i int) []core.Task {
		if mentions == nil {
			return nil
		}
		return mentions[i]
	}
	switch {
	case flagNDJSON:
		for i, it := range items {
			printNDJSONValue(showView(it, mentionsAt(i), noBody, backlinks))
		}
	case flagJSON:
		if single {
			// exactly one item: a single-id miss error-returns before emission
			printJSON(showView(items[0], mentionsAt(0), noBody, backlinks))
			return
		}
		views := make([]any, 0, len(items))
		for i, it := range items {
			views = append(views, showView(it, mentionsAt(i), noBody, backlinks))
		}
		printJSON(views)
	default:
		for i := range items {
			if i > 0 {
				fmt.Fprintln(out, "---")
			}
			if backlinks {
				printTaskDetailWithBacklinks(&items[i].Task, items[i].Body, mentionsAt(i))
			} else {
				printTaskDetail(&items[i].Task, items[i].Body)
			}
		}
	}
}

// printTaskDetail renders a single task's human detail block for `show`. JSON
// and NDJSON are handled one layer up in emitShow/showView (which is where the
// --no-body / --backlinks shape lives), so this is the human path only.
func printTaskDetail(t *core.Task, body string) {
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
	if len(t.Repos) > 0 {
		fmt.Fprintf(out, "repos:    %s\n", strings.Join(t.Repos, ", "))
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

// mentionRef is one entry of `show --backlinks`' mentioned_by: the referencing
// task trimmed to what an agent needs to act (id, title, status) without a
// second lookup.
type mentionRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// backlinkView is the JSON shape for `show --backlinks`: the task, its body, and
// the tasks that mention it. mentioned_by is always present (never null) so a
// "nobody mentions this" result is [] rather than a missing key.
type backlinkView struct {
	core.Task
	BodyText    string       `json:"body_text"`
	MentionedBy []mentionRef `json:"mentioned_by"`
}

// toMentionRefs trims mentioning tasks to the id/title/status an agent needs
// to act without a second lookup. Always a non-nil slice ([] never null).
func toMentionRefs(mentions []core.Task) []mentionRef {
	refs := make([]mentionRef, 0, len(mentions))
	for _, m := range mentions {
		refs = append(refs, mentionRef{ID: m.ID, Title: m.Title, Status: m.Status})
	}
	return refs
}

// printTaskDetailWithBacklinks renders `show --backlinks`'s human block: the
// usual detail plus a "Mentioned in" section. JSON/NDJSON go through
// emitShow/showView (backlinkView), so this is the human path only.
func printTaskDetailWithBacklinks(t *core.Task, body string, mentions []core.Task) {
	printTaskDetail(t, body)
	fmt.Fprintf(out, "\nMentioned in:\n")
	refs := toMentionRefs(mentions)
	if len(refs) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, m := range refs {
		fmt.Fprintf(out, "  %s  %s\n", m.ID, m.Title)
	}
}

// printOK prints a short confirmation line for a mutation (human mode) or the
// task as one JSON value (--json indented / --ndjson compact one-line).
func printOK(verb string, t *core.Task) {
	if jsonMode() {
		emitObject(t)
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, t.ID, t.Title)
}

// printMutation reports a single-task edit. In machine mode it emits
// {before, after, changed} so an agent sees the effect of a mutation inline,
// without a follow-up `show` — indented under --json, compact one-line under
// --ndjson. Any `extra` keys (e.g. a `clamped` signal) are merged into that
// envelope. In human mode it prints the short verb line.
func printMutation(verb string, before, after *core.Task, extra map[string]any) {
	if jsonMode() {
		m := map[string]any{
			"before":  before,
			"after":   after,
			"changed": changedFields(before, after),
		}
		for k, v := range extra {
			m[k] = v
		}
		emitObject(m)
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, after.ID, after.Title)
}

// warnClamp writes a stderr note when an explicit 1..5 estimate was silently
// rounded by the marshaller's clamp (nil requested / in-range = no-op). An
// explicit CLI arg deserves a signal — clamp-don't-reject is a config-file
// policy, not for a typed command argument (t-abj3). stdout stays pure.
func warnClamp(field string, requested, stored *int) {
	if requested == nil || (*requested >= core.EstimateMin && *requested <= core.EstimateMax) {
		return
	}
	s := 0
	if stored != nil {
		s = *stored
	}
	fmt.Fprintf(errOut, "note: %s %d clamped to %d (valid range %d..%d)\n", field, *requested, s, core.EstimateMin, core.EstimateMax)
}

// clampEntry returns the {requested, stored} envelope entry when an explicit
// estimate was clamped, else nil — the machine-readable twin of warnClamp for
// the mutation's --json/--ndjson `clamped` field.
func clampEntry(requested, stored *int) map[string]any {
	if requested == nil || (*requested >= core.EstimateMin && *requested <= core.EstimateMax) {
		return nil
	}
	s := 0
	if stored != nil {
		s = *stored
	}
	return map[string]any{"requested": *requested, "stored": s}
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
	if !strsEq(before.Repos, after.Repos) {
		ch = append(ch, "repos")
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
