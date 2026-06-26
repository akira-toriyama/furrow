package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	var (
		bodyFile string
		ref      string
		on       string
		openLane string
	)
	cmd := &cobra.Command{
		Use:   "apply --on <open|merge> [--ref <src>] [--body-file <path>]",
		Short: "Apply SetStatus-task directives parsed from PR/commit text",
		Long: "Parse `SetStatus-task: <body-link> [<lane>]` directives from a blob of text\n" +
			"(a PR body or commit message, via --body-file or stdin) and apply them to the\n" +
			"store. CI/VCS-agnostic: any GitHub specifics stay in the calling workflow.\n\n" +
			"With --on merge, each directive moves its task to the named lane (omit the lane\n" +
			"to only annotate the body). With --on open, a task whose directive names a lane\n" +
			"is nudged to --open-lane (default in-progress) unless it is already terminal —\n" +
			"the directive's own lane is the merge target, so a `done` directive only closes\n" +
			"the task at merge.\n\n" +
			"Validation is non-blocking: an unknown id or lane is reported per-directive (and\n" +
			"sets a non-zero exit) while the valid directives still apply. --json prints the\n" +
			"full per-directive report to stdout.",
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
			res, err := a.ApplyDirectives(text, ref, mode, openLane)
			if err != nil {
				return err
			}
			printApplyResult(res)
			if code := res.WorstCode(); code != int(core.CodeOK) {
				// Non-blocking by contract: stdout already carries the full report;
				// this exit lets the workflow comment + fail the (non-required) job.
				return &core.Error{Code: core.Code(code), Msg: applyErrorSummary(res)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file holding the PR/commit text (default: read stdin)")
	cmd.Flags().StringVar(&ref, "ref", "", "source reference recorded in the task body, e.g. furrow#42")
	cmd.Flags().StringVar(&on, "on", "", "event being applied: open|merge (required)")
	cmd.Flags().StringVar(&openLane, "open-lane", app.DefaultOpenLane, "lane a task is nudged to on --on open")
	_ = cmd.MarkFlagRequired("on")
	return cmd
}

// readBodyText returns the directive text from --body-file, or stdin when the
// flag is empty.
func readBodyText(cmd *cobra.Command, path string) (string, error) {
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", core.Internalf("", "read --body-file %q: %v", path, err)
		}
		return string(b), nil
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", core.Internalf("", "read stdin: %v", err)
	}
	return string(b), nil
}

// printApplyResult renders the apply report: the full JSON object in --json mode,
// else one human line per directive.
func printApplyResult(res app.ApplyResult) {
	if flagJSON {
		printJSON(res)
		return
	}
	if len(res.Outcomes) == 0 {
		fmt.Fprintln(out, "no SetStatus-task directives found")
		return
	}
	for _, o := range res.Outcomes {
		switch o.Action {
		case "moved":
			fmt.Fprintf(out, "%s  moved → %s\n", o.ID, o.To)
		case "annotated":
			fmt.Fprintf(out, "%s  annotated\n", o.ID)
		case "error":
			fmt.Fprintf(out, "%s  error: %s\n", o.ID, o.Error)
		default: // skipped
			fmt.Fprintf(out, "%s  no change\n", o.ID)
		}
	}
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
