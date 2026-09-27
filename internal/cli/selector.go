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
	filterFlags
	query string
	yes   bool
}

// addSelectorFlags registers the selection flags on a write command. The -q
// registrar is the read side's (addQueryFlag) and -l/-r go through the shared
// filter registrar, so the help text and grammar pointer can never fork from
// ls/next's — the usage lines are overridden because here the flags SELECT
// the targets of a write rather than filter a listing.
func addSelectorFlags(cmd *cobra.Command, s *writeSelector) {
	addQueryFlag(cmd, &s.query)
	addFilterFlags(cmd, &s.filterFlags,
		wantUsage("label", "select by label instead of ids (OR; comma-separated or repeated -l); ANDs with -q/-r and the board scope"),
		wantUsage("repo", "select within this repo instead of the board scope (owner/repo or a unique short name; '' = whole board)"))
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

// previewTail is what a write-specific preview adds to the generic one: a tag
// per human row (what THIS write would make of the row — a shifted due — which
// a listing of the matches alone cannot say), and the rows the all-or-nothing
// apply would REFUSE. A preview must not promise a write the apply will not
// make, so with any refused row the count line says 0 will be written, the
// closing line names the remedy instead of the re-run, and the JSON object
// carries the refused ids under key beside {dry_run, tasks}.
type previewTail struct {
	tag     func(*core.Task) string // per-row tag, after the due; "" for none
	refused func(*core.Task) bool   // a row that makes --yes exit 2
	key     string                  // JSON key for the refused ids
	why     string                  // what such a row lacks ("carry no due to shift")
	remedy  string                  // the human closing line when any row is refused
}

// emitSelectPreview renders the would-be write without performing it —
// archive's preview contract: JSON/NDJSON emit {dry_run: true, tasks} (an
// OBJECT, distinguishable by dry_run from the apply's envelope array), human
// mode lists the matches and names the re-run. action is the whole verb
// phrase ("close", "move to ready", "set"); tail is nil for a write that has
// nothing to add to the generic rendering.
func emitSelectPreview(a *app.App, action string, tasks []core.Task, tail *previewTail) {
	if tasks == nil {
		tasks = []core.Task{} // array shape, never null
	}
	var refused []string
	if tail != nil && tail.refused != nil {
		for i := range tasks {
			if tail.refused(&tasks[i]) {
				refused = append(refused, tasks[i].ID)
			}
		}
	}
	if jsonMode() {
		obj := map[string]any{"dry_run": true, "tasks": tasks}
		if len(refused) > 0 {
			obj[tail.key] = refused
		}
		emitObject(obj)
		return
	}
	if len(refused) > 0 {
		fmt.Fprintf(out, "would %s 0 of %d task(s): %d %s, and the write is all-or-nothing\n", action, len(tasks), len(refused), tail.why)
	} else {
		fmt.Fprintf(out, "would %s %d task(s)\n", action, len(tasks))
	}
	for _, t := range tasks {
		// The shared row tags, because this row IS a task rendered as a task —
		// and it is the row a close is launched from, which is the case
		// repeatTag exists for. Untagged, the gate on a write that MINTS tasks
		// showed a repeating match and a one-off match as the same line.
		extra := ""
		if tail != nil && tail.tag != nil {
			extra = tail.tag(&t)
		}
		fmt.Fprintf(out, "  %s\n", withTags(
			fmt.Sprintf("%s  [%s] %s", t.ID, t.Status, t.Title),
			dueTag(a, &t), extra, repeatTag(&t)))
	}
	switch {
	case len(refused) > 0:
		fmt.Fprintln(out, tail.remedy)
	case len(tasks) > 0:
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
