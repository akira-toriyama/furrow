package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// qIDs runs `ls -q <query> --json` and returns the matched ids (fails on error).
func qIDs(t *testing.T, query string) []string {
	t.Helper()
	out, code := run(t, "--json", "ls", "-q", query)
	if code != 0 {
		t.Fatalf("ls -q %q exit = %d:\n%s", query, code, out)
	}
	var tasks []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("parse ls -q %q: %v\n%s", query, err, out)
	}
	ids := make([]string, len(tasks))
	for i, x := range tasks {
		ids[i] = x.ID
	}
	return ids
}

// TestLsQueryFilters exercises the -q typed query end-to-end (parse → compile →
// filter) across the qualifier families: is: flags, comma-OR, negation, and a
// numeric comparison.
func TestLsQueryFilters(t *testing.T) {
	initStore(t)
	cli := addTask(t, "cli bug fix", "-s", "ready", "-l", "cli,bug", "--value", "4", "--effort", "2")
	docs := addTask(t, "docs sweep", "-s", "backlog", "-l", "docs", "--value", "2", "--effort", "1")
	epic := addTask(t, "an epic", "-s", "backlog", "--type", "epic")
	base := addTask(t, "base", "-s", "ready")
	waiter := addTask(t, "waiter", "-s", "ready", "--dep", base)

	// is:actionable — a next lane, deps done, not a container.
	act := qIDs(t, "is:actionable")
	if !slices.Contains(act, cli) || !slices.Contains(act, base) {
		t.Errorf("is:actionable must include cli+base: %v", act)
	}
	if slices.Contains(act, waiter) || slices.Contains(act, docs) || slices.Contains(act, epic) {
		t.Errorf("is:actionable must exclude waiter/docs/epic: %v", act)
	}

	// is:blocked — the waiter has an unsatisfied dep.
	if b := qIDs(t, "is:blocked"); !slices.Contains(b, waiter) || slices.Contains(b, cli) {
		t.Errorf("is:blocked = %v; want just waiter", b)
	}

	// is:container — the epic.
	if c := qIDs(t, "is:container"); !slices.Contains(c, epic) || len(c) != 1 {
		t.Errorf("is:container = %v; want just the epic", c)
	}

	// comma-OR label + numeric comparison: cli(v4) and docs(v2) both qualify.
	or := qIDs(t, "label:cli,docs value:>=2")
	if !slices.Contains(or, cli) || !slices.Contains(or, docs) || len(or) != 2 {
		t.Errorf("label:cli,docs value:>=2 = %v; want cli+docs", or)
	}

	// negation: everything not in the backlog excludes docs+epic.
	if nb := qIDs(t, "-status:backlog"); slices.Contains(nb, docs) || slices.Contains(nb, epic) {
		t.Errorf("-status:backlog must exclude docs+epic: %v", nb)
	}

	// has:/no: presence.
	if d := qIDs(t, "no:value"); !slices.Contains(d, base) || slices.Contains(d, cli) {
		t.Errorf("no:value = %v; want the estimate-less tasks (base), not cli", d)
	}

	// free text over the title (case-insensitive substring).
	if ft := qIDs(t, "BUG"); !slices.Contains(ft, cli) || len(ft) != 1 {
		t.Errorf("free-text BUG = %v; want just 'cli bug fix'", ft)
	}
}

// TestLsQueryErrors pins the exit-2 + stable-id + candidates contract.
func TestLsQueryErrors(t *testing.T) {
	initStore(t)
	addTask(t, "x", "-s", "ready")

	cases := []struct{ query, id string }{
		{"is:bogus", "query-unknown-flag"},
		{"xyz:1", "query-unknown-field"},
		{"status:>ready", "query-type"},
		{"value:notanumber", "query-type"},
		{`title:'unterminated`, "query-parse"},
	}
	for _, c := range cases {
		fe, _ := runErr(t, "ls", "-q", c.query)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Errorf("ls -q %q should be exit 2, got %+v", c.query, fe)
			continue
		}
		if fe.ID != c.id {
			t.Errorf("ls -q %q id = %q, want %q", c.query, fe.ID, c.id)
		}
	}

	// An unknown lane VALUE reuses the lane vocabulary in candidates.
	fe, _ := runErr(t, "ls", "-q", "status:nope")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("status:nope should be exit 2 with lane candidates, got %+v", fe)
	}
}

