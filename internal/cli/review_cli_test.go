package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// unreviewedLine names at most three repos (with day counts) and reports the
// exact total, so a board with many stale repos stays legible.
func TestUnreviewedLine(t *testing.T) {
	one := unreviewedLine([]app.UnreviewedRepo{{Repo: "o/a", Days: 21}})
	if !strings.Contains(one, "1 repo(s) unreviewed") || !strings.Contains(one, "o/a (21d)") || strings.Contains(one, "more") {
		t.Errorf("single repo line wrong: %q", one)
	}
	many := unreviewedLine([]app.UnreviewedRepo{
		{Repo: "o/a", Days: 1}, {Repo: "o/b", Days: 2}, {Repo: "o/c", Days: 3}, {Repo: "o/d", Days: 4},
	})
	if !strings.Contains(many, "4 repo(s) unreviewed") || !strings.Contains(many, "+1 more") {
		t.Errorf("many-repo line should cap names + report total: %q", many)
	}
	if strings.Contains(many, "o/d") {
		t.Errorf("4th repo should not be named (cap is 3): %q", many)
	}
	// t-nk62: the nudge must name the agent path (`--by agent`) so an agent does
	// not blindly run the bare `furrow review <repo>`, which records a HUMAN review
	// and silences the nudge without one happening.
	if !strings.Contains(one, "--by agent") {
		t.Errorf("unreviewed nudge must steer an agent to --by agent (bare review = human): %q", one)
	}
}

// TestPendingBodiesNote (t-nk62): the sync body nudge leads with the safe
// `-b <id>` and gates --all-bodies behind its yours-alone precondition, rather
// than offering --all-bodies as an equal one-word shortcut.
func TestPendingBodiesNote(t *testing.T) {
	note := pendingBodiesNote([]string{"t-a", "t-b"})
	if !strings.Contains(note, "sync -b <id>") {
		t.Errorf("nudge must lead with the safe -b <id> option: %q", note)
	}
	if !strings.Contains(note, "yours alone") {
		t.Errorf("nudge must carry --all-bodies' yours-alone precondition inline: %q", note)
	}
	// The safe option must come BEFORE --all-bodies in the text.
	if bi, ai := strings.Index(note, "-b <id>"), strings.Index(note, "--all-bodies"); bi < 0 || ai < 0 || bi > ai {
		t.Errorf("`-b <id>` must precede `--all-bodies`: %q", note)
	}
}

// review dispatches by argument shape: an id-shaped token stamps a task's
// reviewed field ({before,after,changed} envelope); anything else records a
// per-repo review (the repo record). --by selects the repo review's actor.
func TestReviewCommand(t *testing.T) {
	initStore(t)
	id := addTask(t, "a task", "-r", "akira-toriyama/furrow")

	// Task mode: reviewed changes, envelope reports it.
	out, code := run(t, "--json", "review", id)
	if code != 0 {
		t.Fatalf("review task exit = %d:\n%s", code, out)
	}
	var task struct {
		Changed []string `json:"changed"`
		After   struct {
			Reviewed *string `json:"reviewed"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("parse review task --json: %v\n%s", err, out)
	}
	if task.After.Reviewed == nil {
		t.Errorf("task reviewed should be set, got null:\n%s", out)
	}
	if !contains(task.Changed, "reviewed") {
		t.Errorf("changed should include reviewed, got %v", task.Changed)
	}

	// Repo mode (human): last_reviewed set, agent clock null.
	out, code = run(t, "--json", "review", "akira-toriyama/furrow")
	if code != 0 {
		t.Fatalf("review repo exit = %d:\n%s", code, out)
	}
	var rec struct {
		Repo              string  `json:"repo"`
		LastReviewed      *string `json:"last_reviewed"`
		LastAgentReviewed *string `json:"last_agent_reviewed"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("parse review repo --json: %v\n%s", err, out)
	}
	if rec.Repo != "akira-toriyama/furrow" || rec.LastReviewed == nil || rec.LastAgentReviewed != nil {
		t.Errorf("human repo review wrong: %+v\n%s", rec, out)
	}
	humanClock := *rec.LastReviewed

	// Repo mode (agent, short name): agent clock set, human clock preserved.
	out, code = run(t, "--json", "review", "furrow", "--by", "agent")
	if code != 0 {
		t.Fatalf("review repo --by agent exit = %d:\n%s", code, out)
	}
	_ = json.Unmarshal([]byte(out), &rec)
	if rec.LastAgentReviewed == nil {
		t.Errorf("agent sweep should set last_agent_reviewed:\n%s", out)
	}
	if rec.LastReviewed == nil || *rec.LastReviewed != humanClock {
		t.Errorf("agent sweep must not advance the human clock (was %q):\n%s", humanClock, out)
	}

	// A bad --by is a validation error.
	if _, code := run(t, "review", "furrow", "--by", "nobody"); code != 2 {
		t.Errorf("bad --by exit = %d, want 2", code)
	}
}

// The schema command gained a `repo` kind printing the repo-shard schema.
func TestSchemaRepoKind(t *testing.T) {
	out, code := run(t, "schema", "repo")
	if code != 0 {
		t.Fatalf("schema repo exit = %d", code)
	}
	if !strings.Contains(out, "furrow.repo.v1.json") || !strings.Contains(out, "last_agent_reviewed") {
		t.Errorf("schema repo missing expected content:\n%s", out)
	}
}

// An id-SHAPED argument that is not an existing task must fall through to repo
// mode: a repo short name like "t-digest" is id-shaped (^t-[0-9a-z]+$) but is a
// repo, not a task. And an id-shaped token that is neither a task nor a repo
// reports task-not-found (exit 1), which is the useful error.
func TestReviewIDShapedRepoShortNameFallsThroughToRepo(t *testing.T) {
	initStore(t)
	// A repo whose SHORT NAME is id-shaped.
	addTask(t, "a task", "-r", "tdunning/t-digest")

	out, code := run(t, "--json", "review", "t-digest")
	if code != 0 {
		t.Fatalf("review t-digest exit = %d (should be repo mode):\n%s", code, out)
	}
	var rec struct {
		Repo         string  `json:"repo"`
		LastReviewed *string `json:"last_reviewed"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("expected a repo record, got:\n%s", out)
	}
	if rec.Repo != "tdunning/t-digest" || rec.LastReviewed == nil {
		t.Errorf("t-digest should have been reviewed as a REPO, got %+v", rec)
	}

	// id-shaped, but neither an existing task nor a resolvable repo -> not found.
	if _, code := run(t, "review", "t-zzzz9"); code != 1 {
		t.Errorf("unknown id-shaped token exit = %d, want 1 (task not found)", code)
	}
}
