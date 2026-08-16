package core

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// -update regenerates the golden files: go test ./internal/core -update
var update = flag.Bool("update", false, "update golden files")

var testLanes = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}

// sampleIndex is a fixed, deliberately-unsorted index covering the tricky cases:
// CJK + HTML-ish characters in titles (must survive SetEscapeHTML(false)),
// an open task (closed == null) and a closed one, nil vs populated slices, and
// tasks out of canonical order so the marshaller must sort them.
func sampleIndex() *Index {
	mk := func(y int, mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, 1, 2, 3, 0, time.UTC)
	}
	closed := mk(2026, 6, 20)
	vi := func(n int) *int { return &n }
	return &Index{
		SchemaVersion: SchemaVersion,
		Tasks: []Task{
			{
				ID: "t-0003", Title: "done item <b>&amp;</b> 完了", Status: "done",
				Priority: 100, Labels: nil, Deps: nil, Refs: nil, Checklist: nil,
				Created: mk(2026, 6, 1), Updated: mk(2026, 6, 20), Closed: &closed,
				Body: BodyPath("t-0003"),
			},
			{
				ID: "t-0001", Title: "畝を一本進める", Status: "in-progress",
				Priority: 110, Value: vi(4), Effort: vi(2),
				Labels:    []string{"zmk", "canon"},
				Repos:     []string{"akira-toriyama/furrow", "akira-toriyama/chord"}, // unsorted: must sort
				Deps:      []string{"t-0002"},
				Refs:      []string{"docs/x.md#L10", "https://example.com"},
				Checklist: []ChecklistItem{{Text: "design", Done: true}, {Text: "ship", Done: false}},
				Created:   mk(2026, 6, 2), Updated: mk(2026, 6, 21), Closed: nil,
				Body: BodyPath("t-0001"),
			},
			{
				ID: "t-0002", Title: "ready task", Status: "in-progress",
				Priority: 100, Created: mk(2026, 6, 3), Updated: mk(2026, 6, 3),
				Body: BodyPath("t-0002"),
			},
		},
	}
}

// goldenBytes compares got against testdata/<name>, regenerating the file
// under -update — the ONE home of the golden ritual, shared by the task, epic,
// and repo shard goldens (they used to carry three identical copies of this
// block, t-eb6a).
func goldenBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output != %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestCanonicalizeSortsTasks pins the in-memory index's normal form: the
// stable lane-rank -> priority -> id order every list read renders in.
// (The index has no serialized form of its own — the on-disk byte recipe is
// pinned per shard by the golden tests and the frozen board.)
func TestCanonicalizeSortsTasks(t *testing.T) {
	idx := sampleIndex()
	Canonicalize(idx, testLanes)
	// in-progress ranks before done. Within in-progress, priority 100 (t-0002)
	// before 110 (t-0001). done is last (t-0003).
	want := []string{"t-0002", "t-0001", "t-0003"}
	for i, id := range want {
		if idx.Tasks[i].ID != id {
			t.Fatalf("task order[%d] = %s, want %s (full: %v)", i, idx.Tasks[i].ID, id, want)
		}
	}
}