// TestQueryOnEveryFilteringRead pins D4: -q is wired on next, revisit, stats,
// and search with the same AND semantics and the same exit-2 error contract as
// ls — one evaluator, five commands. (brief is deliberately excluded.)
func TestQueryOnEveryFilteringRead(t *testing.T) {
	initStore(t)
	hot := addTask(t, "hot fix", "-s", "ready", "-l", "cli")
	cold := addTask(t, "cold chore", "-s", "ready", "-l", "chore")

	// next -q narrows the actionable set.
	out, code := run(t, "--json", "next", "-q", "label:cli")
	if code != 0 || !strings.Contains(out, hot) || strings.Contains(out, cold) {
		t.Errorf("next -q label:cli should keep only the cli task: code=%d\n%s", code, out)
	}

	// revisit -q narrows the flagged set (both tasks flag no_repo/value_unset;
	// the query keeps one).
	out, code = run(t, "--json", "revisit", "-q", "label:cli")
	if code != 0 || !strings.Contains(out, hot) || strings.Contains(out, cold) {
		t.Errorf("revisit -q label:cli should keep only the cli task: code=%d\n%s", code, out)
	}

	// stats -q describes the queried slice.
	out, code = run(t, "--json", "stats", "-q", "label:cli")
	if code != 0 {
		t.Fatalf("stats -q exit = %d:\n%s", code, out)
	}
	var st struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("parse stats --json: %v\n%s", err, out)
	}
	if st.Total != 1 {
		t.Errorf("stats -q label:cli total = %d, want 1", st.Total)
	}

	// search <term> -q ANDs the query onto the text hits.
	out, code = run(t, "--json", "search", "fix", "-q", "label:chore")
	if code != 0 || strings.Contains(out, hot) {
		t.Errorf("search fix -q label:chore must drop the cli task: code=%d\n%s", code, out)
	}

	// The exit-2 error contract propagates through every newly-wired command.
	for _, args := range [][]string{
		{"next", "-q", "is:bogus"},
		{"revisit", "-q", "xyz:1"},
		{"stats", "-q", "status:nope"},
		{"search", "fix", "-q", "value:notanumber"},
	} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Errorf("%v should be exit 2, got %+v", args, fe)
		}
	}
}

// TestQueryFreeTextReachesBody pins D1 at the CLI: a bare -q word matches the
// BODY too (search's matcher), so `ls -q foo` finds what `furrow search foo`
// finds; body: is the explicit qualifier; quoted title: is exact.
func TestQueryFreeTextReachesBody(t *testing.T) {
	initStore(t)
	inBody := addTask(t, "plain title", "-s", "ready", "--body", "# plain\n\na zebra paragraph\n")
	// An explicit body: a bare add would seed "# zebra title" and give this task
	// a BODY hit too, blurring the title-vs-body assertion below.
	inTitle := addTask(t, "zebra title", "-s", "ready", "--body", "# t\n")

	ft := qIDs(t, "zebra")
	if !slices.Contains(ft, inBody) || !slices.Contains(ft, inTitle) {
		t.Errorf("free text must span title+body: %v", ft)
	}
	// Equivalence with furrow search (same matcher, same hits).
	out, code := run(t, "--json", "search", "zebra")
	if code != 0 || !strings.Contains(out, inBody) || !strings.Contains(out, inTitle) {
		t.Errorf("search zebra should find both: code=%d\n%s", code, out)
	}
	if b := qIDs(t, "body:zebra"); !slices.Contains(b, inBody) || slices.Contains(b, inTitle) {
		t.Errorf("body:zebra = %v; want only the body hit", b)
	}
	if e := qIDs(t, "title:'zebra title'"); !slices.Contains(e, inTitle) || len(e) != 1 {
		t.Errorf("quoted title exact = %v; want only %s", e, inTitle)
	}
}

// TestQueryArchivedBodies pins that `ls --archived -q` resolves body-matching
// terms in the ARCHIVE store's bodies — the archive keeps its own bodies/, so
// the hot store's loader would silently search the wrong (missing) files.
func TestQueryArchivedBodies(t *testing.T) {
	initStore(t)
	id := addTask(t, "retired", "-s", "ready", "--body", "# retired\n\nzulu paragraph\n")
	if _, code := run(t, "done", id); code != 0 {
		t.Fatalf("done exit = %d", code)
	}
	if out, code := run(t, "archive", id, "--yes"); code != 0 {
		t.Fatalf("archive exit = %d:\n%s", code, out)
	}
	// Gone from the hot board…
	if got := qIDs(t, "body:zulu"); len(got) != 0 {
		t.Errorf("hot board should no longer match: %v", got)
	}
	// …but its body is still queryable in the archive.
	out, code := run(t, "--json", "ls", "--archived", "-q", "body:zulu")
	if code != 0 || !strings.Contains(out, id) {
		t.Errorf("ls --archived -q body:zulu should find %s: code=%d\n%s", id, code, out)
	}
}

// TestQueryDatesCLI is the wall-clock sanity check for the date qualifiers (the
// precise boundary tests live in internal/app with the fixed clock): a task
// created just now is inside the last hour and not older than a day.
func TestQueryDatesCLI(t *testing.T) {
	initStore(t)
	id := addTask(t, "fresh", "-s", "ready")

	if got := qIDs(t, "created:>=-1h"); !slices.Contains(got, id) {
		t.Errorf("created:>=-1h should include the just-created task: %v", got)
	}
	if got := qIDs(t, "created:<-1d"); len(got) != 0 {
		t.Errorf("created:<-1d should be empty on a fresh board: %v", got)
	}
	if got := qIDs(t, "updated:-1d..*"); !slices.Contains(got, id) {
		t.Errorf("updated:-1d..* should include the fresh task: %v", got)
	}
	// A malformed date is exit 2 with the stable id.
	fe, _ := runErr(t, "ls", "-q", "created:sometime")
	if fe == nil || fe.ID != "query-type" {
		t.Errorf("created:sometime should be query-type, got %+v", fe)
	}
}
