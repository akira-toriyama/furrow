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

func TestMarshalGolden(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "index.golden.json")
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
		t.Errorf("marshal output != golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestMarshalDeterministic: the canonical contract — re-marshalling an index
// parsed from canonical bytes yields byte-identical output (no churn), AND the
// sort is stable regardless of input task order.
func TestMarshalDeterministic(t *testing.T) {
	first, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Unmarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(parsed, testLanes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-marshal not byte-stable\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMarshalDetails(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Error("output must end with a trailing newline")
	}
	// SetEscapeHTML(false): CJK and < > & survive literally.
	for _, lit := range []string{"畝を一本進める", "<b>&amp;</b>", "完了"} {
		if !bytes.Contains(got, []byte(lit)) {
			t.Errorf("expected literal %q to survive un-escaped, output:\n%s", lit, s)
		}
	}
	// [] not null: a task with no labels still emits "labels": [].
	if !bytes.Contains(got, []byte(`"labels": []`)) {
		t.Errorf("nil slices must marshal as [], not null:\n%s", s)
	}
	// repos follows the same []-not-null set contract as labels.
	if !bytes.Contains(got, []byte(`"repos": []`)) {
		t.Errorf("a task with no repos must emit \"repos\": []:\n%s", s)
	}
	// open task -> "closed": null
	if !bytes.Contains(got, []byte(`"closed": null`)) {
		t.Errorf("open task must serialize closed as null:\n%s", s)
	}
	// a set estimate serializes its key; an unset one is omitted entirely
	// (omitempty on the *int), so absent stays distinct from any score.
	if !bytes.Contains(got, []byte(`"value": 4`)) || !bytes.Contains(got, []byte(`"effort": 2`)) {
		t.Errorf("a task with value/effort must serialize them:\n%s", s)
	}
	// t-0002 has no estimate; "value"/"effort" must not appear for it. The whole
	// index has exactly one estimate-bearing task, so a single occurrence each.
	if n := bytes.Count(got, []byte(`"value":`)); n != 1 {
		t.Errorf("unset value must be omitted: want 1 \"value\" key, got %d:\n%s", n, s)
	}
	if n := bytes.Count(got, []byte(`"effort":`)); n != 1 {
		t.Errorf("unset effort must be omitted: want 1 \"effort\" key, got %d:\n%s", n, s)
	}
}

func TestCanonicalSort(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	// in-progress (rank 3) sorts before done (rank 4). Within in-progress,
	// priority 100 (t-0002) before 110 (t-0001). done is last (t-0003).
	order := []string{`"id": "t-0002"`, `"id": "t-0001"`, `"id": "t-0003"`}
	last := -1
	for _, needle := range order {
		i := bytes.Index(got, []byte(needle))
		if i < 0 {
			t.Fatalf("missing %s", needle)
		}
		if i < last {
			t.Errorf("task order wrong: %s appeared before the previous id\n%s", needle, got)
		}
		last = i
	}
}

func TestValidate(t *testing.T) {
	pat := regexp.MustCompile(`^t-[0-9]+$`)
	idx := &Index{
		SchemaVersion: SchemaVersion,
		Tasks: []Task{
			{ID: "t-0001", Status: "ready", Body: BodyPath("t-0001"), Deps: []string{"t-0099"},
				Repos: []string{"furrow", "akira-toriyama/furrow", "https://github.com/a/b"}}, // bare name + URL are not owner/repo
			{ID: "t-0001", Status: "nope", Body: "wrong/path.md", Parent: "t-0404"}, // dup id, bad lane, bad body, missing parent
			{ID: "BAD", Status: "ready", Body: BodyPath("BAD")},                     // id pattern fail
		},
	}
	ps := Validate(idx, testLanes, pat)

	want := map[string]bool{
		"duplicate id: t-0001":                                   false,
		`status "nope" is not a configured lane`:                 false,
		`body path "wrong/path.md" should be "bodies/t-0001.md"`: false,
		`parent "t-0404" does not exist`:                         false,
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