// TestMarshalTaskEmptySets pins the shard shape of a MINIMAL task — the cases
// the noisy task_test golden cannot: nil collections emit [] (never null), an
// open task emits "closed": null, and unset estimates omit their keys
// entirely, so absent stays distinct from any score.
func TestMarshalTaskEmptySets(t *testing.T) {
	got, err := MarshalTask(&Task{
		ID: "t-0002", Title: "bare task", Status: "in-progress", Priority: 100,
		Created: time.Date(2026, 6, 3, 1, 2, 3, 0, time.UTC),
		Updated: time.Date(2026, 6, 3, 1, 2, 3, 0, time.UTC),
		Body:    BodyPath("t-0002"),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, needle := range []string{`"labels": []`, `"repos": []`, `"deps": []`, `"closed": null`} {
		if !bytes.Contains(got, []byte(needle)) {
			t.Errorf("shard must contain %s:\n%s", needle, s)
		}
	}
	for _, absent := range []string{`"value":`, `"effort":`, `"due":`} {
		if bytes.Contains(got, []byte(absent)) {
			t.Errorf("unset %s must be omitted from the shard:\n%s", absent, s)
		}
	}
}

func TestValidate(t *testing.T) {
	pat := regexp.MustCompile(`^t-[0-9]+$`)
	idx := &Index{
		SchemaVersion: SchemaVersion,
		Tasks: []Task{
			{ID: "t-0001", Status: "ready", Body: BodyPath("t-0001"), Deps: []string{"t-0099"},
				Repos: []string{"furrow", "akira-toriyama/furrow", "https://github.com/a/b"}}, // bare name + URL are not owner/repo
			{ID: "t-0001", Status: "nope", Body: "wrong/path.md"}, // dup id, bad lane, bad body
			{ID: "BAD", Status: "ready", Body: BodyPath("BAD")},   // id pattern fail
		},
	}
	ps := Validate(idx, testLanes, pat)

	want := map[string]bool{
		"duplicate id: t-0001":                                   false,
		`status "nope" is not a configured lane`:                 false,
		`body path "wrong/path.md" should be "bodies/t-0001.md"`: false,
		`dep "t-0099" does not exist`:                            false,
		`repo "furrow" is not owner/repo-shaped`:                 false,
		`repo "https://github.com/a/b" is not owner/repo-shaped`: false,
	}
	for _, p := range ps {
		if _, ok := want[p.Msg]; ok {
			want[p.Msg] = true
		}
	}
	for msg, found := range want {
		if !found {
			t.Errorf("expected a problem %q; got %+v", msg, ps)
		}
	}
	if !HasErrors(ps) {
		t.Error("expected HasErrors == true")
	}
	// problems must be deterministically ordered (errors before warns).
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Severity == SevWarn && ps[i].Severity == SevError {
			t.Errorf("problems not ordered: warn before error at %d", i)
		}
	}
}

func TestCanonicalizeDedupesSets(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-0001", Status: "ready", Body: BodyPath("t-0001"),
			Labels: []string{"x", "x", "a", "x"}, Deps: []string{"t-2", "t-2"},
			Repos: []string{"o/b", "o/a", "o/b"}},
	}}
	Canonicalize(idx, testLanes)
	got := idx.Tasks[0]
	if len(got.Labels) != 2 || got.Labels[0] != "a" || got.Labels[1] != "x" {
		t.Errorf("labels should be sorted+deduped to [a x], got %v", got.Labels)
	}
	if len(got.Repos) != 2 || got.Repos[0] != "o/a" || got.Repos[1] != "o/b" {
		t.Errorf("repos should be sorted+deduped to [o/a o/b], got %v", got.Repos)
	}
	if len(got.Deps) != 1 || got.Deps[0] != "t-2" {
		t.Errorf("deps should dedupe to [t-2], got %v", got.Deps)
	}
}

func TestROI(t *testing.T) {
	p := func(n int) *int { return &n }
	cases := []struct {
		name          string
		value, effort *int
		want          float64
	}{
		{"value over effort", p(4), p(2), 2},
		{"fractional", p(3), p(2), 1.5},
		{"effort one", p(5), p(1), 5},
		{"both unset is undefined", nil, nil, 0},
		{"value unset is undefined", nil, p(2), 0},
		{"effort unset is undefined", p(3), nil, 0},
		{"effort zero is undefined", p(3), p(0), 0},
		{"effort negative is undefined", p(3), p(-1), 0},
	}
	for _, c := range cases {
		got := (&Task{Value: c.value, Effort: c.effort}).ROI()
		if got != c.want {
			t.Errorf("%s: ROI = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCanonicalizeClampsEstimates(t *testing.T) {
	p := func(n int) *int { return &n }
	idx := &Index{Tasks: []Task{
		{ID: "t-0001", Status: "ready", Body: BodyPath("t-0001"), Value: p(9), Effort: p(0)},
		{ID: "t-0002", Status: "ready", Body: BodyPath("t-0002"), Value: p(-3), Effort: p(7)},
		{ID: "t-0003", Status: "ready", Body: BodyPath("t-0003"), Value: p(3), Effort: p(2)}, // in range
		{ID: "t-0004", Status: "ready", Body: BodyPath("t-0004")},                            // unset
	}}
	Canonicalize(idx, testLanes)
	want := []struct {
		id            string
		value, effort *int
	}{
		{"t-0001", p(5), p(1)}, // 9->5, 0->1
		{"t-0002", p(1), p(5)}, // -3->1, 7->5
		{"t-0003", p(3), p(2)}, // untouched
		{"t-0004", nil, nil},   // unset stays unset
	}
	for i, w := range want {
		got := idx.Tasks[i]
		if got.ID != w.id {
			t.Fatalf("task %d: id = %s, want %s (canonical sort changed?)", i, got.ID, w.id)
		}
		if !intpEq(got.Value, w.value) {
			t.Errorf("%s: value = %s, want %s", w.id, fmtIntp(got.Value), fmtIntp(w.value))
		}
		if !intpEq(got.Effort, w.effort) {
			t.Errorf("%s: effort = %s, want %s", w.id, fmtIntp(got.Effort), fmtIntp(w.effort))
		}
	}
}

func TestEstimateProblems(t *testing.T) {
	p := func(n int) *int { return &n }
	idx := &Index{Tasks: []Task{
		{ID: "t-0001", Value: p(9)},               // value too high
		{ID: "t-0002", Effort: p(0)},              // effort too low
		{ID: "t-0003", Value: p(3), Effort: p(2)}, // in range, no problem
		{ID: "t-0004"},                            // unset, no problem
	}}
	ps := EstimateProblems(idx)
	gotIDs := map[string]bool{}
	for _, pr := range ps {
		if pr.Severity != SevWarn {
			t.Errorf("estimate problems must be warns, got %q for %s", pr.Severity, pr.ID)
		}
		gotIDs[pr.ID] = true
	}
	if !gotIDs["t-0001"] || !gotIDs["t-0002"] {
		t.Errorf("expected warns for t-0001 (value) and t-0002 (effort); got %+v", ps)
	}
	if gotIDs["t-0003"] || gotIDs["t-0004"] {
		t.Errorf("in-range/unset tasks must not warn; got %+v", ps)
	}
}

func intpEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtIntp(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *p)
}

func TestLaneRankNoSentinelCollisionWithDuplicateLanes(t *testing.T) {
	// A duplicate-containing lane order must not let a real lane share the
	// unknown-lane sentinel rank.
	rank := laneRank([]string{"a", "a", "a", "b"})
	if laneRankOf(rank, "b") == laneRankOf(rank, "zzz") {
		t.Errorf("real lane b collides with unknown sentinel: b=%d unknown=%d",
			laneRankOf(rank, "b"), laneRankOf(rank, "zzz"))
	}
	if laneRankOf(rank, "a") != 0 || laneRankOf(rank, "b") != 1 {
		t.Errorf("first-occurrence ranks wrong: a=%d b=%d", laneRankOf(rank, "a"), laneRankOf(rank, "b"))
	}
}

func TestNextPriority(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-1", Status: "ready", Priority: 100},
		{ID: "t-2", Status: "ready", Priority: 130},
		{ID: "t-3", Status: "done", Priority: 200},
	}}
	if got := idx.NextPriority("ready", 100, 10); got != 140 {
		t.Errorf("NextPriority(ready) = %d, want 140", got)
	}
	if got := idx.NextPriority("backlog", 100, 10); got != 100 {
		t.Errorf("NextPriority(empty lane) = %d, want 100", got)
	}
}

