package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
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
	parked := addTask(t, "parked", "-s", "backlog")
	base := addTask(t, "base", "-s", "ready")
	waiter := addTask(t, "waiter", "-s", "ready", "--dep", base)

	// is:actionable — a next lane with every dep done.
	act := qIDs(t, "is:actionable")
	if !slices.Contains(act, cli) || !slices.Contains(act, base) {
		t.Errorf("is:actionable must include cli+base: %v", act)
	}
	if slices.Contains(act, waiter) || slices.Contains(act, docs) || slices.Contains(act, parked) {
		t.Errorf("is:actionable must exclude waiter/docs/parked: %v", act)
	}

	// is:blocked — the waiter has an unsatisfied dep.
	if b := qIDs(t, "is:blocked"); !slices.Contains(b, waiter) || slices.Contains(b, cli) {
		t.Errorf("is:blocked = %v; want just waiter", b)
	}

	// is:unfiled — every task here, since none was filed under a box. It replaces
	// v5's is:container/is:stuck, which described a box; a box is no longer a task,
	// so a TASK-level flag about one cannot exist.
	if u := qIDs(t, "is:unfiled"); len(u) != 5 {
		t.Errorf("is:unfiled = %v; want all five (none is filed)", u)
	}

	// comma-OR label + numeric comparison: cli(v4) and docs(v2) both qualify.
	or := qIDs(t, "label:cli,docs value:>=2")
	if !slices.Contains(or, cli) || !slices.Contains(or, docs) || len(or) != 2 {
		t.Errorf("label:cli,docs value:>=2 = %v; want cli+docs", or)
	}

	// negation: everything not in the backlog excludes docs+parked.
	if nb := qIDs(t, "-status:backlog"); slices.Contains(nb, docs) || slices.Contains(nb, parked) {
		t.Errorf("-status:backlog must exclude docs+parked: %v", nb)
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

	cases := []struct{ query, kind string }{
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
		if fe.Kind != c.kind {
			t.Errorf("ls -q %q kind = %q, want %q", c.query, fe.Kind, c.kind)
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

	// search <term> -q ANDs the query onto the text hits — asserted BOTH ways.
	// The negative alone was vacuous: the fixture's only chore task does not
	// match "fix" either, so [] was already the right answer with the predicate
	// ignored entirely (forcing it to `return false` left the suite green).
	if got := searchIDs(t, "fix", "label:cli"); !slices.Equal(got, []string{hot}) {
		t.Errorf("search fix -q label:cli = %v, want exactly [%s]", got, hot)
	}
	if got := searchIDs(t, "fix", "label:chore"); len(got) != 0 {
		t.Errorf("search fix -q label:chore must drop the cli task, got %v", got)
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
	if fe == nil || fe.Kind != "query-type" {
		t.Errorf("created:sometime should be query-type, got %+v", fe)
	}
}

// searchIDs runs `search <term> -q <query> --json` and returns the hit ids.
func searchIDs(t *testing.T, term, q string) []string {
	t.Helper()
	out, code := run(t, "--json", "search", term, "-q", q)
	if code != 0 {
		t.Fatalf("search %q -q %q exit = %d:\n%s", term, q, code, out)
	}
	var hits []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("parse search --json: %v\n%s", err, out)
	}
	ids := make([]string, len(hits))
	for i := range hits {
		ids[i] = hits[i].ID
	}
	slices.Sort(ids)
	return ids
}

// `is:stale` must use the CALL's staleness window on every command that takes
// -q, which is what cmd_query.go's help promises ("the flag and the query cannot
// disagree"). Only the ls path was pinned: swapping revisit's staleDays for the
// config default left the whole suite green.
func TestCLIQueryIsStaleUsesTheCallsWindow(t *testing.T) {
	initStore(t)
	id := addTask(t, "aging", "-s", "ready")

	// Age the task by rewriting its shard's updated stamp — the store is the
	// only clock a fresh process reads.
	ageTaskDays(t, id, 10)

	for _, c := range []struct {
		name string
		args []string
		want bool
	}{
		{"revisit window catches it", []string{"revisit", "--stale-days", "1", "-q", "is:stale"}, true},
		{"revisit window excludes it", []string{"revisit", "--stale-days", "30", "-q", "is:stale"}, false},
		{"stale disabled", []string{"revisit", "--stale-days", "0", "-q", "is:stale"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, code := run(t, append([]string{"--json"}, c.args...)...)
			if code != 0 {
				t.Fatalf("exit = %d:\n%s", code, out)
			}
			if got := strings.Contains(out, id); got != c.want {
				t.Errorf("contains(%s) = %t, want %t:\n%s", id, got, c.want, out)
			}
		})
	}
}

// ageTaskDays rewrites a task shard's `updated` stamp n days into the past.
func ageTaskDays(t *testing.T, id string, days int) {
	t.Helper()
	p := filepath.Join(os.Getenv(app.EnvDir), "tasks", id+".json")
	b, err := os.ReadFile(p) // #nosec G304 -- a path this test just created
	if err != nil {
		t.Fatal(err)
	}
	var shard map[string]any
	if err := json.Unmarshal(b, &shard); err != nil {
		t.Fatal(err)
	}
	when, err := time.Parse(time.RFC3339, shard["updated"].(string))
	if err != nil {
		t.Fatal(err)
	}
	shard["updated"] = when.AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	out, err := json.MarshalIndent(shard, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
