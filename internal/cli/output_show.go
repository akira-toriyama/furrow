// `show`: one task with its body, the detail block, and the backlinks that
// ride with it. An epic id given to `show` renders through the epic views
// (output_epic.go) — this file decides which, not how.

package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// showFacts are the derived facts every `show` JSON shape carries beside the
// stored ones: the same two keys, computed by the same app helper (factsFor),
// that `ls`, `next`, `--tree` and `brief` have always put on a task row.
//
// The precedent this follows is t-pqme's, read in the other direction. There the
// rule was "machine readers were already served — every --json row carries
// repeat/repeat_anchor — so this is a human-output defect and no JSON contract
// moves". Here they were NOT served: `show --json` named a task's deps as bare
// ids and nothing else, so the one read that gives a WHOLE task was the only one
// from which "can this move?" could not be answered (t-3acv). The human block
// now prints each dep's lane; leaving JSON behind would recreate the divergence
// t-pqme is remembered for.
//
// Deliberately NOT a resolved dep array: `deps` already names the edges and
// `dep --list --json` is the resolved-both-directions shape. A second spelling
// of the same edge set in a second command is a drift surface; blocked_by is the
// CONCLUSION, which is what a caller of `show` lacked.
//
// All four view shapes carry the pair, --no-body included: that flag trims the
// PROSE, never the facts, and the lean metadata read is precisely the one an
// agent uses to decide what to pick up.
//
// The embed goes LAST in every shape but metaView, so each keeps the key ORDER
// it had and the new keys append — the shard rule's reasoning (a new field goes
// at the end) applied to a view. metaView is the exception on purpose: with no
// view extras to follow, Task-then-facts is listItemView's own order, which is
// what "key-for-key an ls row" means.
type showFacts struct {
	Actionable bool     `json:"actionable"`
	BlockedBy  []string `json:"blocked_by"`
}

func factsOf(it app.ShowItem) showFacts {
	return showFacts{Actionable: it.Actionable, BlockedBy: it.BlockedBy}
}

// taskView is task + body_text, and nothing else. `brief --json` embeds it for
// its next[] rows (output_brief.go), so it is SHARED and must stay exactly that
// shape: the facts `show` adds live in showTaskView, not here. Widening this
// type silently added actionable/blocked_by to brief's rows — built without the
// facts, so they read `false` and `null` on picks that are actionable by
// construction. A shape two commands emit is changed on purpose or not at all.
type taskView struct {
	core.Task
	BodyText string `json:"body_text"`
}

// showTaskView is `show`'s default shape: taskView's fields plus the derived
// facts. core.Task is EMBEDDED, so it must never grow a MarshalJSON — body_text
// and the showFacts siblings would silently vanish (the listItemView discipline).
type showTaskView struct {
	core.Task
	BodyText string `json:"body_text"`
	showFacts
}

// metaBacklinkView is `show --no-body --backlinks`: metadata plus mentioned_by,
// with no body_text key at all (absent, not an empty placeholder).
type metaBacklinkView struct {
	core.Task
	MentionedBy []mentionRef `json:"mentioned_by"`
	showFacts
}

// metaView is `show --no-body`: the bare task plus the derived facts. It is
// deliberately NOT a bare core.Task any more — the facts are what `ls --json`
// puts on the same task, and the lean metadata read is exactly the one an agent
// uses to decide what to pick up.
type metaView struct {
	core.Task
	showFacts
}

// showView picks the JSON shape for one `show` result. Body and backlinks are
// each opt-in/out; an omitted facet means its key is absent, and with both off
// the shape is the task plus its derived facts — key-for-key a `ls` element
// (listItemView), which is what makes `show --no-body --json` a drop-in for the
// row an agent would otherwise have re-fetched through `ls`.
func showView(it app.ShowItem, mentions []core.Task, noBody, backlinks bool) any {
	switch {
	case noBody && backlinks:
		return metaBacklinkView{Task: it.Task, showFacts: factsOf(it), MentionedBy: toMentionRefs(mentions)}
	case noBody:
		return metaView{Task: it.Task, showFacts: factsOf(it)}
	case backlinks:
		return backlinkView{Task: it.Task, showFacts: factsOf(it), BodyText: it.Body, MentionedBy: toMentionRefs(mentions)}
	default:
		return showTaskView{Task: it.Task, showFacts: factsOf(it), BodyText: it.Body}
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
				printTaskDetailWithBacklinks(a, entries[i].Task, entries[i].Task.Body, mentionsAt(i))
			} else {
				printTaskDetail(a, entries[i].Task, entries[i].Task.Body)
			}
		}
	})
}

// repeatAnchorNote names the series start beside the rule. Without it the rule
// alone cannot be read: FREQ=MONTHLY says nothing about WHICH day, which lives
// in the anchor the rule is expanded from.
func repeatAnchorNote(a *app.App, t *core.Task) string {
	if t.RepeatAnchor == nil {
		return ""
	}
	return " (since " + calendarTime(a, *t.RepeatAnchor) + ")"
}

