package cli

import (
	"fmt"
	"io"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	var (
		bodyFile string
		ref      string
		on       string
		openLane string
		dryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "apply --on <open|merge> [--ref <src>] [--body-file <path>] [--dry-run]",
		Short: "Apply SetStatus-task directives parsed from PR/commit text",
		Long: "Parse `SetStatus-task: <body-link> [<lane>]` directives from a blob of text\n" +
			"(a PR body or commit message, via --body-file or stdin) and apply them to the\n" +
			"store. CI/VCS-agnostic: any GitHub specifics stay in the calling workflow.\n\n" +
			"A directive's lane is the MERGE TARGET: where the task should be once the PR\n" +
			"lands. Omit it to only annotate the body.\n\n" +
			"  --on merge  moves the task to the target, except that a task in a terminal\n" +
			"              lane is not reopened: a non-terminal target leaves it where it\n" +
			"              is. A terminal target still applies, so a `done` footer closes\n" +
			"              a task parked (waiting, icebox) while the PR was open.\n" +
			"  --on open   nudges the task to --open-lane (default in-progress) only when\n" +
			"              the target says the PR is the task's work: the done lane, or a\n" +
			"              non-terminal lane at or after --open-lane in [lanes].order. Any\n" +
			"              other target (a lane before --open-lane, or a parked lane) is a\n" +
			"              PR that is not the task's work, and the task stays. A terminal\n" +
			"              task is never nudged, and the target itself never applies at\n" +
			"              open, so `done` closes a task only at merge.\n\n" +
			"A task that stays is annotated like any other (given --ref), and its outcome\n" +
			"names why in `kept` (task-terminal | target-before-open-lane | target-parked),\n" +
			"with a `note:` line on stderr outside --json. A kept directive is an expected\n" +
			"state, not a failure: exit 0.\n\n" +
			"Validation is non-blocking: an unknown id or lane is reported per-directive (and\n" +
			"sets a non-zero exit) while the valid directives still apply. --json prints the\n" +
			"full per-directive report to stdout.\n\n" +
			"--dry-run runs the same parse and validation and reports what each directive\n" +
			"WOULD do (`would move → <lane>`, `would be kept in <lane>`, `would annotate`,\n" +
			"`no change`, `error`) without writing anything — the local gate before\n" +
			"`gh pr create`, where the CI apply is non-blocking and a bad footer would\n" +
			"otherwise surface only after the merge left the lane untouched. Same exit code\n" +
			"as a real apply; the JSON report is the same shape plus `dry_run: true`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := app.ApplyMode(on)
			if mode != app.OnOpen && mode != app.OnMerge {
				return core.Validationf("", "--on must be open|merge, got %q", on)
			}
			text, err := readBodyText(cmd, bodyFile)
			if err != nil {
				return err
			}
			a, err := openApp()
			if err != nil {
				return err
			}
			var res app.ApplyResult
			if dryRun {
				res, err = a.PreviewDirectives(text, ref, mode, openLane)
			} else {
				res, err = a.ApplyDirectives(text, ref, mode, openLane)
			}
			if err != nil {
				return err
			}
			if openLane == "" {
				openLane = app.DefaultOpenLane
			}
			printApplyResult(a, res, openLane)
			if code := res.WorstCode(); code != int(core.CodeOK) {
				// Non-blocking by contract: stdout already carries the full report;
				// this exit lets the workflow comment + fail the (non-required) job.
				return &core.Error{Code: core.Code(code), Kind: core.KindForCode(core.Code(code)), Msg: applyErrorSummary(res)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file holding the PR/commit text (default, or '-': read stdin)")
	cmd.Flags().StringVar(&ref, "ref", "", "source reference recorded in the task body, e.g. furrow#42")
	cmd.Flags().StringVar(&on, "on", "", "event being applied: open|merge (required)")
	cmd.Flags().StringVar(&openLane, "open-lane", app.DefaultOpenLane, "lane a task is nudged to on --on open")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and report what each directive would do; write nothing")
	_ = cmd.MarkFlagRequired("on")
	return cmd
}

// readBodyText returns the directive text from --body-file, or stdin when the
// flag is empty or "-" (the `-`=stdin convention every --body-file shares).
func readBodyText(cmd *cobra.Command, path string) (string, error) {
	if path != "" && path != "-" {
		// The same reader add/edit use: a path that does not resolve is the
		// operator's input, exit 2, not an internal failure (t-xypx).
		return readBodyFile(path)
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", core.Internalf("", "read stdin: %v", err)
	}
	return string(b), nil
}

// printApplyResult renders the apply report: the whole ApplyResult object in
// machine mode (indented under --json, one compact line under --ndjson), else
// one human line per directive, plus a stderr note for each kept one.
func printApplyResult(a *app.App, res app.ApplyResult, openLane string) {
	if jsonMode() {
		emitObject(res)
		return
	}
	if len(res.Outcomes) == 0 {
		fmt.Fprintln(out, "no SetStatus-task directives found")
		return
	}
	moved, annotated, kept := "moved", "annotated", "kept in"
	if res.DryRun {
		moved, annotated, kept = "would move", "would annotate", "would be kept in"
	}
	for _, o := range res.Outcomes {
		// Before the action switch: a kept directive's action is "annotated" or
		// "skipped", and rendering it as such read exactly like a lane-less
		// footer — the author never learned their lane was not applied.
		if o.Kept != "" {
			line := kept + " " + o.To
			if o.Action == "annotated" {
				line += ", " + annotated
			}
			fmt.Fprintf(out, "%s  %s\n", o.ID, line)
			stderrNote(keptNote(app.ApplyMode(res.On), o, openLane))
			continue
		}
		switch o.Action {
		case "moved":
			fmt.Fprintf(out, "%s  %s → %s\n", o.ID, moved, o.To)
			// A close can CREATE the next occurrence. Saying so only in --json
			// left the human arm reporting a lane move while a task appeared out
			// of nowhere in someone's next sync.
			if o.WillRepeat {
				fmt.Fprintf(out, "%s  would also create the next occurrence\n", o.ID)
			}
			// The other outcome of the same close, said for the reason the real
			// path says it: an operator who reads only "would move → done" cannot
			// tell a chore that carries on from one that stops here.
			if o.WillComplete {
				fmt.Fprintf(out, "%s  would complete the series — no further occurrences\n", o.ID)
			}
			if r := o.Repeat; r != nil && (r.Completed || (r.Created != nil && r.Due != nil)) {
				fmt.Fprintf(out, "%s  %s\n", o.ID, seriesLine(a, r))
			}
		case "annotated":
			fmt.Fprintf(out, "%s  %s\n", o.ID, annotated)
		case "error":
			fmt.Fprintf(out, "%s  error: %s\n", o.ID, o.Error)
		default: // skipped
			fmt.Fprintf(out, "%s  no change\n", o.ID)
		}
	}
}

// keptNote says on stderr why a kept directive's task stayed where it is.
func keptNote(mode app.ApplyMode, o app.ApplyOutcome, openLane string) string {
	switch o.Kept {
	case app.KeptTaskTerminal:
		if mode == app.OnOpen {
			return fmt.Sprintf("%s stays in %s: opening a PR does not reopen a task in a terminal lane", o.ID, o.To)
		}
		return fmt.Sprintf("%s stays in %s: a merge does not reopen a task in a terminal lane, so the footer's %s is not applied (`furrow move %s %s` if the work really reopens)", o.ID, o.To, o.Lane, o.ID, o.Lane)
	case app.KeptTargetBeforeOpenLane:
		return fmt.Sprintf("%s stays in %s: the footer's %s comes before --open-lane %s in [lanes].order, so this PR is not the task's work; the lane applies at merge", o.ID, o.To, o.Lane, openLane)
	case app.KeptTargetParked:
		return fmt.Sprintf("%s stays in %s: the footer's %s is a parked lane, so this PR is not the task's work; the lane applies at merge", o.ID, o.To, o.Lane)
	}
	return fmt.Sprintf("%s stays in %s (%s)", o.ID, o.To, o.Kept)
}

// applyErrorSummary is the stderr message when one or more directives failed.
func applyErrorSummary(res app.ApplyResult) string {
	var n int
	for _, o := range res.Outcomes {
		if o.Action == "error" {
			n++
		}
	}
	return fmt.Sprintf("%d of %d SetStatus-task directive(s) failed", n, len(res.Outcomes))
}
