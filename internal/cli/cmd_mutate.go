package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/app"
	"github.com/akira-toriyama/furrow/v10/internal/core"
	"github.com/spf13/cobra"
)

// The stale-read guard (--expect-updated): furrow's store has no locks, so when
// two sessions work one board, the later write silently wins and the earlier
// session keeps reasoning about a task that has moved under it. The guard is
// the caller declaring "my picture of this task is its updated=<ts>": pass the
// stamp your last read emitted, and when someone else wrote in between, the
// mutation still goes through but says so — a stderr note plus a `stale_read`
// {expected, actual} envelope key. A WARNING by design, not an optimistic-lock
// refusal: the co-writer being caught is usually right too (they acted on newer
// knowledge), so the fix is to re-read and reconcile, not to lose the second
// edit as well. One timestamp describes one read of ONE entity, so the batch
// mutators refuse it alongside several ids (the position-flag precedent).
const expectUpdatedFlag = "expect-updated"

// addExpectUpdatedFlag registers --expect-updated on a mutating command. The
// funnels look the flag up by name, so a command without the registration
// simply has no guard (never a silent ignore of a set flag: cobra rejects an
// unregistered flag at parse time).
func addExpectUpdatedFlag(cmd *cobra.Command) {
	cmd.Flags().String(expectUpdatedFlag, "",
		"the updated stamp from your last read (RFC3339); warns via stale_read when the entity changed since")
}

// expectUpdatedArg reads --expect-updated off cmd: ok=false when the command
// never registered it or the caller didn't pass it. A malformed timestamp is
// exit 2 — an explicit argument is never quietly dropped.
func expectUpdatedArg(cmd *cobra.Command, subject string) (time.Time, bool, error) {
	if cmd == nil {
		return time.Time{}, false, nil
	}
	f := cmd.Flags().Lookup(expectUpdatedFlag)
	if f == nil || f.Value.String() == "" {
		return time.Time{}, false, nil
	}
	ts, err := time.Parse(time.RFC3339, f.Value.String())
	if err != nil {
		return time.Time{}, false, core.Validationf(subject,
			"--expect-updated %q is not an RFC3339 timestamp (pass `updated` exactly as a read emitted it, e.g. 2026-08-09T12:00:00Z)", f.Value.String())
	}
	return ts, true, nil
}

// staleReadExtra compares the pre-mutation `updated` against the caller's
// declared read. Instants are compared (time.Equal), so a +09:00 spelling of
// the same moment matches the stored UTC. On a mismatch it warns on stderr and
// returns the `stale_read` envelope entry; nil when current (the key must not
// appear on a clean write). A nil pre-fetch yields nil too: the mutate closure
// owns not-found errors, and a guard that cannot see the before has nothing
// truthful to say.
func staleReadExtra(id string, actual *time.Time, expected time.Time) map[string]any {
	if actual == nil || actual.Equal(expected) {
		return nil
	}
	a, e := actual.UTC().Format(time.RFC3339), expected.UTC().Format(time.RFC3339)
	fmt.Fprintf(errOut, "note: %s changed since your read (updated %s, you read %s) — the write went through; re-read before editing further\n", id, a, e)
	return map[string]any{"stale_read": map[string]any{"expected": e, "actual": a}}
}

// sessionGuardExtra drains the session guard's per-process output (see
// app/session_guard.go): its stand-down notes and its idle-occupant warning
// go to stderr, and the warning also becomes the `session_warn` envelope key
// ({clashes: [...]}) — nil when the guard had nothing to say, so a clean write
// stays key-free. Draining clears the state, so the envelope annotate and the
// root post-run hook can both call this and only the first one prints.
//
// The envelope carries EVERY clash of this write (a machine reader asks each
// time); the stderr line carries only what is news to this Claude Code
// session (app.UnheardClashes — a pair not yet reported, or an occupant that
// has been active since), so a quiet occupant is one line per session, not
// one per write. Under --json/--ndjson the line is one JSON object on stderr
// (`{"session_warn": {message, clashes}}`) rather than prose, so a caller
// that already parses stderr for the error envelope parses this the same way.
// printNotes drains the app's queued stderr notes — the session guard's
// stand-downs and the write-time notes — in that order, one `note:` line each.
// Shared by the success path (the envelope annotate) and classifyFailure, so a
// note queued before a later failure is printed exactly once either way.
func printNotes(a *app.App) {
	for _, n := range a.TakeSessionNotes() {
		fmt.Fprintln(errOut, "note:", n)
	}
	for _, n := range a.TakeNotes() {
		fmt.Fprintln(errOut, "note:", n)
	}
}

func sessionGuardExtra(a *app.App) map[string]any {
	clashes := a.TakeSessionWarn()
	fresh := a.UnheardClashes(clashes)
	printNotes(a)
	if len(clashes) == 0 {
		return nil
	}
	if len(fresh) > 0 {
		line := app.SessionWarnLine(fresh)
		if jsonMode() {
			emitStderrObject(map[string]any{"session_warn": map[string]any{"message": line, "clashes": fresh}})
		} else {
			fmt.Fprintln(errOut, "warning:", line)
		}
	}
	return map[string]any{"session_warn": map[string]any{"clashes": clashes}}
}

// mergeExtra folds add into extra, allocating only when there is something to
// merge — so the envelope stays key-free on the common clean path.
func mergeExtra(extra, add map[string]any) map[string]any {
	if len(add) == 0 {
		return extra
	}
	if extra == nil {
		extra = map[string]any{}
	}
	for k, v := range add {
		extra[k] = v
	}
	return extra
}

// emitMutation runs a single-task edit on id and reports it. In machine mode
// (--json or --ndjson) it snapshots the task before the change and prints
// {before, after, changed}, so an agent sees the effect inline without a
// follow-up `show`. The pre-fetch is skipped (and harmless) in human mode
// unless the stale-read guard needs it; the mutate closure is the
// authoritative source of any not-found / validation error.
func emitMutation(cmd *cobra.Command, a *app.App, verb, id string, mutate func() (*core.Task, error)) error {
	return emitMutationWith(cmd, a, verb, id, mutate, nil)
}