// dueDetail renders a due stamp for the `show` block: the board-calendar
// timestamp plus the state, so "when" and "is that a problem?" are one line instead of a date
// the reader has to compare against today by hand. Empty when there is no date.
//
// The clock is the process's own (time.Now) — a rendered marker is read by a
// human, now, on this machine; the app layer is where an injected clock matters
// (lint/brief must be reproducible). The ZONE is the board's, because a due is a
// promise the board made in its calendar: reading it in the viewer's zone put
// "(today)" next to tomorrow's date on any board whose calendar sits west of the
// reader.
func dueDetail(a *app.App, t *core.Task) string {
	if t.Due == nil {
		return ""
	}
	s := calendarTime(a, *t.Due)
	switch a.DueDisplayState(t) {
	case core.DueOverdue:
		s += "  (overdue)"
	case core.DueToday:
		s += "  (today)"
	}
	return s
}

// epicDetailLine renders the `epic:` value: the membership id followed by the
// box's title, so reading one task no longer means a second `epic show` to learn
// which box "e-k3m9" is (t-3acv; six drill runs and an independent refutation
// agent each stopped at the bare id). A CLOSED box is annotated, an open one is
// not — the dueDetail rule: spend the width on the abnormal state, since an open
// box is what every filed task is supposed to have. ref is nil when the
// membership names no box at all; the bare id is then the honest rendering, and
// lint's epic-missing is what calls it a defect.
func epicDetailLine(id string, ref *app.EpicRef) string {
	if ref == nil || ref.Title == "" {
		return id
	}
	line := id + "  " + ref.Title
	if ref.State != "" && ref.State != "open" {
		line += "  (" + ref.State + ")"
	}
	return line
}

// printTaskDetail renders a single task's human detail block for `show`. JSON
// and NDJSON are handled one layer up in emitShow/showView (which is where the
// --no-body / --backlinks shape lives), so this is the human path only.
//
// It takes the whole ShowItem, not a bare *core.Task, because the two lines that
// name OTHER entities (epic, deps) must print what those entities ARE, which the
// shard's bare ids cannot say — see epicDetailLine and the deps block.
func printTaskDetail(a *app.App, it *app.ShowItem, body string) {
	t := &it.Task
	fmt.Fprintf(out, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(out, "status:   %s\n", t.Status)
	if t.Epic != "" {
		fmt.Fprintf(out, "epic:     %s\n", epicDetailLine(t.Epic, it.EpicRef))
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
		// The tally is the checklist's (t-c7kw): a derived N/M a reader would
		// otherwise compute by eye, and here it IS the question — all deps done
		// is what lets the task move. Its unsatisfied half is blocked_by,
		// computed by the app helper every other view already uses, so `show`
		// cannot disagree with `ls`, `next`, `--tree` or `brief` about what is
		// in the way.
		//
		// The rows go through printTaskRefs — `dep --list`'s OWN renderer, not a
		// copy of it. Two functions drawing one edge shape is how the two reads
		// would drift apart again, which is the defect this closes; a dangling
		// id therefore keeps that renderer's `[?]` too.
		// The word is the board's DONE LANE, not the literal "done": a board
		// that renamed it (`[lanes] done = "shipped"`) printed `1/2 done` above
		// a `[shipped]` row — measured. stateGlyph reads Cfg.DoneLane for the
		// same reason, and blocked_by is computed against it, so spelling it
		// twice is how the tally and its own rows come to disagree.
		fmt.Fprintf(out, "deps:     %d/%d %s\n", len(t.Deps)-len(it.BlockedBy), len(t.Deps), a.Cfg.DoneLane)
		printTaskRefs(it.Deps)
	}
	if len(t.Refs) > 0 {
		fmt.Fprintf(out, "refs:     %s\n", strings.Join(t.Refs, ", "))
	}
	// The tally leads the items: "which of twelve boxes are ticked" is what a
	// closer asks, and counting boxes by eye is where three drill sessions on
	// a hundred-task board stopped (t-c7kw). JSON carries the items; the
	// count is derived, never stored.
	if n := len(t.Checklist); n > 0 {
		done := 0
		for _, c := range t.Checklist {
			if c.Done {
				done++
			}
		}
		fmt.Fprintf(out, "checklist: %d/%d\n", done, n)
		// The index beside each row is the argument `check` takes. Without it
		// every session counted rows by hand and then opened `check --help` to
		// learn the base (every drill run from the fifth on, t-fsvt). The
		// column is as wide as the last index, so a list past ten rows stays
		// aligned. JSON carries no index: the array position IS the index.
		width := len(strconv.Itoa(n - 1))
		for i, c := range t.Checklist {
			box := "[ ]"
			if c.Done {
				box = "[x]"
			}
			fmt.Fprintf(out, "  %*d  %s %s\n", width, i, box, c.Text)
		}
	}
	if t.Repeat != "" {
		fmt.Fprintf(out, "repeat:   %s%s\n", t.Repeat, repeatAnchorNote(a, t))
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
	showFacts
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
func printTaskDetailWithBacklinks(a *app.App, it *app.ShowItem, body string, mentions []core.Task) {
	printTaskDetail(a, it, body)
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
