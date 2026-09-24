// `brief`: the session-start read, human and JSON. The band order (due first,
// then the epic header, then next) is the contract CLAUDE.md documents —
// reorder nothing here without the doc.

package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// epicRevisitCounts is the revisit summary's five epic-level keys, labeled as
// the JSON spells them, in one order for every renderer. EVERY epic key the
// summary counts must appear here: Empty() gates the nudges that print these,
// so a key counted but not rendered prints a nudge that names nothing
// (measured with epic_review_due pre-fix). Two renderers carried the list,
// one without that warning (t-3tq4).
func epicRevisitCounts(sum app.RevisitSummary) []struct {
	label string
	ids   []string
} {
	return []struct {
		label string
		ids   []string
	}{
		{"epic_all_done", sum.EpicAllDone}, {"epic_stuck", sum.EpicStuck}, {"epic_stale", sum.EpicStale},
		{"epic_dep_done", sum.EpicDepDone}, {"epic_review_due", sum.EpicReviewDue},
	}
}

// briefView is the JSON shape for `furrow brief` — the one-shot session-orient
// read. Each section keeps the shape of the command it summarizes: next
// entries are show's taskView (task + body_text), blocked entries carry ls's
// blocked_by, revisit is sync's RevisitSummary. next_total is the uncapped
// actionable count and blocked_total the all-open-lanes blocked count, so
// neither the -n cap nor the next-lane band silently hides its own size.
type briefView struct {
	Repo string `json:"repo"`
	// active is the open+active epic(s) next scopes to ([], never null);
	// epics_declared tells the two empty cases apart (false = the board has no
	// boxes and the scope discipline is off; true = empty means nothing active,
	// so next is deliberately empty).
	Active        []epicView `json:"active"`
	EpicsDeclared bool       `json:"epics_declared"`
	// pinned is the open pinned box(es) not already in active — the always-
	// visible channels whose tasks lead next (omitted when none, so the
	// pre-v7 shape is unchanged on a board that uses no pins). Only channels
	// with an open member are listed; pinned_quiet counts the ones hidden
	// because everything inside is closed (the next_total pattern — a filter
	// never hides its size silently).
	Pinned      []epicView `json:"pinned,omitempty"`
	PinnedQuiet int        `json:"pinned_quiet,omitempty"`
	// due is what has come due — {overdue, today}, each an ls row (task +
	// actionable/blocked_by). It leads the object because it leads the printed
	// dashboard, and struct order IS key order; omitted entirely when nothing is
	// due, so the pre-v8 shape is unchanged on a board that dates nothing.
	Due       *briefDueView `json:"due,omitempty"`
	Next      []taskView    `json:"next"`
	NextTotal int           `json:"next_total"`
	// next_hidden is what -n dropped, tallied per lane in [lanes].order and
	// omitted when the cap did not bite — next_total says how many, this says
	// WHAT: the lane order puts in-progress after ready, so the cap
	// structurally drops the work already in flight first, and a session must
	// see "1 in-progress hidden" to know not to start a second task.
	NextHidden []laneCountView    `json:"next_hidden,omitempty"`
	Blocked    []briefBlockedView `json:"blocked"`
	// blocked_total is the blocked count across every OPEN lane in the same
	// scope — the next_total pattern applied to a lane filter rather than a cap,
	// so `blocked: []` can never read as "nothing on this board is stuck".
	BlockedTotal int                `json:"blocked_total"`
	Revisit      app.RevisitSummary `json:"revisit"`
	Drafts       int                `json:"drafts"`
	// lint mirrors sync's ride-along: omitted when clean, error counts by code
	// otherwise.
	Lint *app.LintErrorSummary `json:"lint,omitempty"`
}

// briefDueView is brief's due section: the promises that have arrived. Both
// arrays are always present ([] never null) whenever the section is emitted at
// all, so a reader can index them without a nil check.
type briefDueView struct {
	Overdue []listItemView `json:"overdue"`
	Today   []listItemView `json:"today"`
}