// emitMutationWith is emitMutation plus an optional `annotate`: given the
// resulting task it returns extra top-level fields to merge into the --json
// {before,after,changed} envelope (and may write a human note to stderr).
func emitMutationWith(cmd *cobra.Command, a *app.App, verb, id string, mutate func() (*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	return emitMutationVerb(cmd, a, func() string { return verb }, id, mutate, annotate)
}

// emitMutationVerb is emitMutationWith with the human line's verb read AFTER
// mutate, for a write that can turn out to be a no-op it must name (`edit
// --body` of the stored body says "unchanged").
func emitMutationVerb(cmd *cobra.Command, a *app.App, verb func() string, id string, mutate func() (*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	expected, guard, gerr := expectUpdatedArg(cmd, id)
	if gerr != nil {
		return gerr
	}
	var before *core.Task
	if guard || jsonMode() {
		if b, _, err := a.Get(id); err == nil {
			before = b
		}
	}
	after, err := mutate()
	if err != nil {
		return err
	}
	var extra map[string]any
	if annotate != nil {
		extra = annotate(after)
	}
	if guard && before != nil {
		extra = mergeExtra(extra, staleReadExtra(before.ID, &before.Updated, expected))
	}
	extra = mergeExtra(extra, sessionGuardExtra(a))
	printMutation(verb(), before, after, extra)
	return nil
}

// emitMutationManyWith is emitMutation for a batch mutator: one {before,after,
// changed} envelope per task, in the batch's (deduped) input order — --json
// ALWAYS an array (a single id is a one-element array: a command whose Use
// says `<id>...` has array cardinality by SIGNATURE, and the runtime argv
// length must not fork the shape — the always-array rule), --ndjson one
// envelope per line, human mode one verb line per task. Befores come from one
// batch read; a miss there is harmless because the mutate closure is the
// authority and fails the whole batch before anything is printed. The optional
// annotate: given a resulting task it returns extra top-level fields merged
// into THAT task's envelope — the batch twin of emitMutationWith's annotate (done --note
// surfaces `appended` on each, a single-id set its `clamped`/`renumbered`).
func emitMutationManyWith(cmd *cobra.Command, a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error), annotate func(after *core.Task) map[string]any) error {
	return emitMutationManyTagged(cmd, a, verb, ids, mutate, annotate, nil)
}

// emitMutationManyTagged is emitMutationManyWith with a per-task row tag on the
// HUMAN line: what the write just made of a field the reader asked to change,
// so a `set --due`/`--due-shift` echoes the instant it bound (in the board's
// calendar, the form every row renders a due in) instead of leaving the caller
// to `show` it back — seven drill runs computed the resulting dates by hand
// because nothing echoed them. nil tags nothing; JSON is untouched, its
// envelopes already carry before/after.
func emitMutationManyTagged(cmd *cobra.Command, a *app.App, verb string, ids []string, mutate func() ([]*core.Task, error), annotate func(after *core.Task) map[string]any, tag func(*core.Task) string) error {
	expected, guard, gerr := expectUpdatedArg(cmd, strings.Join(ids, ","))
	if gerr != nil {
		return gerr
	}
	if guard && len(ids) > 1 {
		// Same rule as set's position flags: one --expect-updated describes one
		// read of one task, so it cannot ride a several-id batch.
		return core.Validationf("", "--expect-updated describes one read of one task; it cannot apply to %d ids", len(ids))
	}
	befores := map[string]*core.Task{}
	if guard || jsonMode() {
		if items, _, err := a.GetBatch(ids, false); err == nil {
			for i := range items {
				t := items[i].Task
				befores[t.ID] = &t
			}
		}
	}
	after, err := mutate()
	if err != nil {
		return err
	}
	var stale map[string]any
	if guard {
		if b := befores[ids[0]]; b != nil {
			stale = staleReadExtra(b.ID, &b.Updated, expected)
		}
	}
	// The session warning describes the whole batch write, so unlike the
	// one-task stale_read it rides on EVERY envelope: a consumer reading any
	// element sees it.
	session := sessionGuardExtra(a)
	// annotate runs for every task in EVERY mode: its stderr side — the clamp
	// and respace notes — is the human reader's only disclosure, and gating it
	// on jsonMode was how `set --value 9` came to round silently while `value 9`
	// (through emitMutationWith, which always annotates) said so (t-hs4a).
	extras := make([]map[string]any, len(after))
	if annotate != nil {
		for i, t := range after {
			extras[i] = annotate(t)
		}
	}
	if jsonMode() {
		envs := make([]any, 0, len(after))
		for i, t := range after {
			extra := extras[i]
			if t.ID == ids[0] {
				extra = mergeExtra(extra, stale)
			}
			extra = mergeExtra(extra, session)
			envs = append(envs, mutationEnvelope(befores[t.ID], t, extra))
		}
		if flagNDJSON {
			for _, e := range envs {
				printNDJSONValue(e)
			}
			return nil
		}
		printJSON(envs)
		return nil
	}
	for _, t := range after {
		line := fmt.Sprintf("%s %s  %s", verb, t.ID, t.Title)
		if tag != nil {
			line = withTags(line, tag(t))
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

// readTextArg resolves a free-text argument that may be "-" — the shared
// `-`=stdin convention across `add --body`, `note <text>`, and `done --note`,
// so `-` never means one thing in one command and a literal dash in another.
// A value other than "-" is returned verbatim (including ""); "-" reads ALL of
// stdin and returns it unmodified (callers trim for display as needed).
func readTextArg(cmd *cobra.Command, s string) (string, error) {
	if s != "-" {
		return s, nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", core.Internalf("", "read stdin: %v", err)
	}
	return string(data), nil
}

func newDoneCmd() *cobra.Command {
	var note string
	var sel writeSelector
	cmd := &cobra.Command{
		Use:   "done [<id>...]",
		Short: "Move tasks into the done lane (stamps closed)",
		Long: "Close one or more tasks in a single index write, all-or-nothing: a batch\n" +
			"with an unknown id closes NOTHING and exits 1 with every miss in\n" +
			"details.missing (the show batch shape). --json is ALWAYS an array of\n" +
			"{before,after,changed} envelopes, one per id (a single id is a one-element\n" +
			"array); --ndjson streams one envelope per line. Entering the done lane\n" +
			"stamps `closed`; leaving it (a reopen) clears it.\n\n" +
			"A close settles the TASK, not its checklist: unticked items neither block\n" +
			"nor warn (the checklist is the task's own breakdown, and what a close asserts\n" +
			"is the body's completion condition), so read `furrow show <id>` — its\n" +
			"`checklist: N/M` line is the tally — before deciding a task is done.\n\n" +
			"That condition can be MET or GONE: a task whose premise died closes the\n" +
			"same way and stamps the same `closed` — furrow has no cancelled close and\n" +
			"no close-reason field (docs/non-goals.md). Say which in --note; park it in\n" +
			"the icebox instead only when it may still come back.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters: -q (the ls/next typed query, same grammar), -l, and -r, which AND\n" +
			"together under the board scope exactly as in ls — so `furrow ls <flags>`\n" +
			"previews precisely what `furrow done <flags>` would close. A selection and\n" +
			"an id list refuse to combine (exit 2). A selection only PREVIEWS the\n" +
			"matched tasks until --yes (the archive/tidy destructive-op guard; the\n" +
			"preview is {dry_run: true, tasks} in JSON), the close itself is the same\n" +
			"single all-or-nothing write as the id form — no jq, no xargs, no ARG_MAX\n" +
			"split — and a selection matching nothing is exit 0 with a stderr note, like\n" +
			"every empty read. --expect-updated cannot ride a selection (one stamp\n" +
			"describes one read of one task).\n\n" +
			"--note \"<text>\" records the closing word in the same command: the text is\n" +
			"appended to EVERY closed task's body as a new paragraph (the note command's\n" +
			"contract — updated advances, nothing is deduped) and the envelope gains the\n" +
			"same `appended` key. Pass `-` to read the note from stdin; an empty note is\n" +
			"exit 2, never a silent plain close.\n\n" +
			"Closing a REPEATING task is what advances its series: the next occurrence\n" +
			"is written in the same all-or-nothing write and the rule handed over to\n" +
			"it, so exactly one live task carries a series and re-closing (or reopen\n" +
			"then close) mints nothing. The occurrence is born in the default lane with\n" +
			"the body and checklist copied (boxes unchecked, the body as it stood before\n" +
			"this close's --note) under a `previous: [[id]]` line, inheriting everything\n" +
			"but what the close settled (closed, reviewed), what the rule computes\n" +
			"(due), this run's deps and its position — the epic included, whatever\n" +
			"state that box is in. Its due is the first occurrence after what the close\n" +
			"SETTLES: for a bare-date series the whole board-calendar day of the later\n" +
			"of now and the due, for a timed one the later of the two as instants — so\n" +
			"an on-time or early close advances one step, a snoozed one never hands the\n" +
			"same day back, and a late one jumps the lapsed cycles and says so:\n" +
			"`<closed-id>  repeat: next due <when> (<new-id>) — N occurrence(s)\n" +
			"skipped`, or `repeat: series complete` when the rule is spent. A close\n" +
			"just past midnight settles the NEW day. --json carries `repeat`\n" +
			"{created, due, skipped, completed} on the envelope. The calendar is the\n" +
			"board's [due].timezone — undeclared, the zone of the machine that closes,\n" +
			"which `furrow lint` errors on, whatever the board's mode (repeat-no-timezone).",
		Example: "  furrow done t-k3m9p\n" +
			"  furrow done t-k3m9p --note \"→ continued in t-x7q2m\"\n" +
			"  furrow done t-k3m9p t-x7q2m t-9d4nk   # triage sweep, one write\n" +
			"  furrow done -q 'label:spike status:waiting'        # preview the selection\n" +
			"  furrow done -q 'label:spike status:waiting' --yes  # close it, one write",
		Args: sel.args,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := sel.guard(cmd, args); err != nil {
				return err
			}
			if sel.active(cmd) {
				tasks, err := sel.resolve(cmd, a)
				if err != nil {
					return err
				}
				if !sel.yes {
					emitSelectPreview(a, "close", tasks, nil)
					return nil
				}
				if len(tasks) == 0 {
					emitEmptySelection()
					return nil
				}
				args = taskIDs(tasks)
			}
			rs, after := newSeriesReports(), []*core.Task(nil)
			var notePtr *string
			annotate := rs.annotate
			if cmd.Flags().Changed("note") {
				text, terr := readTextArg(cmd, note)
				if terr != nil {
					return terr
				}
				notePtr = &text
				// `changed` tracks metadata only, so surface the note's effect the
				// way the note command does.
				trimmed := strings.TrimRight(text, "\n")
				annotate = func(t *core.Task) map[string]any {
					return mergeExtra(map[string]any{"appended": trimmed}, rs.annotate(t))
				}
			}
			if err := emitMutationManyWith(cmd, a, "done", args, func() ([]*core.Task, error) {
				ts, reps, err := a.DoneMany(args, notePtr)
				rs.collect(ts, reps)
				after = ts
				return ts, err
			}, annotate); err != nil {
				return err
			}
			rs.print(a, after)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "append this closing note to each task's body ('-' reads stdin)")
	addSelectorFlags(cmd, &sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newMoveCmd() *cobra.Command {
	var sel writeSelector
	cmd := &cobra.Command{
		Use:   "move [<id>...] <lane>",
		Short: "Move tasks to a lane",
		Long: "Move one or more tasks to <lane> (the LAST argument) in a single index\n" +
			"write, all-or-nothing: a batch with an unknown id moves NOTHING and exits 1\n" +
			"with every miss in details.missing; an unknown lane is exit 2 with the\n" +
			"configured lanes in candidates. --json is ALWAYS an array of\n" +
			"{before,after,changed} envelopes, one per id (a single id is a one-element\n" +
			"array); --ndjson streams one envelope per line. Entering the done lane\n" +
			"stamps `closed`; leaving it (a reopen) clears it.\n\n" +
			"A close settles the TASK, not its checklist: unticked items neither block\n" +
			"nor warn (the checklist is the task's own breakdown, and what a close asserts\n" +
			"is the body's completion condition), so read `furrow show <id>` — its\n" +
			"`checklist: N/M` line is the tally — before deciding a task is done.\n\n" +
			"A task keeps its priority across lanes (parked and brought back, it keeps\n" +
			"its place), but priority only orders WITHIN a lane and each lane respaces on\n" +
			"its own, so the destination may already hold that value: the mover then\n" +
			"lands at the destination's tail instead of on the tie, disclosed in a stderr\n" +
			"note (`furrow reorder` positions it). Set -s does the same.\n\n" +
			"Instead of enumerating ids, SELECT the targets with the read side's own\n" +
			"filters — -q/-l/-r, ANDed under the board scope exactly as in ls (then\n" +
			"`move <flags> <lane>` takes just the lane). The selection previews until\n" +
			"--yes ({dry_run: true, tasks} in JSON), applies as the same single\n" +
			"all-or-nothing write, matches-nothing is exit 0 with a stderr note, and it\n" +
			"refuses to combine with ids or --expect-updated (exit 2) — the full\n" +
			"contract is spelled out in `furrow done --help`.",
		Example: "  furrow move t-k3m9p in-progress\n" +
			"  furrow move t-k3m9p t-x7q2m backlog     # triage sweep, one write\n" +
			"  furrow move -q 'status:inbox no:value' icebox        # preview\n" +
			"  furrow move -q 'status:inbox no:value' icebox --yes  # one write",
		Args: func(cmd *cobra.Command, args []string) error {
			if sel.active(cmd) || cmd.Flags().Changed("yes") {
				return cobra.MinimumNArgs(1)(cmd, args) // <lane> only; extra ids get guard's message
			}
			return cobra.MinimumNArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			ids, lane := args[:len(args)-1], args[len(args)-1]
			if err := sel.guard(cmd, ids); err != nil {
				return err
			}
			if sel.active(cmd) {
				// Vet the lane BEFORE resolving: `move -q … typo-lane` must exit 2
				// with candidates whether or not anything matches, and a preview
				// against a lane the apply would refuse is a lie.
				if err := a.CheckLane(lane); err != nil {
					return err
				}
				tasks, err := sel.resolve(cmd, a)
				if err != nil {
					return err
				}
				if !sel.yes {
					emitSelectPreview(a, "move to "+lane, tasks, nil)
					return nil
				}
				if len(tasks) == 0 {
					emitEmptySelection()
					return nil
				}
				ids = taskIDs(tasks)
			}
			rs, after := newSeriesReports(), []*core.Task(nil)
			if err := emitMutationManyWith(cmd, a, "moved", ids, func() ([]*core.Task, error) {
				ts, reps, err := a.MoveMany(ids, lane, nil)
				rs.collect(ts, reps)
				after = ts
				return ts, err
			}, rs.annotate); err != nil {
				return err
			}
			rs.print(a, after)
			return nil
		},
	}
	addSelectorFlags(cmd, &sel)
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newNoteCmd() *cobra.Command {
	var prepend bool
	cmd := &cobra.Command{
		Use:   "note <id> <text>",
		Short: "Append (or --prepend) a paragraph to a task's or epic's body and advance its updated time",
		Long: "Append <text> as a new paragraph to bodies/<id>.md AND stamp the entity's\n" +
			"`updated` time, in one command — the in-band way to record progress, stop-points,\n" +
			"and next steps across sessions. Unlike hand-editing the file (what `furrow\n" +
			"edit` hands an agent), it keeps `updated` honest, so `furrow lint`'s\n" +
			"reconcile-gap check does not misfire on a task whose progress lives only in\n" +
			"its body. Pass `-` as <text> to read the note from stdin (for multi-line or\n" +
			"long notes).\n\n" +
			"--prepend places the paragraph FIRST instead: right after the body's `# title`\n" +
			"line when it has one (an occurrence's `previous: [[id]]` back-link stays above\n" +
			"it), else at the very top — for the \"latest decision at the top\" habit on a\n" +
			"long record, without replacing the whole body through `edit --body`. The\n" +
			"rest of the body is kept byte-for-byte; `--json` then carries `prepended`\n" +
			"instead of `appended`.\n\n" +
			"<id> may name a TASK or an EPIC — the two share the bodies/ directory, so a\n" +
			"box's progress record is the same operation on the same file, and only the\n" +
			"shard that stamps `updated` differs. MEMBERSHIP routes it, not the id's\n" +
			"prefix: a ref naming a real task is always the task, else a ref the epic\n" +
			"store resolves (exact id, unique id prefix, unique title substring — every\n" +
			"other epic reference's contract) is that box. A ref that resolves to neither\n" +
			"fails on the side its prefix suggests: an unknown `e-` id is exit 2 with\n" +
			"candidates, an unknown task id exit 1.",
		Example: "  furrow note t-k3m9p \"検証完了。次: アダプタ選定。\"\n" +
			"  furrow note e-k3m9p \"方針: v7 の pinned に寄せる。\"\n" +
			"  git log -1 --format=%B | furrow note t-k3m9p -\n" +
			"  furrow note t-k3m9p --prepend \"2026-10-13 判断: B 案で進める（詳細は末尾）\"",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			text, terr := readTextArg(cmd, args[1])
			if terr != nil {
				return terr
			}
			// `changed` tracks metadata fields only, so a note (body + updated)
			// would show changed:[] — surface the effect instead. Both entities
			// carry the same key, so a caller reads one shape either way; the
			// key names the placement, so a reader of the envelope knows where
			// the paragraph went.
			key := "appended"
			if prepend {
				key = "prepended"
			}
			effect := map[string]any{key: strings.TrimRight(text, "\n")}
			epic, rerr := a.RefTargetsEpic(args[0])
			if rerr != nil {
				return rerr
			}
			if epic {
				// The stale-read guard covers the box side of note's dual contract
				// too: an epic's progress record races between sessions exactly
				// like a task's, and both entities carry `updated`.
				expected, guard, gerr := expectUpdatedArg(cmd, args[0])
				if gerr != nil {
					return gerr
				}
				write := a.EpicNote
				if prepend {
					write = a.EpicPrependNote
				}
				before, after, nerr := write(args[0], text)
				if nerr != nil {
					return nerr
				}
				if guard && before != nil {
					effect = mergeExtra(effect, staleReadExtra(before.ID, &before.Updated, expected))
				}
				printEpicMutation(a, "noted", before, after, effect)
				return nil
			}
			write := a.AddNote
			if prepend {
				write = a.PrependNote
			}
			return emitMutationWith(cmd, a, "noted", args[0],
				func() (*core.Task, error) { return write(args[0], text) },
				func(after *core.Task) map[string]any { return effect })
		},
	}
	cmd.Flags().BoolVar(&prepend, "prepend", false, "put the paragraph first (right after the # title line, else at the top) instead of last")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newReorderCmd() *cobra.Command {
	var before, after string
	cmd := &cobra.Command{
		Use:   "reorder <id> [<priority>]",
		Short: "Set a task's priority — absolute, or relative with --before/--after",
		Long: "Order a task within its lane. With an absolute <priority>, set the sparse\n" +
			"integer directly (lower = higher up) — honoured as given, and a value another\n" +
			"task in the lane already holds is disclosed in a stderr note, since the\n" +
			"lane's order is then undefined (`lint` reports it as priority-duplicate).\n" +
			"With --before/--after <id>, compute it:\n" +
			"the task is slotted immediately before/after that task in its lane (both must\n" +
			"share a lane — relative order across lanes is meaningless, so a cross-lane\n" +
			"target is exit 2). When the sparse gap next to the target is exhausted, the\n" +
			"whole lane is respaced in the same single write (all-or-nothing); --json then\n" +
			"adds a `renumbered` array with the neighbors' {id, from, to} moves, and a note\n" +
			"names the count on stderr. The respaced neighbors' `updated` stays put: a\n" +
			"respace is positional bookkeeping, not progress.",
		Example: "  furrow reorder t-k3m9p 90\n" +
			"  furrow reorder t-k3m9p --before t-x1y2z\n" +
			"  furrow reorder t-k3m9p --after t-x1y2z",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id := args[0]
			ref, isBefore := before, true
			if ref == "" {
				ref, isBefore = after, false
			}
			switch {
			case len(args) == 2 && ref != "":
				return core.Validationf(id, "give an absolute <priority> or --before/--after, not both")
			case len(args) == 1 && ref == "":
				return core.Validationf(id, "provide a <priority>, or --before/--after <id>")
			case ref != "":
				var changes []core.PriorityChange
				return emitMutationWith(cmd, a, "reordered", id,
					func() (*core.Task, error) {
						t, ch, err := a.ReorderRelative(id, ref, isBefore)
						changes = ch
						return t, err
					},
					func(t *core.Task) map[string]any { return respaceExtra(changes, t.Status) })
			default:
				prio, err := atoiArg("priority", args[1])
				if err != nil {
					return err
				}
				return emitMutation(cmd, a, "reordered", id, func() (*core.Task, error) { return a.Reorder(id, prio) })
			}
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "place immediately before this task (same lane)")
	cmd.Flags().StringVar(&after, "after", "", "place immediately after this task (same lane)")
	cmd.MarkFlagsMutuallyExclusive("before", "after")
	addExpectUpdatedFlag(cmd)
	return cmd
}

// respaceExtra reports a relative move's lane respace: a stderr note plus the
// envelope's `renumbered` key. Nil when nothing else moved — the key must not
// appear on a plain midpoint insert. Shared by reorder and set so the two
// relative paths can never diverge on the report.
func respaceExtra(changes []core.PriorityChange, lane string) map[string]any {
	if len(changes) == 0 {
		return nil
	}
	fmt.Fprintf(errOut, "note: gap exhausted — respaced %d other task(s) in lane %q\n", len(changes), lane)
	return map[string]any{"renumbered": changes}
}

// newEstimateCmd builds the shared `value`/`effort` setter: `furrow <name> <id>
// <1-5>` records a coarse estimate (clamped into 1..5), `--clear` unsets it.
// value and effort together drive ROI = value/effort for picking the next task.
func newEstimateCmd(name string, set func(*app.App, string, *int) (*core.Task, error), get func(*core.Task) *int) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   name + " <id> [<1-5>]",
		Short: "Set a task's " + name + " estimate (coarse 1..5), or clear it with --clear",
		Long: "Record a coarse 1..5 " + name + " estimate on a task; out-of-range scores are\n" +
			"clamped into 1..5. With --clear, remove the estimate (back to unset, so intake\n" +
			"stays frictionless). value and effort together derive ROI = value/effort, the\n" +
			"signal for picking the next task — order by it with `furrow ls --sort value`\n" +
			"(unset estimates stay last) or select on it with `furrow ls -q 'roi:>=2'`.\n\n" +
			"A clamp is SIGNALED, so an explicit score is never silently rounded: a\n" +
			"stderr note plus `clamped." + name + ".{requested, stored}` in the --json\n" +
			"envelope. Via `add --" + name + "` the clamp is the stderr note alone (add\n" +
			"--json prints the created task, not an envelope).",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id := args[0]
			var v *int
			switch {
			case clear:
				if len(args) != 1 {
					return core.Validationf(id, "--clear takes no score argument")
				}
			default:
				if len(args) != 2 {
					return core.Validationf(id, "provide a 1-5 score, or --clear to unset")
				}
				n, err := atoiArg(name, args[1])
				if err != nil {
					return err
				}
				v = &n
			}
			return emitMutationWith(cmd, a, name, id,
				func() (*core.Task, error) { return set(a, id, v) },
				func(after *core.Task) map[string]any {
					// An out-of-range score is silently clamped to 1..5 on write;
					// signal it (stderr note + a `clamped` envelope key) so an agent
					// that recorded 9 knows it was stored as 5.
					stored := get(after)
					warnClamp(name, v, stored)
					if e := clampEntry(v, stored); e != nil {
						return map[string]any{"clamped": map[string]any{name: e}}
					}
					return nil
				})
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the estimate (back to unset)")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newValueCmd() *cobra.Command {
	return newEstimateCmd("value",
		func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetValue(id, v) },
		func(t *core.Task) *int { return t.Value })
}

func newEffortCmd() *cobra.Command {
	return newEstimateCmd("effort",
		func(a *app.App, id string, v *int) (*core.Task, error) { return a.SetEffort(id, v) },
		func(t *core.Task) *int { return t.Effort })
}

func newCheckCmd() *cobra.Command {
	var (
		adds   []string
		off    bool
		rm     bool
		reword string
	)
	cmd := &cobra.Command{
		Use:   "check <id> [item-index]",
		Short: "Toggle, add, remove, or reword a checklist item",
		Long: "Edit a task's checklist. With no mode flag, mark the item at the given\n" +
			"zero-based index done — an idempotent set, not a toggle (--off unchecks).\n" +
			"The index is the number `show` prints beside each row; an index is a\n" +
			"position in the CURRENT list, so after --rm every item below it moves up by\n" +
			"one — remove from the highest index down. --add appends one or more items\n" +
			"(repeatable, text verbatim). --rm deletes the item at the index. --reword\n" +
			"replaces the text of the item at the index (keeping its done state). The\n" +
			"mode flags are mutually exclusive; an out-of-range index is exit 2.",
		Example: "  furrow check t-k3m9p 0            # mark item 0 done\n" +
			"  furrow check t-k3m9p 0 --off     # uncheck item 0\n" +
			"  furrow check t-k3m9p --add \"write tests\" --add \"update docs\"\n" +
			"  furrow check t-k3m9p 1 --rm      # delete item 1\n" +
			"  furrow check t-k3m9p 1 --reword \"revised step\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			// An explicitly-passed empty value is a validation error, NEVER a silent
			// mode switch: dropping a blank --add (the old behavior) let
			// `check <id> <idx> --add ""` fall through to the toggle path and mark
			// item <idx> done at exit 0 — a value silently switching the command's
			// mode. Same rule as `done --note ""` (exit 2, never a silent plain close).
			if cmd.Flags().Changed("add") {
				for _, s := range adds {
					if strings.TrimSpace(s) == "" {
						return core.Validationf(args[0], "--add needs non-empty text (an empty value is exit 2, never a mode switch)")
					}
				}
			}
			if cmd.Flags().Changed("reword") && strings.TrimSpace(reword) == "" {
				return core.Validationf(args[0], "--reword needs non-empty text")
			}

			// index parses the required zero-based item index for the modes that
			// target an existing item (toggle / --off / --rm / --reword).
			index := func() (int, error) {
				if len(args) != 2 {
					return 0, core.Validationf(args[0], "provide a checklist item index")
				}
				return atoiArg("item-index", args[1])
			}
			verb := "checked"
			var mutate func() (*core.Task, error)
			switch {
			case len(adds) > 0:
				// --add appends; an index beside it was dropped in silence
				// (`check <id> 5 --add x` appended and said nothing about the 5).
				if len(args) == 2 {
					return core.Validationf(args[0], "--add appends a new item and takes no index (an index selects an existing item: toggle, --off, --rm, --reword) — drop the %q", args[1])
				}
				verb = "checklist+"
				mutate = func() (*core.Task, error) { return a.AddChecks(args[0], adds) }
			case rm:
				verb = "checklist-"
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.RemoveCheck(args[0], i)
				}
			case cmd.Flags().Changed("reword"):
				verb = "checklist~"
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.RewordCheck(args[0], i, reword)
				}
			default:
				mutate = func() (*core.Task, error) {
					i, err := index()
					if err != nil {
						return nil, err
					}
					return a.Check(args[0], i, !off)
				}
			}
			return emitMutation(cmd, a, verb, args[0], mutate)
		},
	}
	cmd.Flags().StringArrayVar(&adds, "add", nil, "append a checklist item with this text (repeatable)")
	cmd.Flags().BoolVar(&off, "off", false, "uncheck instead of check")
	cmd.Flags().BoolVar(&rm, "rm", false, "delete the checklist item at the index")
	cmd.Flags().StringVar(&reword, "reword", "", "replace the text of the item at the index")
	cmd.MarkFlagsMutuallyExclusive("add", "rm", "reword", "off")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newDepCmd() *cobra.Command {
	var rm, list bool
	cmd := &cobra.Command{
		Use:   "dep <id> [<dep-id>...]",
		Short: "Add/remove a task's dependencies, or list them both ways with --list",
		Long: "Make <id> depend on each <dep-id> (id waits on them). Several dep-ids in one\n" +
			"call apply in a single write. With --rm, remove those dependencies instead.\n" +
			"Every dep must exist; adding is acyclic and idempotent, and the batch is\n" +
			"all-or-nothing (a bad dep-id aborts without a partial change). The\n" +
			"verb-first spellings `dep add <id> <dep-id>...` and `dep rm <id> <dep-id>...`\n" +
			"are the same two writes (neither word can be an id: ids carry a prefix and\n" +
			"a dash).\n\n" +
			"With --list, don't mutate — read <id>'s dependency neighborhood in BOTH\n" +
			"directions: what it depends_on (its own deps — what it waits on) and what it\n" +
			"blocks (the reverse edge — the tasks waiting on it), each resolved to\n" +
			"id+title+lane plus that task's own blocked_by (its deps not yet done — the\n" +
			"key every ls/show row carries; [] when nothing is in the way). So a blocks\n" +
			"row answers \"what moves if I close this\" by itself: it ends\n" +
			"`← unblocks on this close` when <id> is its last open dep and\n" +
			"`← N other open deps` when not; a parked or closed dependent carries no\n" +
			"note (nothing moves it), and once <id> is done the same rows read\n" +
			"`← unblocked` / `← N open deps`. --json/--ndjson emit one object with\n" +
			"both arrays. The reverse edge is the local, no-server twin of \"what\n" +
			"unblocks if I finish this\". A dangling dep resolves to its id alone with\n" +
			"blocked_by [] (lint's dep-missing flags it). Board-wide, the reverse\n" +
			"lookup is `ls -q depends-on:<id>`. An archived id is not on the live board:\n" +
			"--list exits 1 and names `show <id> --archived`, the read of a retired\n" +
			"task's edges.",
		Example: "  furrow dep t-k3m9p t-a1b2c\n" +
			"  furrow dep t-k3m9p t-a1b2c t-d4e5f    # depend on both in one write\n" +
			"  furrow dep t-k3m9p t-a1b2c --rm\n" +
			"  furrow dep add t-k3m9p t-a1b2c        # the same two writes, verb first\n" +
			"  furrow dep rm t-k3m9p t-a1b2c\n" +
			"  furrow dep t-k3m9p --list --json      # what it waits on and what it blocks",
		Args: depVerbArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if list {
				if f := cmd.Flags().Lookup(expectUpdatedFlag); f != nil && f.Changed {
					return core.Validationf(args[0], "--expect-updated guards a write; --list reads and takes none")
				}
				res, err := a.DepList(args[0])
				if err != nil {
					return err
				}
				return emitDepList(res, a.Cfg.Terminal, a.Cfg.DoneLane)
			}
			spelled, args := depVerb(args)
			id, deps := args[0], args[1:]
			verb := "dep+"
			mutate := func() (*core.Task, error) { return a.AddDeps(id, deps) }
			if rm || spelled == "rm" {
				verb = "dep-"
				mutate = func() (*core.Task, error) { return a.RemoveDeps(id, deps) }
			}
			return emitMutation(cmd, a, verb, id, mutate)
		},
	}
	cmd.Flags().BoolVar(&rm, "rm", false, "remove the dependencies instead of adding them")
	cmd.Flags().BoolVar(&list, "list", false, "read-only: list what <id> depends on and what depends on it (both directions)")
	cmd.MarkFlagsMutuallyExclusive("list", "rm")
	addExpectUpdatedFlag(cmd)
	return cmd
}

// shiftPreviewTag is the preview row's answer to "where does --due-shift land
// this one": the due it would carry after the write, in the board's calendar
// like dueTag beside it, so the row reads as one before → after pair. A row
// the apply would refuse says so instead — no due to shift, or a landing
// outside the years a due can hold (the spelling itself was vetted before the
// preview, so that is the one error ShiftDue can still return here) — because
// the preview must not promise a write the apply will not make. Every refusal
// starts without the arrow; previewTail.refused reads that.
func shiftPreviewTag(a *app.App, t *core.Task, spelling string) string {
	if t.Due == nil {
		return "no due — refused"
	}
	shifted, err := app.ShiftDue(*t.Due, spelling, a.Calendar())
	if err != nil {
		return "lands outside years 1..9999 — refused"
	}
	return "→ " + shifted.In(a.Calendar()).Format("2006-01-02 15:04")
}

// shiftPreviewMove is shiftPreviewTag's JSON twin: the due before and after
// the shift as RFC 3339 instants, for a row the shift can move.
func shiftPreviewMove(a *app.App, t *core.Task, spelling string) (from, to string, ok bool) {
	if t.Due == nil {
		return "", "", false
	}
	shifted, err := app.ShiftDue(*t.Due, spelling, a.Calendar())
	if err != nil {
		return "", "", false
	}
	return t.Due.UTC().Format(time.RFC3339), shifted.UTC().Format(time.RFC3339), true
}

// anchorPreviewTag is the preview row's verdict on `set -q --anchor`: "" for a
// row the apply would mark, else why it would be refused — no due (unless the
// same write promises one) or a repeat rule (unless the same write drops it).
// previewTail.refused reads the non-empty case. The rule case is worded so it
// does not repeat the row's own `repeats` tag beside it.
func anchorPreviewTag(t *core.Task, o app.SetOpts) string {
	switch {
	case t.Due == nil && o.Due == nil:
		return "no due — refused"
	case t.Repeat != "" && !o.ClearRepeat:
		return "follows its own rule — refused"
	}
	return ""
}

// clearDuePreviewTag is the verdict on `set -q --clear-due`: a repeating row
// keeps its due unless the same write drops the rule.
func clearDuePreviewTag(t *core.Task, o app.SetOpts) string {
	if t.Repeat != "" && !o.ClearRepeat {
		return "keeps the due for its rule — refused"
	}
	return ""
}

// repeatPreviewTag is the verdict on `set -q --repeat`: a rule counts from a
// due (unless the same write promises one) and cannot sit on a follower
// (unless the same write drops the pointer).
func repeatPreviewTag(t *core.Task, o app.SetOpts) string {
	switch {
	case t.Due == nil && o.Due == nil:
		return "no due — refused"
	case t.Anchor != "" && !o.ClearAnchor:
		return "follows a box — refused"
	}
	return ""
}

// emptyFlagErr names a flag that WAS passed but carried nothing pflag kept — a
// bare `--add ""` on a StringSlice, or `--before ""`. Without it the command
// falls through to its "provide at least one …" guard and tells the caller to
// pass a flag they just passed, which reads as a bug in furrow rather than in
// the invocation. The exit code was already 2; this only stops the message
// from being wrong. (A NON-empty blank like `--add " "` is caught deeper, in
// the app's requireNonBlank, which is where the whole rule lives.)
func emptyFlagErr(cmd *cobra.Command, id string, names ...string) error {
	remedies := make([]flagRemedy, 0, len(names))
	for _, name := range names {
		remedies = append(remedies, flagRemedy{name, "pass a real one or drop the flag"})
	}
	return emptyFlagRemedyErr(cmd, id, remedies...)
}

// flagRemedy pairs a flag with what an empty value should have been instead —
// the half of the refusal that differs per flag (`--clear-due` is the clear,
// `+7d` is an offset), where emptyFlagErr's generic remedy would misdirect.
type flagRemedy struct {
	name, remedy string
}

// emptyFlagRemedyErr is emptyFlagErr with a remedy per flag, checked in the
// order given so the first offending flag is the one named. Empty is "passed
// with nothing in it": no members for a slice flag, only whitespace otherwise.
func emptyFlagRemedyErr(cmd *cobra.Command, id string, remedies ...flagRemedy) error {
	for _, r := range remedies {
		f := cmd.Flags().Lookup(r.name)
		if f == nil || !f.Changed {
			continue
		}
		empty := false
		switch v := f.Value.(type) {
		case interface{ GetSlice() []string }:
			empty = len(v.GetSlice()) == 0
		default:
			empty = strings.TrimSpace(f.Value.String()) == ""
		}
		if empty {
			return core.Validationf(id, "--%s was given an empty value; %s", r.name, r.remedy)
		}
	}
	return nil
}

func newLabelCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "label <id>",
		Short: "Add and/or remove labels on a task",
		Long: "Add labels with --add and remove them with --rm (both repeatable and\n" +
			"combinable in one call). Adding a label already present, or removing one\n" +
			"already absent, is a no-op. Provide at least one --add or --rm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "labeled", args[0], func() (*core.Task, error) {
				return a.Relabel(args[0], add, remove)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "label to add (repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&remove, "rm", nil, "label to remove (repeatable; comma-separated)")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newRefCmd() *cobra.Command {
	var add, rm []string
	cmd := &cobra.Command{
		Use:   "ref <id>",
		Short: "Add and/or remove refs (file:line or URL) on a task",
		Long: "Edit a task's refs after creation — the counterpart to `add --ref`. Add\n" +
			"refs with --add and remove them with --rm (both repeatable and combinable\n" +
			"in one call). Adding a ref already present, or removing one already absent,\n" +
			"is a no-op. Refs keep the order you gave them (they are a sequence, not a\n" +
			"sorted set like labels): --add appends at the end.",
		Example: "  furrow ref t-k3m9p --add internal/cli/root.go:42\n" +
			"  furrow ref t-k3m9p --add https://example.com/spec --rm docs/old.md:10",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "ref", args[0], func() (*core.Task, error) {
				return a.Reref(args[0], add, rm)
			})
		},
	}
	// StringArray, not StringSlice: a ref is free text (a URL keeps its query
	// commas, a path may carry a quote), so each value is stored verbatim.
	cmd.Flags().StringArrayVar(&add, "add", nil, "ref to add (file:line or URL; verbatim, never checked for existence; repeatable)")
	cmd.Flags().StringArrayVar(&rm, "rm", nil, "ref to remove (exact match; verbatim; repeatable)")
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newRetitleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retitle <id> <title...>",
		Short: "Rename a task (updates the shard title and the body heading)",
		Long: "Set a task's one-line title. The title lives in two places — the task\n" +
			"shard's title field and the body's leading `# ` heading — and retitle\n" +
			"updates both so they never drift (the shard is the source of truth; a body\n" +
			"with no leading heading is left untouched). The remaining args are joined\n" +
			"with spaces, so the title need not be quoted:\n\n" +
			"  furrow retitle t-k3m9p a clearer, shorter title",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id, title := args[0], strings.Join(args[1:], " ")
			return emitMutation(cmd, a, "retitled", id, func() (*core.Task, error) { return a.Retitle(id, title) })
		},
	}
	addExpectUpdatedFlag(cmd)
	return cmd
}

