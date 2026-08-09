package cli

import (
	"fmt"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

// newReviewCmd wires `furrow review <repo|id|epic>` — the non-interactive
// review stamp. The single argument is dispatched by shape then existence: an
// id-shaped token naming a real task stamps that TASK's `reviewed` timestamp;
// else the incumbent REPO resolution runs (a full owner/repo or a unique short
// name); else the ref is tried under the epic-ref contract and stamps that
// BOX's `reviewed` (v9) — the reset for the standing box's epic_review_due
// cadence. Repo mode honors --by: the default `human` advances the
// staleness-nudge clock, while `agent` logs a sweep without advancing it
// (so an autonomous re-evaluation never lets furrow stop nudging a human).
func newReviewCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "review <repo|id|epic>",
		Short: "Record a review: stamp a task's or epic's reviewed time, or a repo's last-reviewed clock",
		Long: "Record a review without any interactive prompt (the interactive inbox is a\n" +
			"separate, later mode). The single argument is dispatched by shape, then\n" +
			"existence:\n\n" +
			"  • an id-shaped token naming a real task (e.g. t-k3m9p) stamps that TASK's\n" +
			"    `reviewed` timestamp, tracked separately from `updated` (a review changes\n" +
			"    no content).\n" +
			"  • else, a full owner/repo or a short name matching exactly one repo known\n" +
			"    to the board records a per-REPO review (the \"when did I last triage this\n" +
			"    repo's backlog\" clock the sync staleness nudge reads).\n" +
			"  • else the ref is tried under the epic-ref contract (exact id, unique id\n" +
			"    prefix, unique title substring) and stamps that BOX's `reviewed` — the\n" +
			"    reset for revisit's epic_review_due cadence on a STANDING box, which\n" +
			"    starts at the box's first review (never-reviewed boxes stay quiet).\n\n" +
			"--by selects the actor of a REPO review: the default `human` advances the\n" +
			"nudge clock (last_reviewed); `agent` logs a sweep (last_agent_reviewed) WITHOUT\n" +
			"advancing it, so an autonomous re-evaluation is recorded but a human is still\n" +
			"nudged to look. For a task or an epic the flag has no effect (they carry one\n" +
			"review clock).",
		Example: "  furrow review t-k3m9p                 # stamp a task reviewed\n" +
			"  furrow review akira-toriyama/furrow   # record a human repo review\n" +
			"  furrow review furrow                  # same, by unique short name\n" +
			"  furrow review furrow --by agent       # log an agent sweep (human clock unchanged)\n" +
			"  furrow review e-k3m9                  # stamp a standing box reviewed",
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
			// Dispatch task-vs-repo-vs-epic. Shape alone is NOT enough: the id
			// pattern is just the configured prefix + base32 (^t-[0-9a-z]+$), so a
			// repo short name that happens to start with it — t-digest, t-rex,
			// t-io — is id-shaped too. So an id-shaped token only takes task mode
			// when a task with that id actually EXISTS; otherwise it falls through
			// to repo mode. The EPIC contract is tried last, after the incumbent
			// repo resolution fails, so no ref that resolved before v9 changes
			// meaning — and the error side follows the note command's routing rule:
			// RefTargetsEpic answers true for an epic-shaped miss, handing it to
			// ReviewEpic whose resolver exits 2 with candidates, while everything
			// else keeps the repo/task error it always had.
			reviewEpic := func() error {
				before, after, err := a.ReviewEpic(arg)
				if err != nil {
					return err
				}
				printEpicMutation("reviewed", before, after, nil)
				return nil
			}
			if a.Cfg.IDPattern().MatchString(arg) {
				if _, _, err := a.Get(arg); err == nil {
					return emitMutation(cmd, a, "reviewed", arg, func() (*core.Task, error) { return a.ReviewTask(arg) })
				}
				if _, rerr := a.ResolveRepo(arg); rerr != nil {
					// A task-id-shaped miss can still be a box on a board whose
					// epic_prefix extends the task prefix (the note precedent).
					if ok, eerr := a.RefTargetsEpic(arg); eerr == nil && ok {
						return reviewEpic()
					}
					return core.NotFound(arg)
				}
			}
			rec, err := a.ReviewRepo(arg, by == "agent")
			if err != nil {
				if ok, eerr := a.RefTargetsEpic(arg); eerr == nil && ok {
					return reviewEpic()
				}
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

// fmtReviewTime renders a nullable review timestamp for the human line, in the
// viewer's local TZ + offset to match `show` (humanTime); the --json review
// view stays UTC RFC3339.
func fmtReviewTime(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return humanTime(*t)
}
