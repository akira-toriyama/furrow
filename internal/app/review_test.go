package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// TestReviewTaskStampsReviewedNotUpdated is the load-bearing invariant: a review
// records `reviewed` but must NOT bump `updated` (reviewing changes no content,
// so staleness and --sort updated stay honest).
func TestReviewTaskStampsReviewedNotUpdated(t *testing.T) {
	a, clk := revisitApp()
	tk, err := a.Add("task", AddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	addedUpdated := tk.Updated

	clk.t = clk.t.AddDate(0, 0, 10) // 10 days later
	reviewed, err := a.ReviewTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Reviewed == nil || !reviewed.Reviewed.Equal(clk.t) {
		t.Errorf("reviewed = %v, want %v", reviewed.Reviewed, clk.t)
	}
	if !reviewed.Updated.Equal(addedUpdated) {
		t.Errorf("updated changed to %v (want preserved %v) — a review must not bump updated", reviewed.Updated, addedUpdated)
	}
}

func TestReviewTaskNotFound(t *testing.T) {
	a, _ := revisitApp()
	if _, err := a.ReviewTask("t-nope0"); err == nil {
		t.Error("expected NotFound reviewing a missing task")
	}
}

// TestReviewRepoTwoClocks: a human review advances last_reviewed; a subsequent
// agent sweep advances last_agent_reviewed WITHOUT resetting the human clock.
func TestReviewRepoTwoClocks(t *testing.T) {
	a, clk := revisitApp()
	// Put the repo in the universe so a short name would resolve too.
	a.Add("t", AddOpts{Repos: []string{"akira-toriyama/furrow"}})

	human, err := a.ReviewRepo("akira-toriyama/furrow", false)
	if err != nil {
		t.Fatal(err)
	}
	if human.LastReviewed == nil || !human.LastReviewed.Equal(clk.t) {
		t.Fatalf("human review: last_reviewed = %v, want %v", human.LastReviewed, clk.t)
	}
	if human.LastAgentReviewed != nil {
		t.Errorf("human review should not set last_agent_reviewed, got %v", human.LastAgentReviewed)
	}
	humanAt := clk.t

	clk.t = clk.t.AddDate(0, 0, 5)
	agent, err := a.ReviewRepo("furrow", true) // short name, agent
	if err != nil {
		t.Fatal(err)
	}
	if agent.LastAgentReviewed == nil || !agent.LastAgentReviewed.Equal(clk.t) {
		t.Errorf("agent sweep: last_agent_reviewed = %v, want %v", agent.LastAgentReviewed, clk.t)
	}
	if agent.LastReviewed == nil || !agent.LastReviewed.Equal(humanAt) {
		t.Errorf("agent sweep must NOT advance the human clock: last_reviewed = %v, want %v", agent.LastReviewed, humanAt)
	}
}

// TestRevisitSummaryUnreviewedNudge exercises the sync staleness nudge: a stale
// human review surfaces (with a day count), a fresh review clears it, an
// agent-only repo never nudges, and stale_after_days = 0 disables the nudge.
func TestRevisitSummaryUnreviewedNudge(t *testing.T) {
	cfg := config.Default()
	cfg.ReviewStaleAfterDays = 14
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	a := NewWithStore(st, cfg, clk)

	a.ReviewRepo("o/reviewed", false) // human review at T0
	a.ReviewRepo("o/agentonly", true) // agent-only: no human clock

	// 20 days later: reviewed repo is now stale (>14d); agent-only never nudges.
	clk.t = clk.t.AddDate(0, 0, 20)
	sum, err := a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Unreviewed) != 1 || sum.Unreviewed[0].Repo != "o/reviewed" {
		t.Fatalf("Unreviewed = %+v, want just o/reviewed", sum.Unreviewed)
	}
	if sum.Unreviewed[0].Days != 20 {
		t.Errorf("Days = %d, want 20", sum.Unreviewed[0].Days)
	}

	// A fresh review clears the nudge.
	a.ReviewRepo("o/reviewed", false)
	sum, _ = a.RevisitSummary(QueryOpts{}, 30)
	if len(sum.Unreviewed) != 0 {
		t.Errorf("after re-review, Unreviewed = %+v, want empty", sum.Unreviewed)
	}

	// stale_after_days = 0 disables the nudge entirely.
	clk.t = clk.t.AddDate(0, 0, 90)
	cfg.ReviewStaleAfterDays = 0
	sum, _ = a.RevisitSummary(QueryOpts{}, 30)
	if len(sum.Unreviewed) != 0 {
		t.Errorf("with stale_after_days=0, Unreviewed = %+v, want empty (disabled)", sum.Unreviewed)
	}
}

// TestRevisitSummaryUnreviewedScoped: a set ScopeRepo limits the nudge to that
// repo (the sync summary is repo-scoped).
func TestRevisitSummaryUnreviewedScoped(t *testing.T) {
	cfg := config.Default()
	cfg.ReviewStaleAfterDays = 14
	st := memstore.New(cfg.IDPrefix, cfg.IDWidth)
	clk := &fixedClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	a := NewWithStore(st, cfg, clk)

	a.ReviewRepo("o/one", false)
	a.ReviewRepo("o/two", false)
	clk.t = clk.t.AddDate(0, 0, 30) // both stale

	scoped, _ := a.RevisitSummary(QueryOpts{ScopeRepo: "o/one"}, 30)
	if len(scoped.Unreviewed) != 1 || scoped.Unreviewed[0].Repo != "o/one" {
		t.Errorf("scoped Unreviewed = %+v, want just o/one", scoped.Unreviewed)
	}
	board, _ := a.RevisitSummary(QueryOpts{}, 30)
	if len(board.Unreviewed) != 2 {
		t.Errorf("board-wide Unreviewed = %+v, want both repos", board.Unreviewed)
	}
}

// TestReviewRepoResolvesAgainstExistingShards: a repo that exists ONLY as a
// review shard (never attached to a task, not the checkout's repo) must still
// resolve — by short name, and to its canonical casing — so a second review
// never forks a duplicate shard for the same repo.
func TestReviewRepoResolvesAgainstExistingShards(t *testing.T) {
	a, _ := revisitApp()

	// First review creates the shard. The repo is in no task and no board scope.
	first, err := a.ReviewRepo("akira-toriyama/Chord", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Repo != "akira-toriyama/Chord" {
		t.Fatalf("first review repo = %q", first.Repo)
	}

	// A short name must now resolve against that existing shard.
	byShort, err := a.ReviewRepo("chord", false)
	if err != nil {
		t.Fatalf("short name should resolve against the existing review shard: %v", err)
	}
	if byShort.Repo != "akira-toriyama/Chord" {
		t.Errorf("short-name review resolved to %q, want the canonical %q", byShort.Repo, "akira-toriyama/Chord")
	}

	// A differently-cased full name must canonicalize, not fork a second shard.
	if _, err := a.ReviewRepo("akira-toriyama/chord", false); err != nil {
		t.Fatal(err)
	}
	recs, err := a.Store.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("expected exactly 1 repo shard (no fork), got %d: %+v", len(recs), recs)
	}
}