func newRepoCmd() *cobra.Command {
	var add, rm []string
	cmd := &cobra.Command{
		Use:   "repo <id>",
		Short: "Attach and/or detach repos (owner/repo) on a task",
		Long: "Attach repos with --add and detach them with --rm (both repeatable and\n" +
			"combinable in one call). Each value must be a full owner/repo, or a short\n" +
			"name matching exactly one repo already known to the board (case-insensitive,\n" +
			"at a '/' boundary); anything else is a validation error — never a silent new\n" +
			"repo. Attaching a repo already present, or detaching one already absent, is\n" +
			"a no-op. A task with no repos is a draft (see ls --drafts). The validation\n" +
			"error is exit 2 with the matching or known repos in candidates.",
		Example: "  furrow repo t-k3m9p --add akira-toriyama/furrow\n" +
			"  furrow repo t-k3m9p --rm furrow                # detach by short name\n" +
			"  furrow repo t-k3m9p --add cifail --rm furrow   # move across repos",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			if err := emptyFlagErr(cmd, args[0], "add", "rm"); err != nil {
				return err
			}
			return emitMutation(cmd, a, "repo", args[0], func() (*core.Task, error) {
				return a.Rerepo(args[0], add, rm)
			})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "repo to attach (owner/repo, or a unique short name; repeatable; comma-separated)")
	cmd.Flags().StringSliceVar(&rm, "rm", nil, "repo to detach (same forms; repeatable; comma-separated)")
	addExpectUpdatedFlag(cmd)
	return cmd
}