func TestRandomIDSuffix(t *testing.T) {
	// Each byte is masked to its low 5 bits and indexes the 32-char alphabet.
	in := []byte{0, 1, 2, 31, 32, 33} // 32&31=0, 33&31=1
	suf, err := RandomIDSuffix(len(in), bytes.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := "012z01"; suf != want {
		t.Errorf("byte->charset mapping = %q, want %q", suf, want)
	}
	// length matches n and every char is in the alphabet.
	long := make([]byte, 64)
	for i := range long {
		long[i] = byte(i * 7)
	}
	s2, err := RandomIDSuffix(64, bytes.NewReader(long))
	if err != nil {
		t.Fatal(err)
	}
	if len(s2) != 64 {
		t.Fatalf("length = %d, want 64", len(s2))
	}
	for _, c := range s2 {
		if !bytes.ContainsRune([]byte(idAlphabet), c) {
			t.Errorf("char %q is not in the id alphabet", c)
		}
	}
	// a short read is an error, not a silent partial id.
	if _, err := RandomIDSuffix(4, bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("expected an error on short read")
	}
}

func TestDependsOn(t *testing.T) {
	// chain: t-1 -> t-2 -> t-3 (t-1 depends on t-2 depends on t-3).
	idx := &Index{Tasks: []Task{
		{ID: "t-1", Deps: []string{"t-2"}},
		{ID: "t-2", Deps: []string{"t-3"}},
		{ID: "t-3"},
		{ID: "t-4"}, // isolated
	}}
	cases := []struct {
		a, b string
		want bool
	}{
		{"t-1", "t-2", true},  // direct
		{"t-1", "t-3", true},  // transitive
		{"t-3", "t-1", false}, // reverse direction
		{"t-1", "t-1", false}, // no self-edge present
		{"t-1", "t-4", false}, // unrelated
		{"t-9", "t-1", false}, // unknown source has no out-edges
	}
	for _, c := range cases {
		if got := idx.DependsOn(c.a, c.b); got != c.want {
			t.Errorf("DependsOn(%s,%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}

	// A pre-existing cycle must not hang the walk.
	cyc := &Index{Tasks: []Task{
		{ID: "a", Deps: []string{"b"}},
		{ID: "b", Deps: []string{"a"}},
	}}
	if !cyc.DependsOn("a", "b") {
		t.Error("DependsOn should still resolve within a cyclic graph")
	}
}

func TestActionable(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-1", Status: "ready", Deps: []string{"t-2"}},
		{ID: "t-2", Status: "done"},
		{ID: "t-3", Status: "ready", Deps: []string{"t-9"}}, // unknown dep -> blocked
		{ID: "t-4", Status: "icebox"},
	}}
	terminal := map[string]bool{"done": true, "icebox": true}
	doneIDs := map[string]bool{"t-2": true}

	cases := map[string]bool{"t-1": true, "t-2": false, "t-3": false, "t-4": false}
	for id, want := range cases {
		tk, _ := idx.Find(id)
		if got := idx.Actionable(tk, terminal, doneIDs); got != want {
			t.Errorf("Actionable(%s) = %v, want %v", id, got, want)
		}
	}
}