// toBriefDueView converts the app summary, or returns nil when nothing is due —
// which is what keeps the `due` key off a dateless board's brief.
func toBriefDueView(d app.DueSummary) *briefDueView {
	if d.Empty() {
		return nil
	}
	v := &briefDueView{Overdue: []listItemView{}, Today: []listItemView{}}
	for _, it := range d.Overdue {
		v.Overdue = append(v.Overdue, toListItemView(it))
	}
	for _, it := range d.Today {
		v.Today = append(v.Today, toListItemView(it))
	}
	return v
}

// briefBlockedView is a brief blocked entry: the task plus what is in the way.
// core.Task is embedded — the usual warning applies (it must never grow a
// MarshalJSON, or blocked_by would silently vanish).
type briefBlockedView struct {
	core.Task
	BlockedBy []string `json:"blocked_by"`
}

// printBrief renders the session-orient read: JSON/NDJSON as one object (the
// single-object command convention), human mode as a compact dashboard WITHOUT
// bodies (prose is --json's payload for agents; a human runs `show`).
func printBrief(a *app.App, b *app.BriefData, scope string) {
	if jsonMode() {
		v := briefView{
			Repo:          scope,
			Active:        make([]epicView, 0, len(b.Active)),
			EpicsDeclared: b.EpicsDeclared,
			Next:          make([]taskView, 0, len(b.Next)),
			NextTotal:     b.NextTotal,
			BlockedTotal:  b.BlockedTotal,
			Due:           toBriefDueView(b.Due),
			Blocked:       make([]briefBlockedView, 0, len(b.Blocked)),
			Revisit:       b.Revisit,
			Drafts:        b.Drafts,
			Lint:          lintPtr(b.Lint),
		}
		for _, it := range b.Active {
			v.Active = append(v.Active, toEpicView(it))
		}
		for _, it := range b.Pinned {
			v.Pinned = append(v.Pinned, toEpicView(it))
		}
		v.PinnedQuiet = b.PinnedQuiet
		// sync hides an empty summary behind omitempty; brief always shows it,
		// so the two non-omitempty id arrays must come out [] never null (an
		// agent indexes them unconditionally — the house nil-slice rule).
		if v.Revisit.DepDone == nil {
			v.Revisit.DepDone = []string{}
		}
		if v.Revisit.Stale == nil {
			v.Revisit.Stale = []string{}
		}
		for _, it := range b.Next {
			v.Next = append(v.Next, taskView{Task: it.Task, BodyText: it.Body})
		}
		for _, h := range b.NextHidden {
			v.NextHidden = append(v.NextHidden, laneCountView{Lane: h.Key, Count: h.Count})
		}
		for _, it := range b.Blocked {
			blockedBy := it.BlockedBy
			if blockedBy == nil {
				blockedBy = []string{}
			}
			v.Blocked = append(v.Blocked, briefBlockedView{Task: it.Task, BlockedBy: blockedBy})
		}
		emitObject(v)
		return
	}
	if scope != "" {
		fmt.Fprintf(out, "repo: %s\n", scope)
	}
	// What has come DUE leads the dashboard: it is the only section that expires.
	// Printed only when there is something to say — an empty band every session
	// would train the eye to skip the place the dates land.
	if !b.Due.Empty() {
		fmt.Fprintf(out, "due (%d):\n", b.Due.Total())
		for _, it := range b.Due.Overdue {
			row := fmt.Sprintf("  ! %s  %-12s %s  (overdue %s)", it.Task.ID, it.Task.Status, it.Task.Title, calendarTime(a, *it.Task.Due))
			fmt.Fprintln(out, withTags(row, repeatTag(&it.Task)))
		}
		for _, it := range b.Due.Today {
			row := fmt.Sprintf("  · %s  %-12s %s  (today %s)", it.Task.ID, it.Task.Status, it.Task.Title, calendarTime(a, *it.Task.Due))
			fmt.Fprintln(out, withTags(row, repeatTag(&it.Task)))
		}
	}
	// The focus header, only on a participating board: which box `next` is
	// scoped to — or the fact that none is, which is WHY next is empty.
	if b.EpicsDeclared {
		if len(b.Active) == 0 {
			fmt.Fprintln(out, "epic: (none active — next is empty until `furrow epic activate <id>`; pick with `furrow epic ls`)")
		}
		for _, it := range b.Active {
			line := fmt.Sprintf("epic: ▶ %s  %d/%d  %s", it.Epic.ID, it.Progress.Done, it.Progress.Total, it.Epic.Title)
			if len(it.OpenDeps) > 0 {
				line += "  ⏳ waits: " + strings.Join(it.OpenDeps, ", ")
			}
			if it.Waiting != nil {
				line += "  " + waitingUntil(a, it.Waiting)
			}
			fmt.Fprintln(out, line)
		}
		// The pinned channels ride under the focus line: their tasks lead next,
		// so the header must say which boxes injected them.
		for _, it := range b.Pinned {
			fmt.Fprintf(out, "epic: 📌 %s  %d/%d  %s\n", it.Epic.ID, it.Progress.Done, it.Progress.Total, it.Epic.Title)
		}
		if b.PinnedQuiet > 0 {
			fmt.Fprintf(out, "epic: 📌 +%d quiet pinned (no open members)\n", b.PinnedQuiet)
		}
	}
	// The header names what the cap dropped, by lane: "3/5" alone reads as
	// "three of the same", while "1 in-progress" below the fold is the one
	// row a session must not overlook.
	fmt.Fprintf(out, "next (%d/%d%s):\n", len(b.Next), b.NextTotal, hiddenByLane(b.NextHidden))
	if len(b.Next) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	// The band carries no date (the due band above is where those land) but it
	// does carry the repeat tag: a rule has no band of its own, and this is the
	// row a session actually closes.
	for _, it := range b.Next {
		row := fmt.Sprintf("  ★ %s  %-12s %s", it.Task.ID, it.Task.Status, it.Task.Title)
		fmt.Fprintln(out, withTags(row, repeatTag(&it.Task)))
	}
	// The band names its own lanes and the all-open-lanes count beside them: a
	// bare "blocked (0)" once read as "nothing is stuck" on a board where most
	// open work was waiting on a dep outside these lanes.
	fmt.Fprintf(out, "blocked (%d in %s / %d in all open lanes):\n",
		len(b.Blocked), strings.Join(a.Cfg.NextLanes, "+"), b.BlockedTotal)
	if len(b.Blocked) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, it := range b.Blocked {
		// The tag sits before the ← edge, as it does on a tree node: the arrow
		// terminates the row.
		row := withTags(fmt.Sprintf("  · %s  %-12s %s", it.Task.ID, it.Task.Status, it.Task.Title), repeatTag(&it.Task))
		fmt.Fprintf(out, "%s  ← %s\n", row, strings.Join(it.BlockedBy, ", "))
	}
	fmt.Fprintf(out, "revisit: %d dep_done, %d stale", len(b.Revisit.DepDone), len(b.Revisit.Stale))
	for _, c := range epicRevisitCounts(b.Revisit) {
		if n := len(c.ids); n > 0 {
			fmt.Fprintf(out, ", %d %s", n, c.label)
		}
	}
	if n := len(b.Revisit.Unreviewed); n > 0 {
		fmt.Fprintf(out, ", %d unreviewed", n)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "drafts: %d\n", b.Drafts)
	if b.Lint.Errors > 0 {
		fmt.Fprintln(out, lintLine(b.Lint))
	}
}

// hiddenByLane renders next_hidden for the band header: "" when the cap did
// not bite, else " — N hidden by -n: 1 ready, 1 in-progress" in lane order.
func hiddenByLane(hidden []app.StatCount) string {
	if len(hidden) == 0 {
		return ""
	}
	total := 0
	parts := make([]string, 0, len(hidden))
	for _, h := range hidden {
		total += h.Count
		parts = append(parts, fmt.Sprintf("%d %s", h.Count, h.Key))
	}
	return fmt.Sprintf(" — %d hidden by -n: %s", total, strings.Join(parts, ", "))
}