// seriesReports carries the per-task repeat facts a write produced — the
// reports a close made, and the bind-time notes of a rule it bound — from the
// mutate closure out to where they render: each task's JSON envelope
// (annotate), the human summary (print) and the stderr notes (noteBound).
type seriesReports struct {
	byID  map[string]*app.RepeatReport
	warns map[string][]string
}

func newSeriesReports() *seriesReports {
	return &seriesReports{byID: map[string]*app.RepeatReport{}, warns: map[string][]string{}}
}

// bind records what a rule this write just bound will do that was almost
// certainly not asked for (a skipped month, an anchor off the rule's lattice),
// keyed by the RETURNED task's id. Call it after collect, and only on an actual
// bind: re-printing it on every unrelated edit of a repeating task taught the
// reader to ignore it.
func (s *seriesReports) bind(a *app.App, tasks []*core.Task) {
	for _, t := range tasks {
		subject := t
		// A write that BINDS a rule and CLOSES in one go hands the rule straight
		// to the occurrence, so the returned task carries none — follow it, or the
		// note is silent exactly when a rule was just bound. The occurrence has no
		// envelope of its own in this write's output, so its notes ride the
		// closed task's, beside the `repeat` receipt that names it.
		if subject.Repeat == "" {
			if r := s.byID[t.ID]; r != nil && r.Created != nil {
				if succ, _, err := a.Get(*r.Created); err == nil {
					subject = succ
				}
			}
		}
		if ws := a.RepeatWarnings(subject); len(ws) > 0 {
			s.warns[t.ID] = ws
		}
	}
}

