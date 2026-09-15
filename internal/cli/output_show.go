// `show`: one task with its body, the detail block, and the backlinks that
// ride with it. An epic id given to `show` renders through the epic views
// (output_epic.go) — this file decides which, not how.

package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

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

// emitShow renders `show` results in input order, each entry as the entity it
// IS: a task's dashboard or a box's goal + roll-up (the very object `epic show`
// prints). --ndjson is one entry per line at any arity; --json is ALWAYS an
// array, one element per found id (a single id is a one-element array, an
// all-miss batch prints []) — the always-array rule: `show <id>...` has array
// cardinality by SIGNATURE, and the runtime argv length must not fork the
// shape. A mixed batch's array is heterogeneous, which is what an id naming
// its own entity kind implies; the human output separates detail blocks with a
// --- line. mentions is non-nil only when --backlinks ran, aligned
// index-for-index with entries; a box has no entry there, since [[id]] links
// name tasks (core.LinkPattern is built from [ids].prefix).
func emitShow(a *app.App, entries []app.ShowEntry, mentions [][]core.Task, noBody, backlinks bool) {
	mentionsAt := func(i int) []core.Task {
		if mentions == nil {
			return nil
		}
		return mentions[i]
	}
	view := func(i int) any {
		if e := entries[i].Epic; e != nil {
			if noBody {
				// --no-body OMITS the key, as it does for a task (showView drops
				// to the bare core.Task) — not an empty string, which a consumer
				// cannot tell from a box whose body really is empty.
				v := toEpicDetailView(e)
				return epicMetaView{Epic: v.Epic, Progress: v.Progress, Stuck: v.Stuck, Waiting: v.Waiting, Tasks: v.Tasks}
			}
			return toEpicDetailView(e)
		}
		return showView(*entries[i].Task, mentionsAt(i), noBody, backlinks)
	}
	views := make([]any, 0, len(entries))
	for i := range entries {
		views = append(views, view(i))
	}
	emitList(views, func() {
		for i := range entries {
			if i > 0 {
				fmt.Fprintln(out, "---")
			}
			if e := entries[i].Epic; e != nil {
				printEpicDetail(a, e)
				continue
			}
			if backlinks {
				printTaskDetailWithBacklinks(a, &entries[i].Task.Task, entries[i].Task.Body, mentionsAt(i))
			} else {
				printTaskDetail(a, &entries[i].Task.Task, entries[i].Task.Body)
			}
		}
	})
}

// repeatAnchorNote names the series start beside the rule. Without it the rule
// alone cannot be read: FREQ=MONTHLY says nothing about WHICH day, which lives
// in the anchor the rule is expanded from.
func repeatAnchorNote(t *core.Task) string {
	if t.RepeatAnchor == nil {
		return ""
	}
	return " (since " + humanTime(*t.RepeatAnchor) + ")"
}

// dueDetail renders a due stamp for the `show` block: the local timestamp plus
// the state, so "when" and "is that a problem?" are one line instead of a date
// the reader has to compare against today by hand. Empty when there is no date.
//
// The zone and the clock are the process's own (time.Local / time.Now) — the
// same pair humanTime already renders every other timestamp with. The app layer
// is where an injected clock matters (lint/brief must be reproducible); a
// rendered marker is read by a human, now, on this machine.
func dueDetail(a *app.App, t *core.Task) string {
	if t.Due == nil {
		return ""
	}
	s := humanTime(*t.Due)
	switch a.DueDisplayState(t) {
	case core.DueOverdue:
		s += "  (overdue)"
	case core.DueToday:
		s += "  (today)"
	}
	return s
}

// printTaskDetail renders a single task's human detail block for `show`. JSON
// and NDJSON are handled one layer up in emitShow/showView (which is where the
// --no-body / --backlinks shape lives), so this is the human path only.
func printTaskDetail(a *app.App, t *core.Task, body string) {
	fmt.Fprintf(out, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(out, "status:   %s\n", t.Status)
	if t.Epic != "" {
		fmt.Fprintf(out, "epic:     %s\n", t.Epic)
	}
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
	if t.Repeat != "" {
		fmt.Fprintf(out, "repeat:   %s%s\n", t.Repeat, repeatAnchorNote(t))
	}
	if d := dueDetail(a, t); d != "" {
		fmt.Fprintf(out, "due:      %s\n", d)
	}
	fmt.Fprintf(out, "created:  %s\n", humanTime(t.Created))
	fmt.Fprintf(out, "updated:  %s\n", humanTime(t.Updated))
	if t.Closed != nil {
		fmt.Fprintf(out, "closed:   %s\n", humanTime(*t.Closed))
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
func printTaskDetailWithBacklinks(a *app.App, t *core.Task, body string, mentions []core.Task) {
	printTaskDetail(a, t, body)
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
