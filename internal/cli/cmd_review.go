package cli

import (
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newReviewCmd wires `furrow review <repo|id>` — the non-interactive review
// stamp. The single argument is dispatched by shape: an id-shaped token
// (matching the configured id pattern, e.g. t-k3m9p) stamps that TASK's
// `reviewed` timestamp; anything else (a full owner/repo or a unique short name)
// records a per-REPO review. Repo mode honors --by: the default `human` advances
// the staleness-nudge clock, while `agent` logs a sweep without advancing it
// (so an autonomous re-evaluation never lets furrow stop nudging a human).
func newReviewCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "review <repo|id>",
		Short: "Record a review: stamp a task's reviewed time, or a repo's last-reviewed clock",
		Long: "Record a review without any interactive prompt (the interactive inbox is a\n" +
			"separate, later mode). The single argument is dispatched by shape:\n\n" +
			"  • an id-shaped token (e.g. t-k3m9p) stamps that TASK's `reviewed` timestamp,\n" +
			"    tracked separately from `updated` (a review changes no content).\n" +
			"  • anything else — a full owner/repo, or a short name matching exactly one\n" +
			"    repo known to the board — records a per-REPO review (the \"when did I last\n" +
			"    triage this repo's backlog\" clock the sync staleness nudge reads).\n\n" +
			"--by selects the actor of a REPO review: the default `human` advances the\n" +
			"nudge clock (last_reviewed); `agent` logs a sweep (last_agent_reviewed) WITHOUT\n" +
			"advancing it, so an autonomous re-evaluation is recorded but a human is still\n" +
			"nudged to look. For a task the flag has no effect (a task has one review clock).",
		Example: "  furrow review t-k3m9p                 # stamp a task reviewed\n" +
			"  furrow review akira-toriyama/furrow   # record a human repo review\n" +
			"  furrow review furrow                  # same, by unique short name\n" +
			"  furrow review furrow --by agent       # log an agent sweep (human clock unchanged)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch by {
			case "human", "agent":
			default:
				return core.Validationf("", "--by must be \"human\" or \"agent\" (got %q)", by)
			}
			a, err := openApp()
			if err != nil {
				return err
			}
			arg := args[0]
			// Dispatch task-vs-repo. Shape alone is NOT enough: the id pattern is
			// just the configured prefix + base32 (^t-[0-9a-z]+$), so a repo short
			// name that happens to start with it — t-digest, t-rex, t-io — is
			// id-shaped too. So an id-shaped token only takes task mode when a task
			// with that id actually EXISTS; otherwise it falls through to repo mode,
			// and if it is not a resolvable repo either we report the more useful
			// error (task not found, since it looked like an id).
			if a.Cfg.IDPattern().MatchString(arg) {
				if _, _, err := a.Get(arg); err == nil {
					return emitMutation(a, "reviewed", arg, func() (*core.Task, error) { return a.ReviewTask(arg) })
				}
				if _, rerr := a.ResolveRepo(arg); rerr != nil {
					return core.NotFound(arg)
				}
			}
			rec, err := a.ReviewRepo(arg, by == "agent")
			if err != nil {
				return err
			}
			emitRepoReview(rec, by == "agent")
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "human", "actor of a repo review: human (advances the nudge clock) or agent (logs a sweep without advancing it)")
	return cmd
}

// emitRepoReview reports a per-repo review. In machine mode it prints the whole
// record ({repo, last_reviewed, last_agent_reviewed}) so an agent sees both
// clocks; in human mode it prints which clock advanced (and, for an agent sweep,
// that the human clock was deliberately left alone).
func emitRepoReview(rec *core.RepoRecord, byAgent bool) {
	if jsonMode() {
		emitObject(rec)
		return
	}
	if byAgent {
		fmt.Fprintf(out, "reviewed %s (agent sweep: %s; human clock unchanged)\n", rec.Repo, fmtReviewTime(rec.LastAgentReviewed))
		return
	}
	fmt.Fprintf(out, "reviewed %s (human review: %s)\n", rec.Repo, fmtReviewTime(rec.LastReviewed))
}

// fmtReviewTime renders a nullable review timestamp for the human line.
func fmtReviewTime(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Format(time.RFC3339)
}