// noteBound says each bound rule's notes on stderr, once per distinct note.
func (s *seriesReports) noteBound(tasks []*core.Task) {
	lists := make([][]string, 0, len(tasks))
	for _, t := range tasks {
		lists = append(lists, s.warns[t.ID])
	}
	noteOnce(lists...)
}

// collect zips the reports onto the tasks the write returned. app returns them
// positionally, so a length mismatch means a contract change, not a data
// condition — it is simply ignored rather than guessed at.
func (s *seriesReports) collect(tasks []*core.Task, reps []*app.RepeatReport) {
	if len(tasks) != len(reps) {
		return
	}
	for i, t := range tasks {
		if reps[i] != nil {
			s.byID[t.ID] = reps[i]
		}
	}
}

func (s *seriesReports) annotate(t *core.Task) map[string]any {
	var m map[string]any
	if r := s.byID[t.ID]; r != nil {
		m = mergeExtra(m, map[string]any{"repeat": r})
	}
	if ws := s.warns[t.ID]; len(ws) > 0 {
		m = mergeExtra(m, map[string]any{"repeat_warnings": ws})
	}
	return m
}

// print writes one line per advanced series, after the mutation summary. Each
// line is prefixed with the CLOSED task's id, in apply's `%s  %s` shape: a
// batch prints one verb line per task but only as many receipts as there were
// series, so position maps a receipt to the wrong task in any mixed batch, and
// two spent series render byte-identical lines. The prefix is unconditional —
// the same rule the batch envelopes follow, that the runtime argv length must
// not fork the output shape. The JSON path already carries the same facts on
// each envelope.
func (s *seriesReports) print(a *app.App, tasks []*core.Task) {
	if jsonMode() {
		return
	}
	for _, t := range tasks {
		r := s.byID[t.ID]
		if r == nil {
			continue
		}
		fmt.Fprintf(out, "%s  %s\n", t.ID, seriesLine(a, r))
	}
}

// seriesLine is the one human line a close's series report renders — shared
// with `apply`, so the two closes that mint an occurrence say the same thing.
// A spent series still names what lapsed: a late close is exactly what runs a
// bounded one out.
func seriesLine(a *app.App, r *app.RepeatReport) string {
	line := "repeat: series complete — no further occurrences"
	if !r.Completed {
		line = fmt.Sprintf("repeat: next due %s (%s)", calendarTime(a, *r.Due), *r.Created)
	}
	if r.Skipped > 0 {
		line += fmt.Sprintf(" — %d occurrence(s) skipped", r.Skipped)
	}
	return line
}
