package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// scopedQuery resolves the board scope and the repo/epic filters for a read
// command (ls/next/revisit/stats/search). The board scope (a pointer's or
// central board's DefaultRepo, honored when AutoFilter is on) lands in
// ScopeRepo; the explicit -l value is a pure tag filter (Label) that ANDs with
// the scope and never clears it. Scope control is -r only: an explicit -r X
// replaces the board scope with a repo filter (X resolved strictly — a full
// owner/repo, or a short name matching exactly one repo known to the board),
// and -r "" means the whole board. -e resolves through the same strict path
// `epic show` uses (exact id, unique id prefix, unique title substring; a
// miss/ambiguity is exit 2 + candidates), so a typo never silently returns [];
// a command without the flag passes "". The filtering stays silent (stderr
// quiet, stdout pure data).
func scopedQuery(cmd *cobra.Command, a *app.App, flagLabel, flagRepo, flagEpic string) (app.QueryOpts, error) {
	o := app.QueryOpts{Label: flagLabel}
	if a.DefaultRepo != "" && a.AutoFilter {
		o.ScopeRepo = a.DefaultRepo
	}
	if cmd.Flags().Changed("repo") {
		o.ScopeRepo = ""
		if flagRepo != "" {
			r, err := a.ResolveRepo(flagRepo)
			if err != nil {
				return o, err
			}
			o.Repo = r
		}
	}
	if flagEpic != "" {
		id, err := a.ResolveEpic(flagEpic)
		if err != nil {
			return o, err
		}
		o.Epic = id
	}
	return o, nil
}

// labelDidYouMean applies the -l did-you-mean guard after an empty result: an
// explicit tag filter that matched nothing, where the tag uniquely names a
// repo that has tasks, exits 2 with the repo in candidates (the caller almost
// certainly wanted -r). It never fires alongside an explicit -r or --drafts,
// and a tag with >0 matches is unaffected.
func labelDidYouMean(cmd *cobra.Command, a *app.App, o app.QueryOpts, n int) error {
	if n > 0 || o.Label == "" || o.Drafts || cmd.Flags().Changed("repo") {
		return nil
	}
	return a.DidYouMeanRepo(o.Label)
}

// hintEpicScope explains `furrow next`'s active-epic scope on stderr, in the two
// states where the data alone misleads:
//
//   - scope engaged, nothing active: the empty result is deliberate ("no box
//     open"), not "nothing to do" — name the fix, since this is the one state
//     where next's contract differs from the classic board.
//   - an active epic is STUCK (open members, none actionable): next structurally
//     cannot show why it skipped the focus (it just returns rescue tasks or
//     nothing), so it says so — unblocking the box is the day's move.
//
// It is quiet whenever the scope did not engage (-e, --all-epics, an epic-less
// board), so the classic read stays byte-identical.
func hintEpicScope(a *app.App, o app.QueryOpts, tasks []core.Task) {
	scope, err := a.NextScope(o)
	if err != nil || !scope.Engaged {
		return
	}
	if len(scope.Active) == 0 {
		where := o.Repo
		if where == "" {
			where = o.ScopeRepo
		}
		if where == "" {
			where = "this board"
		}
		// A pinned channel can populate the result even with nothing active, so
		// the wording must not call a non-empty listing "empty".
		if len(tasks) > 0 {
			fmt.Fprintf(errOut, "note: no active epic for %s — these are the PINNED box(es)' tasks only; pick a focus with `furrow epic ls` + `furrow epic activate <id>`\n", where)
		} else {
			fmt.Fprintf(errOut, "note: no active epic for %s, so next is deliberately empty — pick a box with `furrow epic ls` + `furrow epic activate <id>` (--all-epics reads the whole board)\n", where)
		}
		return
	}
	items, err := a.EpicList(app.EpicQueryOpts{})
	if err != nil {
		return
	}
	for _, it := range items {
		if scope.Active[it.Epic.ID] && it.Stuck {
			fmt.Fprintf(errOut, "note: focus %s %q is stuck — it has open tasks but none are actionable; `furrow ls -e %s --blocked` shows what is in the way\n",
				it.Epic.ID, it.Epic.Title, it.Epic.ID)
		}
	}
}

// hintHiddenDrafts prints the single stderr hint line when a repo-scoped read
// hid drafts (a draft has no repo, so any repo filter excludes it) — whether
// the filter came from an explicit -r or from the board's auto scope. count
// re-runs the same query with the repo filters swapped for drafts-only;
// stdout stays pure data.
func hintHiddenDrafts(o app.QueryOpts, count func(app.QueryOpts) ([]core.Task, error)) {
	if o.Drafts || (o.Repo == "" && o.ScopeRepo == "") {
		return // a drafts listing hides nothing; nor does an unscoped read
	}
	d := o
	d.Repo, d.ScopeRepo, d.Drafts, d.Limit = "", "", true, 0
	if hidden, err := count(d); err == nil && len(hidden) > 0 {
		fmt.Fprintf(errOut, "%d draft(s) hidden — furrow ls --drafts\n", len(hidden))
	}
}

// hintDue notes on STDERR what has come due, so a `furrow next` — the command a
// session runs when it is deciding what to do — cannot silently skip a promise
// whose day has arrived. It is a note, never a row: `next` hands out what is
// ACTIONABLE, and the tasks that carry dates are usually parked in a lane next
// deliberately excludes, so folding them into the list would change what the
// command means. stdout stays a clean array for `next --json`.
//
// Scope: the due read drops the caller's lane filter (App.Due's contract) but
// keeps the repo scope, so the count matches `furrow brief`'s section.
func hintDue(a *app.App, o app.QueryOpts) {
	o.Query, o.Lanes, o.AllEpics = "", nil, false
	o.Epic, o.Status = "", ""
	sum, err := a.Due(o)
	if err != nil || sum.Empty() {
		return
	}
	if n := len(sum.Overdue); n > 0 {
		fmt.Fprintf(errOut, "note: %d due (%d OVERDUE) — `furrow brief` lists them\n", sum.Total(), n)
		return
	}
	fmt.Fprintf(errOut, "note: %d due today — `furrow brief` lists them\n", sum.Total())
}
