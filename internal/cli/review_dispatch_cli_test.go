package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// The epic arm must not swallow the incumbent repo errors: an AMBIGUOUS short
// name keeps its exit 2 + candidates even when an epic's title would resolve
// the same token — only an UNRESOLVABLE repo falls through to the epic
// contract. (Pre-fix, any ReviewRepo failure routed to the epic: `review
// tools` on a two-repo board stamped a box and exited 0.)
func TestCLIReviewAmbiguousRepoBeatsEpicTitle(t *testing.T) {
	initStore(t)
	a1 := addTask(t, "one")
	b1 := addTask(t, "two")
	if _, code := run(t, "repo", a1, "--add", "acme/tools"); code != 0 {
		t.Fatal("attach acme/tools")
	}
	if _, code := run(t, "repo", b1, "--add", "other/tools"); code != 0 {
		t.Fatal("attach other/tools")
	}
	if _, code := run(t, "epic", "add", "tools migration"); code != 0 {
		t.Fatal("epic add")
	}

	err, _ := runErr(t, "review", "tools")
	if err == nil || err.Kind != core.KindRepoAmbiguous {
		t.Fatalf("ambiguous short name must keep the repo error, got %+v", err)
	}
	if len(err.Candidates) != 2 {
		t.Errorf("candidates = %v, want both repos", err.Candidates)
	}
}

// Every epic key the revisit summary COUNTS must render on the human line:
// Empty() gates the line, so a counted-but-unrendered key prints a nudge that
// names nothing (measured with epic_review_due pre-fix).
func TestRevisitLineRendersEveryEpicKey(t *testing.T) {
	sum := app.RevisitSummary{
		EpicAllDone:   []string{"e-a"},
		EpicStuck:     []string{"e-b"},
		EpicStale:     []string{"e-c"},
		EpicDepDone:   []string{"e-d"},
		EpicReviewDue: []string{"e-e"},
	}
	line := revisitLine(sum, "board")
	for _, want := range []string{"epic_all_done", "epic_stuck", "epic_stale", "epic_dep_done", "epic_review_due"} {
		if !strings.Contains(line, want) {
			t.Errorf("revisit line %q must name %s", line, want)
		}
	}
}
