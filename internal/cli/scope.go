package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// scopedQuery resolves the board scope and the repo filter for a read command
// (ls/next/revisit). The board scope (a pointer's or central board's
// DefaultRepo, honored when AutoFilter is on) lands in ScopeRepo; the explicit
// -l value is a pure tag filter (Label) that ANDs with the scope and never
// clears it. Scope control is -r only: an explicit -r X replaces the board
// scope with a repo filter (X resolved strictly — a full owner/repo, or a
// short name matching exactly one repo known to the board), and -r "" means
// the whole board. The filtering stays silent (stderr quiet, stdout pure data).
func scopedQuery(cmd *cobra.Command, a *app.App, flagLabel, flagRepo string) (app.QueryOpts, error) {
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
