package cli

import (
	"encoding/json"
	"slices"
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
