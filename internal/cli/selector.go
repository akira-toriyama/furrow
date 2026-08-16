package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// Write-side selection. set/done/move can target tasks by the SAME filters the
// reads use — the -q typed query, the -l tag filter, the -r repo scope —
// instead of enumerating ids, so a bulk triage no longer needs
// `ls -q … --json | jq -r '.[].id' | xargs furrow …` (which ARG_MAX can split
// into partial applies, breaking the batch mutators' all-or-nothing contract).
// The predicate is the read side's own (scopedQuery + app.List): no new
// grammar, and `furrow ls <same flags>` previews exactly what a write would
// touch, board scope included (-r ” escapes it, as everywhere).
//
// The contract, shared by all three commands:
//   - a selection and an id list are DIFFERENT ways of naming the targets, so
//     they refuse to combine (ids enumerate; filters describe);
//   - a selection previews unless --yes (archive/tidy's destructive-op guard:
//     the whole point of a filter is that you did not count the matches);
//     --yes without a selection is refused, never silently ignored;
//   - matching NOTHING is exit 0 (the read side's "an empty listing is a valid
//     result"), disclosed with one stderr note;
//   - --expect-updated is refused beside a selection (one stamp = one read of
//     ONE task, and a selection is however many the filters matched).
type writeSelector struct {
	query string
	label []string
	repo  string
	yes   bool
}

// addSelectorFlags registers the selection flags on a write command. The -q
// registrar is the read side's (addQueryFlag), so the help text and grammar
// pointer can never fork from ls/next's.
func addSelectorFlags(cmd *cobra.Command, s *writeSelector) {
	addQueryFlag(cmd, &s.query)
	cmd.Flags().StringArrayVarP(&s.label, "label", "l", nil, "select by label instead of ids (OR; comma-separated or repeated -l); ANDs with -q/-r and the board scope")
	cmd.Flags().StringVarP(&s.repo, "repo", "r", "", "select within this repo instead of the board scope (owner/repo or a unique short name; '' = whole board)")
	cmd.Flags().BoolVar(&s.yes, "yes", false, "apply the -q/-l/-r selection (without it the selection only previews)")
}

// active reports whether the caller selected by filter — any of -q/-l/-r was
// passed (an explicitly-empty value still counts: `-q ”` deliberately matches
// the whole scope, and the preview guard is what makes that survivable).
func (s *writeSelector) active(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("query") || cmd.Flags().Changed("label") || cmd.Flags().Changed("repo")
}

// guard enforces the parts of the contract every command shares, before any
// resolution: no ids beside a selection, no --expect-updated beside one, and
// no --yes without one.
func (s *writeSelector) guard(cmd *cobra.Command, ids []string) error {
	if !s.active(cmd) {
		if s.yes {
			return core.Validationf("", "--yes confirms a -q/-l/-r selection; with explicit ids there is no preview to confirm")
		}
		return nil
	}
	if len(ids) > 0 {
		return core.Validationf("", "ids and -q/-l/-r name the targets two different ways — enumerate ids OR select by filter, not both")
	}
	if cmd.Flags().Changed(expectUpdatedFlag) {
		return core.Validationf("", "--expect-updated describes one read of one task; it cannot ride a -q/-l/-r selection")
	}
	return nil
}

// resolve runs the selection through the read path: the board scope and -r/-l
// resolve exactly as in ls (scopedQuery), -q compiles through the same typed
// grammar, and the read-side honesty hooks fire too — a -l that matched
// nothing but names a repo exits 2 with candidates, and a scope that hides
// drafts says so on stderr. Returns the matched tasks in canonical order.
func (s *writeSelector) resolve(cmd *cobra.Command, a *app.App) ([]core.Task, error) {
	o, err := scopedQuery(cmd, a, joinOrFilter(s.label), s.repo, "")
	if err != nil {
		return nil, err
	}
	o.Query = s.query
	tasks, err := a.List(o)
	if err != nil {
		return nil, err
	}
	if err := labelDidYouMean(cmd, a, o, len(tasks)); err != nil {
		return nil, err
	}
	hintHiddenDrafts(o, a.List, "-r '' escapes the board scope")
	return tasks, nil
}

// taskIDs projects the matched tasks to the id list the batch mutators take.
func taskIDs(tasks []core.Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

// emitSelectPreview renders the would-be write without performing it —
// archive's preview contract: JSON/NDJSON emit {dry_run: true, tasks} (an
// OBJECT, distinguishable by dry_run from the apply's envelope array), human
// mode lists the matches and names the re-run. action is the whole verb
// phrase ("close", "move to ready", "set").
func emitSelectPreview(action string, tasks []core.Task) {
	if tasks == nil {
		tasks = []core.Task{} // array shape, never null
	}
	if jsonMode() {
		emitObject(map[string]any{"dry_run": true, "tasks": tasks})
		return
	}
	fmt.Fprintf(out, "would %s %d task(s)\n", action, len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(out, "  %s  [%s] %s\n", t.ID, t.Status, t.Title)
	}
	if len(tasks) > 0 {
		fmt.Fprintln(out, "re-run with --yes to apply")
	}
}

// emitEmptySelection reports an applied selection that matched nothing: exit 0
// with the read side's empty shape (--json prints [], --ndjson nothing) and
// one stderr note — never a silent no-op, never an error.
func emitEmptySelection() {
	fmt.Fprintln(errOut, "note: the selection matched 0 task(s) — nothing to do")
	if jsonMode() && !flagNDJSON {
		printJSON([]any{})
	}
}
